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

package client

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"testing/synctest"

	"perkeep.org/pkg/auth"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver"
	"perkeep.org/pkg/blobserver/handlers"
	"perkeep.org/pkg/blobserver/uploadsession"
	"perkeep.org/pkg/test"
)

// sessionStorage is the storage of a sessionServer: a test.Fetcher that
// fails to receive the blobs in fail.
type sessionStorage struct {
	*test.Fetcher
	mu   sync.Mutex
	fail map[blob.Ref]bool
}

var errSessionStorage = errors.New("storage failed")

func (s *sessionStorage) ReceiveBlobs(ctx context.Context, blobs []*blob.Blob) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	be := &blobserver.BlobsError{Op: "receive"}
	for _, b := range blobs {
		if s.fail[b.Ref()] {
			be.Add(b.Ref(), errSessionStorage)
			continue
		}
		r, err := b.ReadAll(ctx)
		if err == nil {
			_, err = s.Fetcher.ReceiveBlob(ctx, b.Ref(), r)
		}
		be.Add(b.Ref(), err)
	}
	return be.OrNil()
}

// sessionServer is an in-memory server with upload sessions.
type sessionServer struct {
	sto      *sessionStorage
	sessions *uploadsession.Manager

	mu         sync.Mutex
	acks       [][]string    // of each flush request
	failFlush  bool          // whether to fail flush requests with a 500
	holdUpload chan struct{} // if non-nil, upload responses wait for it to close
	uploading  chan struct{} // if non-nil, closed when an upload has been handled
	holdFlush  chan struct{} // if non-nil, flush responses wait for it to close
	flushing   chan struct{} // if non-nil, closed when a flush has been handled
	gate       chan struct{} // if non-nil, upload requests wait for it to close
	uploads    int           // upload requests
}

// newSessionClient returns a client of a new sessionServer, which has
// upload sessions if withSessions.
func newSessionClient(t *testing.T, withSessions bool) (*Client, *sessionServer) {
	t.Helper()
	srv := &sessionServer{sto: &sessionStorage{Fetcher: new(test.Fetcher)}}
	srv.sessions = uploadsession.NewManager(srv.sto, uploadsession.Limits{})
	upload := handlers.CreateBatchUploadHandler(srv.sto, nil, srv.sessions)
	session := handlers.CreateSessionHandler(srv.sessions)
	ts := httptest.NewTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Header.Get("Accept") == "text/x-camli-configuration":
			w.Header().Set("Content-Type", "text/javascript")
			if withSessions {
				w.Write([]byte(`{"blobRoot": "/bs/", "uploadSessions": true}`))
			} else {
				w.Write([]byte(`{"blobRoot": "/bs/"}`))
			}
		case r.URL.Path == "/bs/camli/upload":
			srv.mu.Lock()
			srv.uploads++
			gate := srv.gate
			srv.mu.Unlock()
			if gate != nil {
				<-gate
			}
			rec := httptest.NewRecorder()
			upload.ServeHTTP(rec, r)
			srv.mu.Lock()
			hold, uploading := srv.holdUpload, srv.uploading
			srv.mu.Unlock()
			if uploading != nil {
				close(uploading)
			}
			if hold != nil {
				<-hold
			}
			for k, v := range rec.Header() {
				w.Header()[k] = v
			}
			w.WriteHeader(rec.Code)
			w.Write(rec.Body.Bytes())
		case strings.HasPrefix(r.URL.Path, "/bs/camli/session"):
			if strings.HasSuffix(r.URL.Path, "/flush") {
				r.ParseForm()
				srv.mu.Lock()
				srv.acks = append(srv.acks, r.PostForm["ack"])
				fail := srv.failFlush
				hold, flushing := srv.holdFlush, srv.flushing
				srv.mu.Unlock()
				if fail {
					http.Error(w, "flush failed", http.StatusInternalServerError)
					return
				}
				rec := httptest.NewRecorder()
				session.ServeHTTP(rec, r)
				if flushing != nil {
					close(flushing)
				}
				if hold != nil {
					<-hold
				}
				w.WriteHeader(rec.Code)
				w.Write(rec.Body.Bytes())
				return
			}
			session.ServeHTTP(w, r)
		default:
			http.NotFound(w, r)
		}
	}))
	c, err := New(OptionServer("http://example.com/bs"), OptionNoExternalConfig(), OptionAuthMode(auth.None{}))
	if err != nil {
		t.Fatal(err)
	}
	c.SetHTTPClient(ts.Client())
	c.SetHaveCache(&mapHaveCache{m: make(map[blob.Ref]uint32)})
	return c, srv
}

func newSession(t *testing.T, c *Client) *UploadSession {
	t.Helper()
	s, err := c.NewUploadSession(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close(context.Background()) })
	return s
}

func sessionUpload(t *testing.T, s *UploadSession, tbs ...*test.Blob) {
	t.Helper()
	for _, tb := range tbs {
		if err := s.Upload(context.Background(), NewUploadHandleFromString(tb.Contents)); err != nil {
			t.Fatalf("Upload of %v: %v", tb.BlobRef(), err)
		}
	}
}

func sortedSizedRefs(tbs ...*test.Blob) []blob.SizedRef {
	var sbs []blob.SizedRef
	for _, tb := range tbs {
		sbs = append(sbs, tb.SizedRef())
	}
	sort.Sort(blob.SizedByRef(sbs))
	return sbs
}

func sortSizedRefs(sbs []blob.SizedRef) []blob.SizedRef {
	sort.Sort(blob.SizedByRef(sbs))
	return sbs
}

// TestUploadSession checks uploading in a session, flushing it, and that
// each flush acknowledges the blobs the previous one returned.
func TestUploadSession(t *testing.T) {
	c, srv := newSessionClient(t, true)
	s := newSession(t, c)
	a, b, d := &test.Blob{Contents: "a"}, &test.Blob{Contents: "b"}, &test.Blob{Contents: "d"}
	sessionUpload(t, s, a, b)

	durable, err := s.Flush(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if want := sortedSizedRefs(a, b); !slices.Equal(sortSizedRefs(durable), want) {
		t.Errorf("Flush = %v; want %v", durable, want)
	}
	for _, tb := range []*test.Blob{a, b} {
		if _, ok := c.haveCache.StatBlobCache(tb.BlobRef()); !ok {
			t.Errorf("%v not in the have cache after Flush", tb.BlobRef())
		}
		if _, _, err := srv.sto.Fetch(context.Background(), tb.BlobRef()); err != nil {
			t.Errorf("%v not in storage after Flush: %v", tb.BlobRef(), err)
		}
	}

	sessionUpload(t, s, d)
	durable, err = s.Flush(context.Background())
	if want := sortedSizedRefs(d); err != nil || !slices.Equal(durable, want) {
		t.Errorf("second Flush = %v, %v; want %v", durable, err, want)
	}
	srv.mu.Lock()
	acks := srv.acks
	srv.mu.Unlock()
	wantAcks := []string{a.BlobRef().String(), b.BlobRef().String()}
	slices.Sort(wantAcks)
	if len(acks) != 2 || len(acks[0]) != 0 || !slices.Equal(slices.Sorted(slices.Values(acks[1])), wantAcks) {
		t.Errorf("flush acks = %q; want none, then %q", acks, wantAcks)
	}

	if err := s.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.sessions.Session(s.id); !errors.Is(err, uploadsession.ErrUnknownSession) {
		t.Errorf("session after Close: %v; want %v", err, uploadsession.ErrUnknownSession)
	}
}

// TestUploadSessionUnsupported checks NewUploadSession's error for a
// server without upload sessions.
func TestUploadSessionUnsupported(t *testing.T) {
	c, _ := newSessionClient(t, false)
	if _, err := c.NewUploadSession(context.Background()); !errors.Is(err, errors.ErrUnsupported) {
		t.Errorf("NewUploadSession = %v; want an error matching %v", err, errors.ErrUnsupported)
	}
}

// TestUploadSessionVivify checks that vivifying isn't allowed in a session.
func TestUploadSessionVivify(t *testing.T) {
	c, _ := newSessionClient(t, true)
	s := newSession(t, c)
	h := NewUploadHandleFromString("a")
	h.Vivify = true
	if err := s.Upload(context.Background(), h); err == nil {
		t.Error("Upload with Vivify succeeded")
	}
}

// TestUploadSessionFailures checks that Flush reports blobs that failed to
// commit along with the durable ones, and all of them when the server
// loses the session.
func TestUploadSessionFailures(t *testing.T) {
	c, srv := newSessionClient(t, true)
	s := newSession(t, c)
	a, b := &test.Blob{Contents: "a"}, &test.Blob{Contents: "b"}
	srv.sto.mu.Lock()
	srv.sto.fail = map[blob.Ref]bool{b.BlobRef(): true}
	srv.sto.mu.Unlock()
	sessionUpload(t, s, a, b)
	durable, err := s.Flush(context.Background())
	if want := sortedSizedRefs(a); !slices.Equal(durable, want) {
		t.Errorf("Flush = %v; want %v", durable, want)
	}
	if be, ok := errors.AsType[*blobserver.BlobsError](err); !ok || be.Len() != 1 ||
		!strings.Contains(fmt.Sprint(be.Err(b.BlobRef())), errSessionStorage.Error()) {
		t.Errorf("Flush error = %v; want %v failing with %q", err, b.BlobRef(), errSessionStorage)
	}
	if _, ok := c.haveCache.StatBlobCache(b.BlobRef()); ok {
		t.Errorf("failed blob %v in the have cache", b.BlobRef())
	}

	// The server loses the session.
	d, e := &test.Blob{Contents: "d"}, &test.Blob{Contents: "e"}
	srv.sto.mu.Lock()
	srv.sto.fail = nil
	srv.sto.mu.Unlock()
	sessionUpload(t, s, d, e)
	ss, err := srv.sessions.Session(s.id)
	if err != nil {
		t.Fatal(err)
	}
	ss.Close()
	if err := s.Upload(context.Background(), NewUploadHandleFromString("f")); !errors.Is(err, uploadsession.ErrUnknownSession) {
		t.Errorf("Upload to a lost session = %v; want %v", err, uploadsession.ErrUnknownSession)
	}
	durable, err = s.Flush(context.Background())
	if len(durable) != 0 {
		t.Errorf("Flush of a lost session = %v; want none", durable)
	}
	be, ok := errors.AsType[*blobserver.BlobsError](err)
	if !ok || be.Len() != 2 || !errors.Is(be.Err(d.BlobRef()), uploadsession.ErrUnknownSession) || be.Err(e.BlobRef()) == nil {
		t.Errorf("Flush of a lost session error = %v; want %v and %v lost", err, d.BlobRef(), e.BlobRef())
	}
}

// TestUploadSessionFlushRetry checks that after a Flush fails as a whole,
// the next reports the same blobs, and acknowledges the blobs of the last
// successful Flush.
func TestUploadSessionFlushRetry(t *testing.T) {
	c, srv := newSessionClient(t, true)
	s := newSession(t, c)
	a, b := &test.Blob{Contents: "a"}, &test.Blob{Contents: "b"}
	sessionUpload(t, s, a)
	if _, err := s.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	sessionUpload(t, s, b)
	srv.mu.Lock()
	srv.failFlush = true
	srv.mu.Unlock()
	durable, err := s.Flush(context.Background())
	if _, ok := errors.AsType[*blobserver.BlobsError](err); err == nil || ok || durable != nil {
		t.Fatalf("failing Flush = %v, %v; want nil and a plain error", durable, err)
	}
	srv.mu.Lock()
	srv.failFlush = false
	srv.mu.Unlock()
	durable, err = s.Flush(context.Background())
	if want := sortedSizedRefs(b); err != nil || !slices.Equal(durable, want) {
		t.Errorf("Flush after a failed one = %v, %v; want %v", durable, err, want)
	}
	srv.mu.Lock()
	acks := srv.acks
	srv.mu.Unlock()
	want := []string{a.BlobRef().String()}
	if len(acks) != 3 || !slices.Equal(acks[1], want) || !slices.Equal(acks[2], want) {
		t.Errorf("flush acks = %q; want none, then %q twice", acks, want)
	}
}

// TestUploadSessionFlushDuringUpload checks that a blob a Flush reports
// durable before its Upload returns isn't later taken as lost.
func TestUploadSessionFlushDuringUpload(t *testing.T) {
	c, srv := newSessionClient(t, true)
	s := newSession(t, c)
	a := &test.Blob{Contents: "a"}
	hold, uploading := make(chan struct{}), make(chan struct{})
	srv.mu.Lock()
	srv.holdUpload, srv.uploading = hold, uploading
	srv.mu.Unlock()
	uploaded := make(chan error)
	go func() { uploaded <- s.Upload(context.Background(), NewUploadHandleFromString(a.Contents)) }()
	<-uploading // the server added it to the session, but hasn't responded
	durable, err := s.Flush(context.Background())
	if want := sortedSizedRefs(a); err != nil || !slices.Equal(durable, want) {
		t.Errorf("Flush during the Upload = %v, %v; want %v", durable, err, want)
	}
	srv.mu.Lock()
	srv.holdUpload, srv.uploading = nil, nil
	srv.mu.Unlock()
	close(hold)
	if err := <-uploaded; err != nil {
		t.Fatal(err)
	}
	if durable, err := s.Flush(context.Background()); err != nil || len(durable) != 0 {
		t.Errorf("Flush after the Upload = %v, %v; want nothing", durable, err)
	}
}

// flushCount returns how many flush requests srv has had.
func (srv *sessionServer) flushCount() int {
	srv.mu.Lock()
	defer srv.mu.Unlock()
	return len(srv.acks)
}

// TestWaitDurable checks that WaitDurable flushes to learn its blobs are
// durable, reports failures, and leaves what it learns for Flush.
func TestWaitDurable(t *testing.T) {
	c, srv := newSessionClient(t, true)
	s := newSession(t, c)
	a, b := &test.Blob{Contents: "a"}, &test.Blob{Contents: "b"}
	srv.sto.mu.Lock()
	srv.sto.fail = map[blob.Ref]bool{b.BlobRef(): true}
	srv.sto.mu.Unlock()
	sessionUpload(t, s, a, b)

	if err := s.WaitDurable(context.Background(), a.BlobRefSlice()); err != nil {
		t.Fatal(err)
	}
	if _, ok := c.haveCache.StatBlobCache(a.BlobRef()); !ok {
		t.Errorf("%v not in the have cache after WaitDurable", a.BlobRef())
	}
	err := s.WaitDurable(context.Background(), []blob.Ref{a.BlobRef(), b.BlobRef()})
	if be, ok := errors.AsType[*blobserver.BlobsError](err); !ok || be.Len() != 1 || be.Err(b.BlobRef()) == nil {
		t.Errorf("WaitDurable with a failed blob = %v; want %v failing", err, b.BlobRef())
	}
	if n := srv.flushCount(); n != 1 {
		t.Errorf("%d flush requests; want 1, as the second WaitDurable had nothing to wait for", n)
	}

	durable, err := s.Flush(context.Background())
	if want := sortedSizedRefs(a); !slices.Equal(durable, want) {
		t.Errorf("Flush after WaitDurable = %v; want %v", durable, want)
	}
	if be, ok := errors.AsType[*blobserver.BlobsError](err); !ok || be.Len() != 1 || be.Err(b.BlobRef()) == nil {
		t.Errorf("Flush after WaitDurable error = %v; want %v failing", err, b.BlobRef())
	}

	// Uploading the failed blob again makes it wait for the next flush.
	srv.sto.mu.Lock()
	srv.sto.fail = nil
	srv.sto.mu.Unlock()
	sessionUpload(t, s, b)
	if err := s.WaitDurable(context.Background(), b.BlobRefSlice()); err != nil {
		t.Errorf("WaitDurable after uploading again = %v", err)
	}
}

// TestWaitDurableSharesFlush checks that a WaitDurable call whose blobs a
// concurrent call's flush settles doesn't flush again.
func TestWaitDurableSharesFlush(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, srv := newSessionClient(t, true)
		s := newSession(t, c)
		a, b := &test.Blob{Contents: "a"}, &test.Blob{Contents: "b"}
		sessionUpload(t, s, a, b)
		hold, flushing := make(chan struct{}), make(chan struct{})
		srv.mu.Lock()
		srv.holdFlush, srv.flushing = hold, flushing
		srv.mu.Unlock()

		errc := make(chan error, 2)
		go func() { errc <- s.WaitDurable(context.Background(), a.BlobRefSlice()) }()
		<-flushing // the first call's flush is in progress
		srv.mu.Lock()
		srv.holdFlush, srv.flushing = nil, nil
		srv.mu.Unlock()
		go func() { errc <- s.WaitDurable(context.Background(), b.BlobRefSlice()) }()
		synctest.Wait() // the second call waits for the first's flush

		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := s.WaitDurable(ctx, b.BlobRefSlice()); !errors.Is(err, context.Canceled) {
			t.Errorf("WaitDurable with a done ctx during a flush = %v; want %v", err, context.Canceled)
		}
		close(hold)
		for range 2 {
			if err := <-errc; err != nil {
				t.Error(err)
			}
		}
		if n := srv.flushCount(); n != 1 {
			t.Errorf("%d flush requests; want 1", n)
		}
	})
}

// TestUploadSessionBatches checks that Upload calls waiting for an HTTP
// request to be free send their blobs together, and that blobs the have
// cache has aren't sent.
func TestUploadSessionBatches(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, srv := newSessionClient(t, true)
		s := newSession(t, c)
		srv.mu.Lock()
		srv.gate = make(chan struct{})
		srv.mu.Unlock()

		var blobs []*test.Blob
		for i := range 3 * s.maxSending {
			blobs = append(blobs, &test.Blob{Contents: fmt.Sprintf("blob %d", i)})
		}
		errc := make(chan error, len(blobs))
		upload := func(tb *test.Blob) {
			go func() { errc <- s.Upload(context.Background(), NewUploadHandleFromString(tb.Contents)) }()
		}
		for _, tb := range blobs[:s.maxSending] {
			upload(tb)
		}
		synctest.Wait() // each in its own request, held
		for _, tb := range blobs[s.maxSending:] {
			upload(tb)
		}
		synctest.Wait() // queued, for a free request
		srv.mu.Lock()
		close(srv.gate)
		srv.gate = nil
		srv.mu.Unlock()
		for range blobs {
			if err := <-errc; err != nil {
				t.Fatal(err)
			}
		}
		srv.mu.Lock()
		uploads := srv.uploads
		srv.mu.Unlock()
		if uploads != s.maxSending+1 {
			t.Errorf("%d upload requests for %d blobs; want %d: one each for the first %d, and one for the rest",
				uploads, len(blobs), s.maxSending+1, s.maxSending)
		}
		durable, err := s.Flush(context.Background())
		if err != nil || len(durable) != len(blobs) {
			t.Errorf("Flush = %d blobs, %v; want %d", len(durable), err, len(blobs))
		}

		// Blobs the have cache has (as it now has these) aren't sent.
		if err := s.Upload(context.Background(), NewUploadHandleFromString(blobs[0].Contents)); err != nil {
			t.Fatal(err)
		}
		srv.mu.Lock()
		again := srv.uploads
		srv.mu.Unlock()
		if again != uploads {
			t.Errorf("uploading a blob in the have cache made %d requests; want none", again-uploads)
		}
	})
}
