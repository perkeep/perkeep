/*
Copyright 2013 The Perkeep Authors

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

// Package sorted provides a KeyValue interface and constructor registry.
package sorted // import "perkeep.org/pkg/sorted"

import (
	"errors"
	"fmt"
	"iter"
	"log"

	"go4.org/jsonconfig"
	"tailscale.com/types/result"
)

const (
	MaxKeySize   = 767   // Maximum size, in bytes, for a key in any store implementing KeyValue.
	MaxValueSize = 63000 // Maximum size, in bytes, for a value in any store implementing KeyValue. MaxKeySize and MaxValueSize values originate from InnoDB and MySQL limitations.
)

const DefaultKVFileType = "leveldb"

var (
	ErrNotFound      = errors.New("sorted: key not found")
	ErrKeyTooLarge   = fmt.Errorf("sorted: key size is over %v", MaxKeySize)
	ErrValueTooLarge = fmt.Errorf("sorted: value size is over %v", MaxValueSize)
)

// KeyValue is a sorted, enumerable key-value interface supporting
// batch mutations.
type KeyValue interface {
	// Get gets the value for the given key. It returns ErrNotFound if the DB
	// does not contain the key.
	Get(key string) (string, error)

	Set(key, value string) error

	// Delete deletes keys. Deleting a non-existent key does not return an error.
	Delete(key string) error

	BeginBatch() BatchMutation
	CommitBatch(b BatchMutation) error

	// Find returns an iterator positioned before the first key/value pair
	// whose key is 'greater than or equal to' the given key. There may be no
	// such pair, in which case the iterator will return false on Next.
	//
	// The optional end value specifies the exclusive upper
	// bound. If the empty string, the iterator returns keys
	// where "key >= start".
	// If non-empty, the iterator returns keys where
	// "key >= start && key < endHint".
	//
	// Any error encountered will be implicitly returned via the iterator. An
	// error-iterator will yield no key/value pairs and closing that iterator
	// will return that error.
	//
	// An iterator may or may not observe writes made after Find returns;
	// implementations differ. Callers needing a consistent view across
	// several reads should use TransactionalReader, if implemented.
	Find(start, end string) Iterator

	// Close is a polite way for the server to shut down the storage.
	// Implementations should never lose data after a Set, Delete,
	// or CommmitBatch, though.
	Close() error
}

// TransactionalReader is an optional interface that may be implemented by storage
// implementations. It may be implemented when a storage backend supports multiple
// atomic reads.
type TransactionalReader interface {
	KeyValue

	// BeginReadTx begins a read-only transaction.
	BeginReadTx() ReadTransaction
}

// Wiper is an optional interface that may be implemented by storage
// implementations.
type Wiper interface {
	KeyValue

	// Wipe removes all key/value pairs, and resets the storage to a blank state.
	// For a given storage, when NewKeyValue returns a NeedWipeError, Wipe
	// should be implemented so that it fixes the returned KeyValue.
	Wipe() error
}

// Reindexer optionally supports batching writes to a disposable index.
// Only offline reindexing may use this interface; ordinary KeyValue mutations
// retain their normal durability guarantees.
type Reindexer interface {
	KeyValue

	// BeginReindex atomically discards the old rows and durably marks the
	// index incomplete. No other access to this storage is allowed until
	// the returned session is finished. An interrupted build must not be
	// accepted on an ordinary reopen; it requires another reindex or Wipe.
	BeginReindex() (ReindexSession, error)
}

// ReindexSession is a private, concurrent-safe view of a rebuilding index.
// Unlike an ordinary KeyValue, individual successful mutations may not yet be
// durable. Reads see those mutations, including complete logical batches.
// The view must not be published to readers or used for live upload acks.
type ReindexSession interface {
	KeyValue

	// Finish(true) durably commits all remaining writes and marks the index
	// complete, returning any previously encountered storage error as well.
	// Finish(false), or Close, aborts the pending group and leaves the index
	// marked incomplete; earlier committed groups may remain. In either
	// case Finish releases the session's resources, not the underlying KV.
	// A failed build's Index and caches must be discarded, not reused.
	Finish(success bool) error
}

// NeedWipeError is returned by NewKeyValue when the returned KeyValue is not
// usable until Wipe has been called on it.
type NeedWipeError struct {
	Msg string
}

func (e NeedWipeError) Error() string {
	return e.Msg
}

// Iterator iterates over an index KeyValue's key/value pairs in key order.
//
// An iterator must be closed after use, but it is not necessary to read an
// iterator until exhaustion.
//
// An iterator is not necessarily goroutine-safe, but it is safe to use
// multiple iterators concurrently, with each in a dedicated goroutine.
type Iterator interface {
	// Next moves the iterator to the next key/value pair.
	// It returns false when the iterator is exhausted.
	Next() bool

	// Key returns the key of the current key/value pair.
	// Only valid after a call to Next returns true.
	Key() string

	// KeyBytes returns the key as bytes. The returned bytes
	// should not be written and are invalid after the next call
	// to Next or Close.
	// TODO(bradfitz): rename this and change it to return a
	// mem.RO instead?
	KeyBytes() []byte

	// Value returns the value of the current key/value pair.
	// Only valid after a call to Next returns true.
	Value() string

	// ValueBytes returns the value as bytes. The returned bytes
	// should not be written and are invalid after the next call
	// to Next or Close.
	// TODO(bradfitz): rename this and change it to return a
	// mem.RO instead?
	ValueBytes() []byte

	// Close closes the iterator and returns any accumulated error. Exhausting
	// all the key/value pairs in a table is not considered to be an error.
	// It is valid to call Close multiple times. Other methods should not be
	// called after the iterator has been closed.
	Close() error
}

// ReadTransaction is a read-only transaction on a KeyValue. It admits the same read
// operations as the KeyValue itself, but writes that occur after the transaction is
// created are not observed.
//
// Users should close the transaction as soon as it as no longer needed, as failing
// to do so can tie up resources.
type ReadTransaction interface {
	Get(key string) (string, error)
	Find(start, end string) Iterator

	// End the transaction.
	Close() error
}

type BatchMutation interface {
	Set(key, value string)
	Delete(key string)
}

type Mutation interface {
	Key() string
	Value() string
	IsDelete() bool
}

type mutation struct {
	key    string
	value  string // used if !delete
	delete bool   // if to be deleted
}

func (m mutation) Key() string {
	return m.key
}

func (m mutation) Value() string {
	return m.value
}

func (m mutation) IsDelete() bool {
	return m.delete
}

func NewBatchMutation() BatchMutation {
	return &batch{}
}

type batch struct {
	m []Mutation
}

func (b *batch) Mutations() []Mutation {
	return b.m
}

func (b *batch) Delete(key string) {
	b.m = append(b.m, mutation{key: key, delete: true})
}

func (b *batch) Set(key, value string) {
	b.m = append(b.m, mutation{key: key, value: value})
}

var (
	ctors = make(map[string]func(jsonconfig.Obj) (KeyValue, error))
)

func RegisterKeyValue(typ string, fn func(jsonconfig.Obj) (KeyValue, error)) {
	if typ == "" || fn == nil {
		panic("zero type or func")
	}
	if _, dup := ctors[typ]; dup {
		panic("duplication registration of type " + typ)
	}
	ctors[typ] = fn
}

// NewKeyValue returns a KeyValue as defined by cfg.
// It returns an error of type NeedWipeError when the returned KeyValue should
// be fixed with Wipe.
func NewKeyValue(cfg jsonconfig.Obj) (KeyValue, error) {
	var s KeyValue
	var err error
	typ := cfg.RequiredString("type")
	ctor, ok := ctors[typ]
	if typ != "" && !ok {
		return nil, fmt.Errorf("Invalid sorted.KeyValue type %q", typ)
	}
	if ok {
		s, err = ctor(cfg)
		if err != nil {
			var we NeedWipeError
			if !errors.As(err, &we) {
				return nil, fmt.Errorf("error from %q KeyValue: %w", typ, err)
			}
			if err := cfg.Validate(); err != nil {
				return nil, err
			}
			we.Msg = fmt.Sprintf("error from %q KeyValue: %v", typ, err)
			return s, we
		}
	}
	return s, cfg.Validate()
}

// NewKeyValueMaybeWipe calls NewKeyValue and wipes the KeyValue if, and only
// if, NewKeyValue has returned a NeedWipeError.
func NewKeyValueMaybeWipe(cfg jsonconfig.Obj) (KeyValue, error) {
	kv, err := NewKeyValue(cfg)
	if err == nil {
		return kv, nil
	}
	var we NeedWipeError
	if !errors.As(err, &we) {
		return nil, err
	}
	wiper, ok := kv.(Wiper)
	if !ok {
		return nil, fmt.Errorf("storage type %T needs wiping because %w. But it doesn't support sorted.Wiper", kv, err)
	}
	if err := wiper.Wipe(); err != nil {
		return nil, fmt.Errorf("sorted key/value type %T needed wiping because %w. But could not wipe: %w", kv, we, err)
	}
	return kv, nil
}

// Foreach runs fn for each key/value pair in kv. If fn returns an error,
// that same error is returned from Foreach and iteration stops.
func Foreach(kv KeyValue, fn func(key, value string) error) error {
	return ForeachInRange(kv, "", "", fn)
}

// ForeachInRange runs fn for each key/value pair in kv in the range
// of start and end, which behave the same as kv.Find. If fn returns
// an error, that same error is returned from Foreach and iteration
// stops.
// fn runs with the iterator open. Use ScanPaged if fn accesses kv
// or blocks on consumers that do.
func ForeachInRange(kv KeyValue, start, end string, fn func(key, value string) error) error {
	it := kv.Find(start, end)
	for it.Next() {
		if err := fn(it.Key(), it.Value()); err != nil {
			it.Close()
			return err
		}
	}
	return it.Close()
}

// Entry is a key/value pair in a KeyValue.
type Entry struct {
	Key, Value string
}

// ScanPaged returns a sequence of key/value pairs in kv in key order in the
// range specified by start and end, which behave the same as kv.Find.
// The sequence opens iterators lazily and can be iterated more than once;
// each iteration starts a new scan from start.
//
// It buffers at most 1000 entries at a time and closes each iterator before
// yielding, so the loop body can safely read from or write to kv, even when an open
// iterator would otherwise block those operations.
// Subsequent pages resume at the last key and skip keys less than or equal
// to it, without extending the key or constructing a backend-specific bound.
// A page iterator may therefore read 1001 pairs, including the skipped key.
//
// Unlike ForeachInRange, it uses a new iterator for each page and does not
// provide a snapshot across pages. Changes to later pages may be observed,
// while changes to already buffered pairs are not. Keys inserted at or before
// the last key of an already buffered page will not be visited.
//
// Results contain successful entries, followed by at most one terminal error.
// If an iterator reports an error when closed, the entries successfully read
// from that page are yielded before the error. If iteration stops early, no more
// results (including a pending error) are yielded and no more iterators are opened.
func ScanPaged(kv KeyValue, start, end string) iter.Seq[result.Of[Entry]] {
	return func(yield func(result.Of[Entry]) bool) {
		const pageSize = 1000
		var page []Entry
		cursor := start
		firstPage := true
		for {
			page = page[:0]
			it := kv.Find(cursor, end)
			for len(page) < pageSize && it.Next() {
				key := it.Key()
				if !firstPage && key <= cursor {
					continue
				}
				page = append(page, Entry{Key: key, Value: it.Value()})
			}
			err := it.Close()
			for _, entry := range page {
				if !yield(result.Value(entry)) {
					return
				}
			}
			if err != nil {
				yield(result.Error[Entry](err))
				return
			}
			if len(page) < pageSize {
				return
			}
			// Reuse the key itself: appending a NUL is not supported by all
			// backends and could also exceed the maximum key size.
			cursor = page[len(page)-1].Key
			firstPage = false
		}
	}
}

// CheckSizes returns ErrKeyTooLarge if key does not respect KeyMaxSize or
// ErrValueTooLarge if value does not respect ValueMaxSize
func CheckSizes(key, value string) error {
	if len(key) > MaxKeySize {
		log.Printf("sorted: key of %d bytes too large: %q...", len(key), key[:100])
		return ErrKeyTooLarge
	}
	if len(value) > MaxValueSize {
		log.Printf("sorted: key %q value too large (%d bytes)", key, len(value))
		return ErrValueTooLarge
	}
	return nil
}
