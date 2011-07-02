/*
Copyright 2011 Google Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

     http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// Package memcache provides a client for the memcached cache server.
package memcache

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Similar to:
// http://code.google.com/appengine/docs/go/memcache/reference.html

var (
	// ErrCacheMiss means that a Get failed because the item wasn't present.
	ErrCacheMiss = os.NewError("memcache: cache miss")

	// ErrCASConflict means that a CompareAndSwap call failed due to the
	// cached value being modified between the Get and the CompareAndSwap.
	// If the cached value was simply evicted rather than replaced,
	// ErrNotStored will be returned instead.
	ErrCASConflict = os.NewError("memcache: compare-and-swap conflict")

	// ErrNotStored means that a conditional write operation (i.e. Add or
	// CompareAndSwap) failed because the condition was not satisfied.
	ErrNotStored = os.NewError("memcache: item not stored")

	// ErrServer means that a server error occurred.
	ErrServerError = os.NewError("memcache: server error")

	// ErrNoStats means that no statistics were available.
	ErrNoStats = os.NewError("memcache: no statistics available")

	// ErrMalformedKey is returned when an invalid key is used.
	// Keys must be at maximum 250 bytes long, ASCII, and not
	// contain whitespace or control characters.
	ErrMalformedKey = os.NewError("malformed: key is too long or contains invalid characters")

	// ErrNoServers is returned when no servers are configured or available.
	ErrNoServers = os.NewError("memcache: no servers configured or available")
)

// DefaultTimeoutNanos is the default socket read/write timeout, in nanoseconds.
const DefaultTimeoutNanos = 100e6 // 100 ms

const (
	buffered            = 8 // arbitrary buffered channel size, for readability
	maxIdleConnsPerAddr = 2 // TODO(bradfitz): make this configurable?
)

// resumableError returns true if err is only a protocol-level cache error.
// This is used to determine whether or not a server connection should
// be re-used or not. If an error occurs, by default we don't reuse the
// connection, unless it was just a cache error.
func resumableError(err os.Error) bool {
	switch err {
	case ErrCacheMiss, ErrCASConflict, ErrNotStored, ErrMalformedKey:
		return true
	}
	return false
}

func legalKey(key string) bool {
	if len(key) > 250 {
		return false
	}
	for i := 0; i < len(key); i++ {
		if key[i] <= ' ' || key[i] > 0x7e {
			return false
		}
	}
	return true
}

var (
	crlf            = []byte("\r\n")
	resultStored    = []byte("STORED\r\n")
	resultNotStored = []byte("NOT_STORED\r\n")
	resultExists    = []byte("EXISTS\r\n")
	resultNotFound  = []byte("NOT_FOUND\r\n")
	resultDeleted   = []byte("DELETED\r\n")
	resultEnd       = []byte("END\r\n")

	resultClientErrorPrefix = []byte("CLIENT_ERROR ")
)

// New returns a memcache client using the provided server(s)
// with equal weight. If a server is listed multiple times,
// it gets a proportional amount of weight.
func New(server ...string) *Client {
	ss := new(ServerList)
	ss.SetServers(server...)
	return NewFromSelector(ss)
}

// NewFromSelector returns a new Client using the provided ServerSelector.
func NewFromSelector(ss ServerSelector) *Client {
	return &Client{selector: ss}
}

// Client is a memcache client.
// It is safe for unlocked use by multiple concurrent goroutines.
type Client struct {
	// TimeoutNanos specifies the socket read/write timeout.
	// If zero, DefaultTimeoutNanos is used.
	TimeoutNanos int64

	selector ServerSelector

	lk       sync.Mutex
	freeconn map[net.Addr][]*conn
}

// Item is an item to be got or stored in a memcached server.
type Item struct {
	// Key is the Item's key (250 bytes maximum).
	Key string

	// Value is the Item's value.
	Value []byte

	// Object is the Item's value for use with a Codec.
	Object interface{}

	// Flags are server-opaque flags whose semantics are entirely
	// up to the app.
	Flags uint32

	// Expiration is the cache expiration time, in seconds: either a relative
	// time from now (up to 1 month), or an absolute Unix epoch time.
	// Zero means the Item has no expiration time.
	Expiration int32

	// Compare and swap ID.
	casid uint64
}

// conn is a connection to a server.
type conn struct {
	nc   net.Conn
	rw   *bufio.ReadWriter
	addr net.Addr
	c    *Client
}

// release returns this connection back to the client's free pool
func (cn *conn) release() {
	cn.c.putFreeConn(cn.addr, cn)
}

// condRelease releases this connection if the error pointed to by err
// is is nil (not an error) or is only a protocol level error (e.g. a
// cache miss).  The purpose is to not recycle TCP connections that
// are bad.
func (cn *conn) condRelease(err *os.Error) {
	if *err == nil || resumableError(*err) {
		cn.release()
	}
}

func (c *Client) putFreeConn(addr net.Addr, cn *conn) {
	c.lk.Lock()
	defer c.lk.Unlock()
	if c.freeconn == nil {
		c.freeconn = make(map[net.Addr][]*conn)
	}
	freelist := c.freeconn[addr]
	if len(freelist) >= maxIdleConnsPerAddr {
		cn.nc.Close()
		return
	}
	c.freeconn[addr] = append(freelist, cn)
}

func (c *Client) getFreeConn(addr net.Addr) (cn *conn, ok bool) {
	c.lk.Lock()
	defer c.lk.Unlock()
	if c.freeconn == nil {
		return nil, false
	}
	freelist, ok := c.freeconn[addr]
	if !ok || len(freelist) == 0 {
		return nil, false
	}
	cn = freelist[len(freelist)-1]
	c.freeconn[addr] = freelist[:len(freelist)-1]
	return cn, true
}

func (c *Client) netTimeoutNs() int64 {
	if c.TimeoutNanos != 0 {
		return c.TimeoutNanos
	}
	return DefaultTimeoutNanos
}

// ConnectTimeoutError is the error type used when it takes
// too long to connect to the desired host. This level of
// detail can generally be ignored.
type ConnectTimeoutError struct {
	Addr net.Addr
}

func (cte *ConnectTimeoutError) String() string {
	return "memcache: connect timeout to " + cte.Addr.String()
}

func (c *Client) dial(addr net.Addr) (net.Conn, os.Error) {
	type connError struct {
		cn  net.Conn
		err os.Error
	}
	ch := make(chan connError)
	go func() {
		nc, err := net.Dial(addr.Network(), addr.String())
		ch <- connError{nc, err}
	}()
	select {
	case ce := <-ch:
		return ce.cn, ce.err
	case <-time.After(c.netTimeoutNs()):
		// Too slow. Fall through.
	}
	// Close the conn if it does end up finally coming in
	go func() {
		ce := <-ch
		if ce.err == nil {
			ce.cn.Close()
		}
	}()
	return nil, &ConnectTimeoutError{addr}
}

func (c *Client) getConn(addr net.Addr) (*conn, os.Error) {
	cn, ok := c.getFreeConn(addr)
	if ok {
		return cn, nil
	}
	nc, err := c.dial(addr)
	if err != nil {
		return nil, err
	}
	nc.SetTimeout(c.netTimeoutNs())
	return &conn{
		nc:   nc,
		addr: addr,
		rw:   bufio.NewReadWriter(bufio.NewReader(nc), bufio.NewWriter(nc)),
		c:    c,
	}, nil
}

func (c *Client) onItem(item *Item, fn func(*Client, *bufio.ReadWriter, *Item) os.Error) os.Error {
	addr, err := c.selector.PickServer(item.Key)
	if err != nil {
		return err
	}
	cn, err := c.getConn(addr)
	if err != nil {
		return err
	}
	defer cn.condRelease(&err)
	if err := fn(c, cn.rw, item); err != nil {
		return err
	}
	return nil
}

// Get gets the item for the given key. ErrCacheMiss is returned for a
// memcache cache miss. The key must be at most 250 bytes in length.
func (c *Client) Get(key string) (item *Item, err os.Error) {
	err = c.withKeyAddr(key, func(addr net.Addr) os.Error {
		return c.getFromAddr(addr, []string{key}, func(it *Item) { item = it })
	})
	if err == nil && item == nil {
		err = ErrCacheMiss
	}
	return
}

func (c *Client) withKeyAddr(key string, fn func(net.Addr) os.Error) (err os.Error) {
	if !legalKey(key) {
		return ErrMalformedKey
	}
	addr, err := c.selector.PickServer(key)
	if err != nil {
		return err
	}
	return fn(addr)
}

func (c *Client) withAddrRw(addr net.Addr, fn func(*bufio.ReadWriter) os.Error) (err os.Error) {
	cn, err := c.getConn(addr)
	if err != nil {
		return err
	}
	defer cn.condRelease(&err)
	return fn(cn.rw)
}

func (c *Client) withKeyRw(key string, fn func(*bufio.ReadWriter) os.Error) os.Error {
	return c.withKeyAddr(key, func(addr net.Addr) os.Error {
		return c.withAddrRw(addr, fn)
	})
}

func (c *Client) getFromAddr(addr net.Addr, keys []string, cb func(*Item)) os.Error {
	return c.withAddrRw(addr, func(rw *bufio.ReadWriter) os.Error {
		if _, err := fmt.Fprintf(rw, "gets %s\r\n", strings.Join(keys, " ")); err != nil {
			return err
		}
		if err := rw.Flush(); err != nil {
			return err
		}
		if err := parseGetResponse(rw.Reader, cb); err != nil {
			return err
		}
		return nil
	})
}

// GetMulti is a batch version of Get. The returned map from keys to
// items may have fewer elements than the input slice, due to memcache
// cache misses. Each key must be at most 250 bytes in length.
// If no error is returned, the returned map will also be non-nil.
func (c *Client) GetMulti(keys []string) (map[string]*Item, os.Error) {
	var lk sync.Mutex
	m := make(map[string]*Item)
	addItemToMap := func(it *Item) {
		lk.Lock()
		defer lk.Unlock()
		m[it.Key] = it
	}

	keyMap := make(map[net.Addr][]string)
	for _, key := range keys {
		if !legalKey(key) {
			return nil, ErrMalformedKey
		}
		addr, err := c.selector.PickServer(key)
		if err != nil {
			return nil, err
		}
		keyMap[addr] = append(keyMap[addr], key)
	}

	ch := make(chan os.Error, buffered)
	for addr, keys := range keyMap {
		go func(addr net.Addr, keys []string) {
			ch <- c.getFromAddr(addr, keys, addItemToMap)
		}(addr, keys)
	}

	var err os.Error
	for _ = range keyMap {
		if ge := <-ch; ge != nil {
			err = ge
		}
	}
	return m, err
}

// parseGetResponse reads a GET response from r and calls cb for each
// read and allocated Item
func parseGetResponse(r *bufio.Reader, cb func(*Item)) os.Error {
	for {
		line, err := r.ReadSlice('\n')
		if err != nil {
			return err
		}
		if bytes.Equal(line, resultEnd) {
			return nil
		}
		it := new(Item)
		var size int
		n, err := fmt.Sscanf(string(line), "VALUE %s %d %d %d\r\n",
			&it.Key, &it.Flags, &size, &it.casid)
		if err != nil {
			return err
		}
		if n != 4 {
			return fmt.Errorf("memcache: unexpected line in get response: %q", string(line))
		}
		it.Value, err = ioutil.ReadAll(io.LimitReader(r, int64(size)+2))
		if err != nil {
			return err
		}
		if !bytes.HasSuffix(it.Value, crlf) {
			return fmt.Errorf("memcache: corrupt get result read")
		}
		it.Value = it.Value[:size]
		cb(it)
	}
	panic("unreached")
}

// Set writes the given item, unconditionally.
func (c *Client) Set(item *Item) os.Error {
	return c.onItem(item, (*Client).set)
}

func (c *Client) set(rw *bufio.ReadWriter, item *Item) os.Error {
	return c.populateOne(rw, "set", item)
}

// Add writes the given item, if no value already exists for its
// key. ErrNotStored is returned if that condition is not met.
func (c *Client) Add(item *Item) os.Error {
	return c.onItem(item, (*Client).add)
}

func (c *Client) add(rw *bufio.ReadWriter, item *Item) os.Error {
	return c.populateOne(rw, "add", item)
}

// CompareAndSwap writes the given item that was previously returned
// by Get, if the value was neither modified or evicted between the
// Get and the CompareAndSwap calls. The item's Key should not change
// between calls but all other item fields may differ. ErrCASConflict
// is returned if the value was modified in between the
// calls. ErrNotStored is returned if the value was evicted in between
// the calls.
func (c *Client) CompareAndSwap(item *Item) os.Error {
	return c.onItem(item, (*Client).cas)
}

func (c *Client) cas(rw *bufio.ReadWriter, item *Item) os.Error {
	return c.populateOne(rw, "cas", item)
}

func (c *Client) populateOne(rw *bufio.ReadWriter, verb string, item *Item) os.Error {
	if !legalKey(item.Key) {
		return ErrMalformedKey
	}
	var err os.Error
	if verb == "cas" {
		_, err = fmt.Fprintf(rw, "%s %s %d %d %d %d %d\r\n",
			verb, item.Key, item.Flags, item.Expiration, len(item.Value), item.casid)
	} else {
		_, err = fmt.Fprintf(rw, "%s %s %d %d %d %d\r\n",
			verb, item.Key, item.Flags, item.Expiration, len(item.Value))
	}
	if err != nil {
		return err
	}
	if _, err = rw.Write(item.Value); err != nil {
		return err
	}
	if _, err := rw.Write(crlf); err != nil {
		return err
	}
	if err := rw.Flush(); err != nil {
		return err
	}
	line, err := rw.ReadSlice('\n')
	if err != nil {
		return err
	}
	switch {
	case bytes.Equal(line, resultStored):
		return nil
	case bytes.Equal(line, resultNotStored):
		return ErrNotStored
	case bytes.Equal(line, resultExists):
		return ErrCASConflict
	case bytes.Equal(line, resultNotFound):
		return ErrCacheMiss
	}
	return fmt.Errorf("memcache: unexpected response line from %q: %q", verb, string(line))
}

func writeReadLine(rw *bufio.ReadWriter, format string, args ...interface{}) ([]byte, os.Error) {
	_, err := fmt.Fprintf(rw, format, args...)
	if err != nil {
		return nil, err
	}
	if err := rw.Flush(); err != nil {
		return nil, err
	}
	line, err := rw.ReadSlice('\n')
	return line, err
}

func writeExpectf(rw *bufio.ReadWriter, expect []byte, format string, args ...interface{}) os.Error {
	line, err := writeReadLine(rw, format, args...)
	if err != nil {
		return err
	}
	switch {
	case bytes.Equal(line, expect):
		return nil
	case bytes.Equal(line, resultNotStored):
		return ErrNotStored
	case bytes.Equal(line, resultExists):
		return ErrCASConflict
	case bytes.Equal(line, resultNotFound):
		return ErrCacheMiss
	}
	return fmt.Errorf("memcache: unexpected response line: %q", string(line))
}

// Delete deletes the item with the provided key. The error ErrCacheMiss is
// returned if the item didn't already exist in the cache.
func (c *Client) Delete(key string) os.Error {
	return c.withKeyRw(key, func(rw *bufio.ReadWriter) os.Error {
		return writeExpectf(rw, resultDeleted, "delete %s\r\n", key)
	})
}

// Increment atomically increments key by delta. The return value is
// the new value after being incremented or an error. If the value
// didn't exist in memcached the error is ErrCacheMiss. The value in
// memcached must be an decimal number, or an error will be returned.
// On 64-bit overflow, the new value wraps around.
func (c *Client) Increment(key string, delta uint64) (newValue uint64, err os.Error) {
	return c.incrDecr("incr", key, delta)
}

// Decrement atomically decrements key by delta. The return value is
// the new value after being decremented or an error. If the value
// didn't exist in memcached the error is ErrCacheMiss. The value in
// memcached must be an decimal number, or an error will be returned.
// On underflow, the new value is capped at zero and does not wrap
// around.
func (c *Client) Decrement(key string, delta uint64) (newValue uint64, err os.Error) {
	return c.incrDecr("decr", key, delta)
}

func (c *Client) incrDecr(verb, key string, delta uint64) (uint64, os.Error) {
	var val uint64
	err := c.withKeyRw(key, func(rw *bufio.ReadWriter) os.Error {
		line, err := writeReadLine(rw, "%s %s %d\r\n", verb, key, delta)
		if err != nil {
			return err
		}
		switch {
		case bytes.Equal(line, resultNotFound):
			return ErrCacheMiss
		case bytes.HasPrefix(line, resultClientErrorPrefix):
			errMsg := line[len(resultClientErrorPrefix) : len(line)-2]
			return os.NewError("memcache: client error: " + string(errMsg))
		}
		val, err = strconv.Atoui64(string(line[:len(line)-2]))
		if err != nil {
			return err
		}
		return nil
	})
	return val, err
}
