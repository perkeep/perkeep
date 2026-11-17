/*
Copyright 2026 The Perkeep Authors

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
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver"
	"perkeep.org/pkg/index"
	"perkeep.org/pkg/index/indextest"
	"perkeep.org/pkg/schema"
	"perkeep.org/pkg/test"
)

// wsTest is a search handler on an in-memory index with a corpus, for
// tests of the handler's websocket hub.
type wsTest struct {
	t   *testing.T
	id  *indextest.IndexDeps
	h   *Handler
	seq int
}

func newWSTest(t *testing.T) *wsTest {
	idx := index.NewMemoryIndex()
	idx.Logger = test.NewLogger(t, "")
	id := indextest.NewIndexDeps(idx)
	id.Fataler = t
	corpus, err := idx.KeepInMemory()
	if err != nil {
		t.Fatal(err)
	}
	h := NewHandler(idx, index.NewOwner(indextest.KeyID, indextest.PubKey.BlobRef()))
	h.SetCorpus(corpus)
	return &wsTest{t: t, id: id, h: h}
}

// subscribedConn returns a connection registered with the hub. It has
// no socket and no writePump: the test takes its unsent messages
// itself. It is subscribed to all permanodes under the tag "q", and
// the first result has already been taken.
func (wt *wsTest) subscribedConn() *wsConn {
	wt.t.Helper()
	c := newWSConn(wt.h, nil)
	wt.h.wsHub.register <- c
	wt.h.wsHub.watchReq <- watchReq{conn: c, tag: "q", q: allPermanodes(50)}
	wt.awaitResult(c, "")
	return c
}

// allPermanodes returns a search for all permanodes, newest first, up
// to limit.
func allPermanodes(limit int) *SearchQuery {
	return &SearchQuery{
		Constraint: &Constraint{Permanode: &PermanodeConstraint{}},
		Sort:       CreatedDesc,
		Limit:      limit,
	}
}

// within runs f, and fails the test if f takes longer than 10 seconds,
// in case it is stuck for good.
func within(t *testing.T, what string, f func() error) {
	t.Helper()
	errc := make(chan error, 1)
	go func() { errc <- f() }()
	select {
	case err := <-errc:
		if err != nil {
			t.Fatalf("%s: %v", what, err)
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("%s: still waiting after 10s", what)
	}
}

// awaitResult is awaitResultOfTag for the tag "q"; see subscribedConn.
func (wt *wsTest) awaitResult(c *wsConn, want string) {
	wt.t.Helper()
	wt.awaitResultOfTag(c, "q", want)
}

// awaitResultOfTag takes c's unsent messages, as writePump would, until
// it finds a result for tag that contains want. It returns the messages
// it took, and fails the test if none matches within 10 seconds.
func (wt *wsTest) awaitResultOfTag(c *wsConn, tag, want string) (took []outboxMessage) {
	wt.t.Helper()
	timeout := time.After(10 * time.Second)
	for {
		select {
		case <-c.wake:
			out, closed := takeAll(c)
			if closed {
				wt.t.Fatalf("the connection was closed; want a search result of %q with %q", tag, want)
			}
			took = append(took, out...)
			for _, m := range out {
				if m.tag == tag && bytes.Contains(m.msg, []byte(want)) {
					return took
				}
			}
		case <-timeout:
			wt.t.Fatalf("no search result of %q with %q after 10s", tag, want)
		}
	}
}

// takeAll takes all of c's unsent messages, as its writePump does, and
// reports whether c is closed.
func takeAll(c *wsConn) (out []outboxMessage, closed bool) {
	for {
		m, ok, closed := c.takeOutboxMessage()
		if closed || !ok {
			return out, closed
		}
		out = append(out, m)
	}
}

// outboxOf returns a copy of c's unsent messages.
func outboxOf(c *wsConn) []outboxMessage {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.outbox)
}

// waitForHub waits until the hub has handled what the test has sent it
// on watchReq, updatedResults and statusUpdate.
func (wt *wsTest) waitForHub() {
	h := wt.h.wsHub
	for len(h.watchReq) > 0 || len(h.updatedResults) > 0 || len(h.statusUpdate) > 0 {
		runtime.Gosched()
	}
	// unregister is unbuffered, so this send returns only once the hub
	// has finished handling those.
	h.unregister <- newWSConn(wt.h, nil)
}

// indexNewPermanode has the index receive a new permanode with a
// title, and returns it. It fails the test if that takes over 10
// seconds.
func (wt *wsTest) indexNewPermanode() blob.Ref {
	wt.t.Helper()
	wt.seq++
	pn := wt.id.Sign(schema.NewUnsignedPermanode())
	claim := schema.NewSetAttributeClaim(pn.BlobRef(), "title", fmt.Sprint(wt.seq))
	claim.SetClaimDate(time.Unix(int64(1e9+wt.seq), 0))
	ctx := context.Background()
	for _, tb := range []*test.Blob{pn, wt.id.Sign(claim)} {
		if _, err := wt.id.BlobSource.ReceiveBlob(ctx, tb.BlobRef(), tb.Reader()); err != nil {
			wt.t.Fatal(err)
		}
		within(wt.t, fmt.Sprintf("indexing a blob of permanode %d", wt.seq), func() error {
			_, err := blobserver.Receive(ctx, wt.id.Index, tb.BlobRef(), tb.Reader())
			return err
		})
	}
	return pn.BlobRef()
}

// putResult makes the hub queue a fake result for wq, as if its search
// had found one blob. The blob's ref is made from marker, so that
// hasMarker can recognize the result.
func (wt *wsTest) putResult(wq *watchedQuery, marker string) {
	wq.mu.Lock()
	wq.lastres = &SearchResult{Blobs: []*SearchResultBlob{{Blob: blob.RefFromString(marker)}}}
	wq.mu.Unlock()
	wt.h.wsHub.updatedResults <- wq
}

// hasMarker reports whether msgs includes a result that putResult made
// with marker.
func hasMarker(msgs []outboxMessage, marker string) bool {
	ref := []byte(blob.RefFromString(marker).String())
	return slices.ContainsFunc(msgs, func(m outboxMessage) bool { return bytes.Contains(m.msg, ref) })
}

// TestStalledWebsocketDoesntLockIndex checks that a websocket client
// that stops reading doesn't hold up anything else: the index still
// receives blobs, searches still run, and other clients still get
// their updates. The hub used to wait for a client whose queue was
// full, so one stalled client could block the index for good.
func TestStalledWebsocketDoesntLockIndex(t *testing.T) {
	wt := newWSTest(t)
	wt.h.wsHub.searchInterval = 0  // so the last blob's result comes at once
	stalled := wt.subscribedConn() // never takes from it again
	reading := wt.subscribedConn()

	// Give the stalled client more results than the old per-client
	// queue could hold (256). That used to block the hub on it, and
	// with it the index and the other clients.
	within(t, "the hub taking results for the stalled client", func() error {
		for i := range 600 {
			wt.putResult(stalled.queries["q"], fmt.Sprint(i))
		}
		wt.waitForHub()
		return nil
	})

	last := wt.indexNewPermanode()
	within(t, "a search", func() error {
		_, err := wt.h.Query(context.Background(), allPermanodes(1))
		return err
	})
	wt.awaitResult(reading, last.String())
}

// TestWebsocketSlowClientGetsLatest checks that, for a client that
// isn't reading, the hub keeps only the newest result of a search,
// instead of queueing results or waiting. Other clients keep getting
// updates, and the slow client gets the newest result when it reads
// again.
func TestWebsocketSlowClientGetsLatest(t *testing.T) {
	wt := newWSTest(t)
	wt.h.wsHub.searchInterval = 10 * time.Millisecond
	reading := wt.subscribedConn()
	slow := wt.subscribedConn()

	var last blob.Ref
	for range 30 {
		last = wt.indexNewPermanode()
		if n := len(outboxOf(slow)); n > 2 {
			t.Fatalf("the hub has %d messages for the slow client; want at most one per tag, 2", n)
		}
	}
	wt.awaitResult(reading, last.String())

	// The slow client's search runs on its own, so its newest result
	// can come after the reading client's; awaitResult waits for it.
	wt.awaitResult(slow, last.String())
}

// TestWebsocketUnsubscribe checks that unsubscribing removes the
// client's search, and any result of it that hasn't been sent yet, and
// leaves the client's other searches alone.
func TestWebsocketUnsubscribe(t *testing.T) {
	wt := newWSTest(t)
	wt.h.wsHub.searchInterval = 0 // so r's second result comes at once
	c := wt.subscribedConn()
	wt.h.wsHub.watchReq <- watchReq{conn: c, tag: "r", q: allPermanodes(50)}
	wt.awaitResultOfTag(c, "r", "")
	wt.putResult(c.queries["q"], "unsent")
	wt.waitForHub()
	if !hasMarker(outboxOf(c), "unsent") {
		t.Fatal("the result to drop isn't unsent before unsubscribing")
	}

	wt.h.wsHub.watchReq <- watchReq{conn: c, tag: "q"} // unsubscribe
	wt.waitForHub()
	if _, ok := c.queries["q"]; ok {
		t.Fatal("the hub still has the search of tag q after unsubscribing")
	}
	if hasMarker(outboxOf(c), "unsent") {
		t.Error("the hub kept an unsent result of tag q after unsubscribing")
	}
	for _, m := range wt.awaitResultOfTag(c, "r", wt.indexNewPermanode().String()) {
		if m.tag == "q" {
			t.Errorf("got a result of tag q after unsubscribing: %s", m.msg)
		}
	}
}

// TestWebsocketReplacedQueryResultDropped checks that when a client
// replaces the search of a tag, results of the old search are not
// sent, whether they were queued before the replacement or arrive
// after it. An old result could otherwise overwrite a new one.
func TestWebsocketReplacedQueryResultDropped(t *testing.T) {
	wt := newWSTest(t)
	c := wt.subscribedConn()
	wt.putResult(c.queries["q"], "before")
	wt.waitForHub()
	if !hasMarker(outboxOf(c), "before") {
		t.Fatal("the result to drop isn't unsent before replacing the search")
	}

	wt.h.wsHub.watchReq <- watchReq{conn: c, tag: "q", q: allPermanodes(10)}
	wt.waitForHub()
	wt.putResult(&watchedQuery{conn: c, tag: "q"}, "after")
	wt.waitForHub()
	for _, when := range []string{"before", "after"} {
		if hasMarker(outboxOf(c), when) {
			t.Errorf("the hub has an unsent result of the replaced search, from %s", when)
		}
	}
}

// TestWebsocketOneRedoSearchPerQuery checks that refreshing a query
// whose redoSearch is running doesn't start another: two would race,
// and the older result could arrive last.
func TestWebsocketOneRedoSearchPerQuery(t *testing.T) {
	wt := newWSTest(t)
	wt.h.wsHub.searchInterval = time.Hour
	c := wt.subscribedConn() // its redoSearch has searched, and is pausing
	wq := c.queries["q"]
	before := redoSearches()
	for range 20 {
		wt.h.wsHub.refresh(wq)
	}
	if n := redoSearches() - before; n > 0 {
		t.Errorf("%d more redoSearch goroutines for a query that has one; want 0", n)
	}
}

// redoSearches returns how many goroutines are in wsHub.redoSearch.
func redoSearches() int {
	buf := make([]byte, 1<<20)
	for {
		n := runtime.Stack(buf, true)
		if n < len(buf) {
			return strings.Count(string(buf[:n]), "search.(*wsHub).redoSearch(")
		}
		buf = make([]byte, 2*len(buf))
	}
}

// TestWebsocketMaxQueries checks that the hub closes a client that
// subscribes to more than maxQueries searches, and that subscribing a
// tag again doesn't count.
func TestWebsocketMaxQueries(t *testing.T) {
	wt := newWSTest(t)
	c := newWSConn(wt.h, nil)
	wt.h.wsHub.register <- c
	for i := range maxQueries {
		wt.h.wsHub.watchReq <- watchReq{conn: c, tag: fmt.Sprint(i), q: allPermanodes(1)}
	}
	wt.h.wsHub.watchReq <- watchReq{conn: c, tag: "0", q: allPermanodes(2)}
	wt.waitForHub()
	if _, closed := takeAll(c); closed {
		t.Fatalf("closed at %d subscriptions; want open", maxQueries)
	}

	wt.h.wsHub.watchReq <- watchReq{conn: c, tag: "one more", q: allPermanodes(1)}
	wt.waitForHub()
	if _, closed := takeAll(c); !closed {
		t.Fatalf("open at %d subscriptions; want closed", maxQueries+1)
	}
}

// TestWebsocketSubscribeAfterClose checks that the hub ignores a
// subscription that a client's readPump sends after the hub has closed
// the client: its search would run with no one to send results to.
func TestWebsocketSubscribeAfterClose(t *testing.T) {
	wt := newWSTest(t)
	c := newWSConn(wt.h, nil)
	wt.h.wsHub.register <- c
	wt.h.wsHub.unregister <- c
	wt.h.wsHub.watchReq <- watchReq{conn: c, tag: "q", q: allPermanodes(1)}
	wt.waitForHub()
	if n := len(c.queries); n != 0 {
		t.Errorf("the hub added %d searches for a closed client; want 0", n)
	}
}

// TestWebsocketNoEmptyFirstMessage checks that the hub sends nothing
// to a client that connects before there is a status, rather than an
// empty message: the web UI's JSON.parse throws on one.
func TestWebsocketNoEmptyFirstMessage(t *testing.T) {
	wt := newWSTest(t)
	c := newWSConn(wt.h, nil)
	wt.h.wsHub.register <- c
	wt.waitForHub()
	if n := len(outboxOf(c)); n != 0 {
		t.Errorf("the hub has %d messages for a client with no status to send; want 0", n)
	}
}

// TestWebsocketStatus checks that a client that connects gets the last
// status before its first search result, which the web UI relies on,
// and that a later status doesn't change one already queued or taken.
func TestWebsocketStatus(t *testing.T) {
	wt := newWSTest(t)
	h := wt.h.wsHub
	h.statusUpdate <- json.RawMessage(`{"n":1}`)
	wt.waitForHub()

	c := newWSConn(wt.h, nil)
	h.register <- c
	h.watchReq <- watchReq{conn: c, tag: "q", q: allPermanodes(1)}
	took := wt.awaitResultOfTag(c, "q", "")
	if len(took) != 2 || took[0].tag != statusTag {
		t.Fatalf("took %q; want the status, then the search result", took)
	}

	h.statusUpdate <- json.RawMessage(`{"n":2}`)
	wt.waitForHub()
	if got, want := string(took[0].msg), `{"tag":"_status","status":{"n":1}}`; got != want {
		t.Errorf("the status taken first is now %s; want %s", got, want)
	}
	if out := outboxOf(c); len(out) != 1 || !strings.Contains(string(out[0].msg), `{"n":2}`) {
		t.Errorf("unsent after the second status: %q; want only that status", out)
	}
}

// closeNotifyListener is a net.Listener for tests. Its connections
// have small socket buffers, so that writes to a client that isn't
// reading soon block. It closes its closed channel when the server
// closes a connection.
type closeNotifyListener struct {
	net.Listener
	closed chan struct{}
	once   sync.Once
}

func (ln *closeNotifyListener) Accept() (net.Conn, error) {
	c, err := ln.Listener.Accept()
	if err != nil {
		return nil, err
	}
	if tc, ok := c.(*net.TCPConn); ok {
		tc.SetWriteBuffer(4 << 10)
	}
	return &closeNotifyConn{Conn: c, ln: ln}, nil
}

type closeNotifyConn struct {
	net.Conn
	ln *closeNotifyListener
}

func (c *closeNotifyConn) Close() error {
	c.ln.once.Do(func() { close(c.ln.closed) })
	return c.Conn.Close()
}

// TestWebsocketOutbox checks setOutboxMessage, clearOutboxMessage,
// takeOutboxMessage and close on one connection's outbox.
func TestWebsocketOutbox(t *testing.T) {
	c := newWSConn(nil, nil)
	c.setOutboxMessage("a", []byte("a1"))
	c.setOutboxMessage("b", []byte("b1"))
	c.setOutboxMessage("a", []byte("a2"))
	c.setOutboxMessage("c", []byte("c1"))
	c.clearOutboxMessage("b")
	c.clearOutboxMessage("none")
	<-c.wake
	first, _, _ := c.takeOutboxMessage()
	if len(c.wake) != 1 {
		t.Fatal("no wake-up left after taking one of two messages")
	}
	got := []string{first.tag + ":" + string(first.msg)}
	out, closed := takeAll(c)
	for _, m := range out {
		got = append(got, m.tag+":"+string(m.msg))
	}
	if want := []string{"a:a2", "c:c1"}; closed || !slices.Equal(got, want) {
		t.Fatalf("took %q, closed %v; want %q, false", got, closed, want)
	}

	c.setOutboxMessage("a", []byte("a3"))
	<-c.wake
	c.close()
	if len(c.wake) != 1 {
		t.Error("no wake-up left for writePump after closing")
	}
	c.setOutboxMessage("b", []byte("b2"))
	if out, closed := takeAll(c); !closed || len(out) != 0 {
		t.Errorf("after closing: %d messages, closed %v; want 0, true", len(out), closed)
	}
}

// TestWebsocketStalledClientDisconnected checks that the server closes
// the connection of a websocket client that has stopped reading, once
// a write to it has waited longer than the hub's write timeout.
func TestWebsocketStalledClientDisconnected(t *testing.T) {
	wt := newWSTest(t)
	wt.h.wsHub.writeTimeout = 100 * time.Millisecond
	wt.h.wsHub.searchInterval = 0 // a result per blob, so writes block sooner

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	cln := &closeNotifyListener{Listener: ln, closed: make(chan struct{})}
	srv := httptest.NewUnstartedServer(http.HandlerFunc(wt.h.serveWebSocket))
	srv.Listener = cln
	srv.Start()
	defer srv.Close()

	dialer := websocket.Dialer{
		NetDialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			c, err := new(net.Dialer).DialContext(ctx, network, addr)
			if tc, ok := c.(*net.TCPConn); ok {
				tc.SetReadBuffer(4 << 10)
			}
			return c, err
		},
	}
	ws, _, err := dialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	const sub = `{"tag":"q","query":{"sort":"-created","constraint":{"permanode":{}},"limit":50,"describe":{"depth":1}}}`
	if err := ws.WriteMessage(websocket.TextMessage, []byte(sub)); err != nil {
		t.Fatal(err)
	}
	ws.SetReadDeadline(time.Now().Add(10 * time.Second))
	if _, _, err := ws.ReadMessage(); err != nil {
		t.Fatalf("reading the search's first result: %v", err)
	}

	// Stop reading. Each blob changes the result, so the server keeps
	// writing until its writes block. How many blobs that takes
	// depends on the socket buffer sizes the OS gives us.
	deadline := time.Now().Add(10 * time.Second)
	for {
		select {
		case <-cln.closed:
			return
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("the server hasn't closed the connection of a client that stopped reading")
		}
		wt.indexNewPermanode()
	}
}

// TestWebsocketSearchInterval checks that a subscription's searches
// are at least the hub's search interval apart, so that a burst of
// blobs causes only a few searches, and that the last result still
// includes the last blob.
func TestWebsocketSearchInterval(t *testing.T) {
	wt := newWSTest(t)
	const interval = 100 * time.Millisecond
	wt.h.wsHub.searchInterval = interval
	c := wt.subscribedConn()

	// Take results as they come, as writePump does, and count them
	// until one has the last blob.
	lastc := make(chan string, 1)
	counted := make(chan int, 1) // or -1 after 10s without one
	go func() {
		n, want := 0, ""
		var newest []byte
		for {
			select {
			case want = <-lastc:
			case <-c.wake:
				out, _ := takeAll(c)
				for _, m := range out {
					if m.tag == "q" {
						n++
						newest = m.msg
					}
				}
			case <-time.After(10 * time.Second):
				counted <- -1
				return
			}
			if want != "" && bytes.Contains(newest, []byte(want)) {
				counted <- n
				return
			}
		}
	}()

	start := time.Now()
	var last blob.Ref
	for range 30 {
		last = wt.indexNewPermanode()
	}
	lastc <- last.String()
	n := <-counted
	elapsed := time.Since(start)
	if n < 0 {
		t.Fatalf("no search result with the last permanode %v", last)
	}
	// One search can start at once, then one per interval.
	if max := 2 + int(elapsed/interval); n > max {
		t.Errorf("got %d results in %v; want at most %d with searches %v apart", n, elapsed, max, interval)
	}
}
