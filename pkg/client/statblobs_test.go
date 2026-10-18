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
	"sync"
	"sync/atomic"
	"testing"

	"perkeep.org/pkg/auth"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver/handlers"
	"perkeep.org/pkg/blobserver/memory"
	"perkeep.org/pkg/test"
)

// mapHaveCache is a HaveCache in a map.
type mapHaveCache struct {
	mu sync.Mutex
	m  map[blob.Ref]uint32
}

func (c *mapHaveCache) StatBlobCache(br blob.Ref) (uint32, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	size, ok := c.m[br]
	return size, ok
}

func (c *mapHaveCache) NoteBlobExists(br blob.Ref, size uint32) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.m[br] = size
}

// newStatClient returns a client of an in-memory server whose stat
// handler serves sto, and a count of its stat requests.
func newStatClient(t *testing.T, sto *memory.Storage) (*Client, *atomic.Int32) {
	t.Helper()
	var requests atomic.Int32
	stat := handlers.CreateStatHandler(sto, nil)
	ts := httptest.NewTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/bs/camli/stat" {
			http.NotFound(w, r)
			return
		}
		requests.Add(1)
		stat.ServeHTTP(w, r)
	}))
	c, err := New(OptionServer("http://example.com/bs"), OptionNoExternalConfig(), OptionAuthMode(auth.None{}))
	if err != nil {
		t.Fatal(err)
	}
	c.SetHTTPClient(ts.Client())
	return c, &requests
}

// TestStatBlobsBatches checks that StatBlobs asks the server about many
// blobs per request, reports blobs in the HaveCache without asking, and
// reports each blob it has once.
func TestStatBlobsBatches(t *testing.T) {
	sto := new(memory.Storage)
	var refs []blob.Ref
	have := map[blob.Ref]bool{}
	for i := range 2500 {
		tb := &test.Blob{Contents: fmt.Sprintf("blob %d", i)}
		refs = append(refs, tb.BlobRef())
		if i%2 == 0 {
			tb.MustUpload(t, sto)
			have[tb.BlobRef()] = true
		}
	}
	c, requests := newStatClient(t, sto)
	cached := &mapHaveCache{m: map[blob.Ref]uint32{refs[0]: 7}} // not the real size, to tell
	c.SetHaveCache(cached)

	var mu sync.Mutex
	got := map[blob.Ref]uint32{}
	err := c.StatBlobs(context.Background(), refs, func(sb blob.SizedRef) error {
		mu.Lock()
		defer mu.Unlock()
		if _, dup := got[sb.Ref]; dup {
			t.Errorf("%v reported twice", sb.Ref)
		}
		got[sb.Ref] = sb.Size
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if n := requests.Load(); n != 3 {
		t.Errorf("stat requests = %d; want 3, for 2499 uncached blobs", n)
	}
	if len(got) != len(have) {
		t.Errorf("reported %d blobs; want %d", len(got), len(have))
	}
	if got[refs[0]] != 7 {
		t.Errorf("cached blob reported with size %d; want its cached size, 7", got[refs[0]])
	}
	for br := range have {
		if _, ok := got[br]; !ok {
			t.Errorf("%v not reported", br)
		}
	}
	// What the server reported is cached now.
	if _, ok := cached.StatBlobCache(refs[2]); !ok {
		t.Error("blob the server has isn't in the HaveCache")
	}
}

func TestStatBlobsStopsOnError(t *testing.T) {
	sto := new(memory.Storage)
	var refs []blob.Ref
	for i := range 2500 {
		tb := &test.Blob{Contents: fmt.Sprintf("blob %d", i)}
		tb.MustUpload(t, sto)
		refs = append(refs, tb.BlobRef())
	}
	c, _ := newStatClient(t, sto)
	errStop := errors.New("stop")
	var calls atomic.Int32
	err := c.StatBlobs(context.Background(), refs, func(blob.SizedRef) error {
		calls.Add(1)
		return errStop
	})
	if !errors.Is(err, errStop) {
		t.Errorf("StatBlobs = %v; want %v", err, errStop)
	}
	if n := calls.Load(); n != 1 {
		t.Errorf("fn called %d times; want once", n)
	}
}
