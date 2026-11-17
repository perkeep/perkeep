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

package search

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"perkeep.org/pkg/schema"
)

const (
	// Time allowed to write a message to the peer.
	writeWait = 10 * time.Second

	// Time allowed to read the next pong message from the peer.
	pongWait = 60 * time.Second

	// Send pings to peer with this period. Must be less than pongWait.
	pingPeriod = (pongWait * 9) / 10

	// Maximum message size allowed from peer.
	maxMessageSize = 10 << 10

	// Maximum search subscriptions per connection. Subscribing to one
	// more closes the connection. This limits how many searches and
	// unsent messages a connection can have. The web UI uses one.
	maxQueries = 16

	// statusTag is the tag of status messages. The web UI depends on it.
	statusTag = "_status"
)

var debug, _ = strconv.ParseBool(os.Getenv("CAMLI_DEBUG"))

type wsHub struct {
	sh             *Handler
	register       chan *wsConn
	unregister     chan *wsConn
	watchReq       chan watchReq
	newBlobRecv    chan schema.CamliType // new blob received.
	updatedResults chan *watchedQuery
	statusUpdate   chan json.RawMessage

	// Tests may change these before the hub has clients.
	writeTimeout   time.Duration
	searchInterval time.Duration // how long a subscription waits after one search before the next

	// Owned by func run:
	conns map[*wsConn]bool
}

func newWebsocketHub(sh *Handler) *wsHub {
	return &wsHub{
		sh:             sh,
		register:       make(chan *wsConn), // unbuffered; issue 563
		unregister:     make(chan *wsConn), // unbuffered; issue 563
		conns:          make(map[*wsConn]bool),
		watchReq:       make(chan watchReq, buffered),
		newBlobRecv:    make(chan schema.CamliType, buffered),
		updatedResults: make(chan *watchedQuery, buffered),
		statusUpdate:   make(chan json.RawMessage, buffered),
		writeTimeout:   writeWait,
		searchInterval: time.Second,
	}
}

func (h *wsHub) run() {
	var lastStatusMsg []byte
	for {
		select {
		case st := <-h.statusUpdate:
			const prefix = `{"tag":"` + statusTag + `","status":`
			lastStatusMsg = make([]byte, 0, len(prefix)+len(st)+1)
			lastStatusMsg = append(lastStatusMsg, prefix...)
			lastStatusMsg = append(lastStatusMsg, st...)
			lastStatusMsg = append(lastStatusMsg, '}')
			for c := range h.conns {
				c.setOutboxMessage(statusTag, lastStatusMsg)
			}
		case c := <-h.register:
			h.conns[c] = true
			// Send the last status, if any. A client that connects
			// before the first one gets it when it arrives.
			if lastStatusMsg != nil {
				c.setOutboxMessage(statusTag, lastStatusMsg)
			}
		case c := <-h.unregister:
			delete(h.conns, c)
			c.close()
		case camliType := <-h.newBlobRecv:
			if camliType == "" {
				// TODO: something smarter. some
				// queries might care about all blobs.
				// But for now only re-kick off
				// queries if schema blobs arrive.  We
				// should track per-WatchdQuery which
				// blob types the search cares about.
				continue
			}
			// New blob was received. Kick off standing search queries to see if any changed.
			for conn := range h.conns {
				for _, wq := range conn.queries {
					h.refresh(wq)
				}
			}
		case wr := <-h.watchReq:
			if !h.conns[wr.conn] {
				// Client has since disconnected or been closed.
				continue
			}
			wr.conn.clearOutboxMessage(wr.tag)
			// Unsubscribe
			if wr.q == nil {
				delete(wr.conn.queries, wr.tag)
				log.Printf("Removed subscription for %s, %q", wr.conn.remoteAddr, wr.tag)
				continue
			}
			if _, ok := wr.conn.queries[wr.tag]; !ok && len(wr.conn.queries) >= maxQueries {
				log.Printf("websocket: closing %s: more than %d subscriptions", wr.conn.remoteAddr, maxQueries)
				delete(h.conns, wr.conn)
				wr.conn.close()
				continue
			}
			// Very similar type, but semantically
			// different, so separate for now:
			wq := &watchedQuery{
				conn: wr.conn,
				tag:  wr.tag,
				q:    wr.q,
			}
			wr.conn.queries[wr.tag] = wq
			if debug {
				log.Printf("websocket: added/updated search subscription for tag %q", wr.tag)
			}
			h.refresh(wq)

		case wq := <-h.updatedResults:
			if !h.conns[wq.conn] || wq.conn.queries[wq.tag] != wq {
				// Client has since disconnected, unsubscribed, or
				// replaced the search of this tag with another.
				continue
			}
			wq.mu.Lock()
			lastres := wq.lastres
			wq.mu.Unlock()
			resb, err := json.Marshal(wsUpdateMessage{
				Tag:    wq.tag,
				Result: lastres,
			})
			if err != nil {
				panic(err)
			}
			wq.conn.setOutboxMessage(wq.tag, resb)
		}
	}
}

// refresh marks wq as needing a new search, and starts the goroutine
// that runs it if one isn't running already. It doesn't block: nothing
// holds wq.mu for long.
func (h *wsHub) refresh(wq *watchedQuery) {
	wq.mu.Lock()
	defer wq.mu.Unlock()
	wq.dirty = true
	if wq.refreshing {
		// Somebody else is already refreshing.
		// One's enough.
		return
	}
	wq.refreshing = true
	go h.redoSearch(wq)
}

// redoSearch runs wq's search, waits h.searchInterval, and repeats for
// as long as wq is dirty. Changes that arrive during the wait are all
// covered by the next search. Only one redoSearch runs per query at a
// time; see refresh. If the client unsubscribes or disconnects in the
// meantime, one more search may run, and the hub discards its result.
func (h *wsHub) redoSearch(wq *watchedQuery) {
	wq.mu.Lock()
	defer wq.mu.Unlock()
	for wq.dirty {
		wq.dirty = false
		wq.mu.Unlock() // release lock while running query; might become dirty meanwhile
		h.doSearch(wq)
		time.Sleep(h.searchInterval)
		wq.mu.Lock() // before checking wq.dirty
	}
	wq.refreshing = false
}

func (h *wsHub) doSearch(wq *watchedQuery) {
	// Make our own copy, in case
	q := new(SearchQuery)
	*q = *wq.q // shallow copy, since Query will mutate its internal state fields
	if q.Describe != nil {
		q.Describe = wq.q.Describe.Clone()
	}

	res, err := h.sh.Query(context.TODO(), q)
	if err != nil {
		log.Printf("Query error: %v", err)
		return
	}
	resj, _ := json.Marshal(res)

	wq.mu.Lock()
	eq := bytes.Equal(wq.lastresj, resj)
	wq.lastres = res
	wq.lastresj = resj
	wq.mu.Unlock()
	if eq {
		// No change in search. Ignore.
		return
	}
	h.updatedResults <- wq
}

// wsConn is a websocket client of the hub.
type wsConn struct {
	ws         *websocket.Conn
	sh         *Handler
	remoteAddr string // for logs

	// queries is owned by the wsHub.run goroutine.
	queries map[string]*watchedQuery // tag -> subscription

	// wake holds a value when writePump has work to do: a message to
	// send, or the connection to close.
	wake chan struct{}

	mu sync.Mutex // guards following

	// outbox holds the messages not yet sent to the client, at most
	// one per tag: a newer message for a tag replaces the older one.
	// It's a slice, kept in the order the tags were added, so that the
	// status message sent on connect goes out before the first search
	// result. The web UI relies on that: it never treats the first
	// message it receives as a search result. The tags are the status
	// tag and the client's subscriptions, so there are at most
	// maxQueries+1 entries.
	outbox []outboxMessage
	closed bool
}

type outboxMessage struct {
	tag string
	msg []byte // JSON
}

func newWSConn(sh *Handler, ws *websocket.Conn) *wsConn {
	return &wsConn{
		ws:      ws,
		sh:      sh,
		queries: make(map[string]*watchedQuery),
		wake:    make(chan struct{}, 1),
	}
}

// setOutboxMessage queues msg to be sent to the client. It replaces
// any message for the same tag that hasn't been sent yet: each message
// is a complete result, so only the newest matters. It never blocks.
func (c *wsConn) setOutboxMessage(tag string, msg []byte) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	if i := slices.IndexFunc(c.outbox, func(m outboxMessage) bool { return m.tag == tag }); i >= 0 {
		c.outbox[i].msg = msg
	} else {
		c.outbox = append(c.outbox, outboxMessage{tag, msg})
	}
	c.mu.Unlock()
	c.wakeWritePump()
}

func (c *wsConn) clearOutboxMessage(tag string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.outbox = slices.DeleteFunc(c.outbox, func(m outboxMessage) bool { return m.tag == tag })
}

// takeOutboxMessage removes and returns the oldest unsent message. ok
// is false if there is none, and closed reports whether c is closed.
// If more messages remain, it wakes writePump again. That way
// writePump sends one message at a time, and can send pings in between.
func (c *wsConn) takeOutboxMessage() (m outboxMessage, ok, closed bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.outbox) == 0 {
		return outboxMessage{}, false, c.closed
	}
	m = c.outbox[0]
	c.outbox = slices.Delete(c.outbox, 0, 1)
	if len(c.outbox) > 0 {
		c.wakeWritePump()
	}
	return m, true, false
}

// close tells writePump to close the connection. Messages that haven't
// been sent are dropped.
func (c *wsConn) close() {
	c.mu.Lock()
	c.closed = true
	c.outbox = nil
	c.mu.Unlock()
	c.wakeWritePump()
}

func (c *wsConn) wakeWritePump() {
	select {
	case c.wake <- struct{}{}:
	default: // a wake-up is already pending
	}
}

type watchedQuery struct {
	conn *wsConn
	tag  string
	q    *SearchQuery

	mu         sync.Mutex // guards following
	refreshing bool       // redoSearch is running
	dirty      bool       // the index has changed since the last search started; another is due
	lastres    *SearchResult
	lastresj   []byte // as JSON
}

// watchReq is a (un)subscribe request.
type watchReq struct {
	conn *wsConn
	tag  string       // required
	q    *SearchQuery // if nil, subscribe
}

// Client->Server subscription message.
type wsClientMessage struct {
	// Tag is required.
	Tag string `json:"tag"`
	// Query is required to subscribe. If absent, it means unsubscribe.
	Query *SearchQuery `json:"query,omitempty"`
}

type wsUpdateMessage struct {
	Tag    string        `json:"tag"`
	Result *SearchResult `json:"result,omitempty"`
}

// readPump pumps messages from the websocket connection to the hub.
func (c *wsConn) readPump() {
	defer func() {
		c.sh.wsHub.unregister <- c
		c.ws.Close()
	}()
	c.ws.SetReadLimit(maxMessageSize)
	c.ws.SetReadDeadline(time.Now().Add(pongWait))
	c.ws.SetPongHandler(func(string) error { c.ws.SetReadDeadline(time.Now().Add(pongWait)); return nil })
	for {
		_, message, err := c.ws.ReadMessage()
		if err != nil {
			break
		}
		if debug {
			log.Printf("websocket: got message %#q", message)
		}
		cm := new(wsClientMessage)
		if err := json.Unmarshal(message, cm); err != nil {
			log.Printf("Ignoring bogus websocket message. Err: %v", err)
			continue
		}
		if cm.Tag == "" || cm.Tag == statusTag {
			log.Printf("Ignoring websocket message with missing or reserved tag %q", cm.Tag)
			continue
		}
		c.sh.wsHub.watchReq <- watchReq{
			conn: c,
			tag:  cm.Tag,
			q:    cm.Query,
		}
	}
}

// write writes a message with the given message type and payload.
func (c *wsConn) write(mt int, payload []byte) error {
	c.ws.SetWriteDeadline(time.Now().Add(c.sh.wsHub.writeTimeout))
	return c.ws.WriteMessage(mt, payload)
}

// writePump pumps messages from the hub to the websocket connection.
func (c *wsConn) writePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		c.ws.Close()
	}()
	for {
		select {
		case <-c.wake:
			m, ok, closed := c.takeOutboxMessage()
			if closed {
				c.write(websocket.CloseMessage, []byte{})
				return
			}
			if !ok {
				continue
			}
			if err := c.write(websocket.TextMessage, m.msg); err != nil {
				return
			}
		case <-ticker.C:
			if err := c.write(websocket.PingMessage, []byte{}); err != nil {
				return
			}
		}
	}
}

// upgrader is used in serveWebSocket to construct websocket connections.
var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	// uses a default origin check policy
}

func (h *Handler) serveWebSocket(rw http.ResponseWriter, req *http.Request) {
	ws, err := upgrader.Upgrade(rw, req, nil)
	var he websocket.HandshakeError
	if errors.As(err, &he) {
		http.Error(rw, "Not a websocket handshake", http.StatusBadRequest)
		return
	} else if err != nil {
		log.Println(err)
		return
	}
	c := newWSConn(h, ws)
	c.remoteAddr = ws.RemoteAddr().String()
	h.wsHub.register <- c
	go c.writePump()
	c.readPump()
}
