/*
Copyright 2016 The Perkeep Authors.

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

package proxycache

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"go4.org/jsonconfig"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver"
	"perkeep.org/pkg/blobserver/localdisk"
	"perkeep.org/pkg/blobserver/memory"
	"perkeep.org/pkg/blobserver/storagetest"
	"perkeep.org/pkg/test"
)

var ctxbg = context.Background()

func cleanUp(ds *localdisk.DiskStorage) {
	err := os.RemoveAll(rootDir)
	if err != nil {
		log.Printf("error removing cache (%s): %v", rootDir, err)
	}
}

var (
	epochLock sync.Mutex
	rootEpoch = 0
	rootDir   string
)

func NewDiskStorage(t *testing.T) *localdisk.DiskStorage {
	epochLock.Lock()
	rootEpoch++
	path := fmt.Sprintf("%s/camli-testroot-%d-%d", os.TempDir(), os.Getpid(), rootEpoch)
	rootDir = path
	epochLock.Unlock()
	if err := os.Mkdir(path, 0755); err != nil {
		t.Fatalf("Failed to create temp directory %q: %v", path, err)
	}
	ds, err := localdisk.New(path)
	if err != nil {
		t.Fatalf("Failed to run New: %v", err)
	}
	return ds
}

const cacheSize = 1 << 20

func NewProxiedDisk(t *testing.T) (*Storage, *localdisk.DiskStorage) {
	ds := NewDiskStorage(t)
	return New(cacheSize, memory.NewCache(cacheSize), ds), ds
}

func TestEviction(t *testing.T) {
	const blobsize = cacheSize / 6
	px, ds := NewProxiedDisk(t)
	defer cleanUp(ds)

	tb := test.RandomBlob(t, blobsize)
	tb.MustUpload(t, px)
	test.RandomBlob(t, blobsize).MustUpload(t, px)
	test.RandomBlob(t, blobsize).MustUpload(t, px)
	test.RandomBlob(t, blobsize).MustUpload(t, px)
	test.RandomBlob(t, blobsize).MustUpload(t, px)
	test.RandomBlob(t, blobsize).MustUpload(t, px)

	_, _, err := px.cache.Fetch(ctxbg, tb.BlobRef())
	if err != nil {
		t.Fatal("ref should still be in the proxy:", err)
	}

	test.RandomBlob(t, blobsize).MustUpload(t, px)
	_, _, err = px.cache.Fetch(ctxbg, tb.BlobRef())
	if err == nil {
		t.Fatal("ref should have been evicted from the proxy")
	}

	_, _, err = px.Fetch(ctxbg, tb.BlobRef())
	if err != nil {
		t.Fatal("ref should be available via the proxy fetching from origin:", err)
	}
}

func TestMissingGetReturnsNoEnt(t *testing.T) {
	px, ds := NewProxiedDisk(t)
	defer cleanUp(ds)
	foo := &test.Blob{Contents: "foo"}

	blob, _, err := px.Fetch(ctxbg, foo.BlobRef())
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("expected ErrNotExist; got %v", err)
	}
	if blob != nil {
		t.Errorf("expected nil blob; got a value")
	}
}

func TestProxyCache(t *testing.T) {
	px, ds := NewProxiedDisk(t)
	storagetest.Test(t, func(t *testing.T) blobserver.Storage {
		return px
	})
	px.origin = memory.NewCache(0)
	storagetest.Test(t, func(t *testing.T) blobserver.Storage {
		t.Cleanup(func() { cleanUp(ds) })
		return px
	})

	// A maxCacheBytes of 0 evicts each blob as soon as it's cached, so
	// every SubFetch misses the cache and populates it.
	evicting := New(0, memory.NewCache(0), memory.NewCache(0))
	evicting.populateOnSubFetch = true
	storagetest.Test(t, func(t *testing.T) blobserver.Storage {
		return evicting
	})

	// With a cache that doesn't do ranged reads, SubFetch reads the
	// whole blob from the cache.
	noRanges := New(cacheSize, struct{ blobserver.Storage }{memory.NewCache(0)}, memory.NewCache(0))
	noRanges.populateOnSubFetch = true
	storagetest.Test(t, func(t *testing.T) blobserver.Storage {
		return noRanges
	})
}

// countingStorage counts calls to Fetch. If release is non-nil, Fetch
// waits for it to be closed or for its context to be done.
//
// Embedding the interface hides any SubFetch method of the wrapped
// storage.
type countingStorage struct {
	blobserver.Storage
	release chan struct{}
	fetches atomic.Int32
}

func (s *countingStorage) Fetch(ctx context.Context, br blob.Ref) (io.ReadCloser, uint32, error) {
	s.fetches.Add(1)
	if s.release != nil {
		select {
		case <-s.release:
		case <-ctx.Done():
			return nil, 0, ctx.Err()
		}
	}
	return s.Storage.Fetch(ctx, br)
}

// subFetchString reads length bytes at offset of br from px.
func subFetchString(ctx context.Context, px *Storage, br blob.Ref, offset, length int64) (string, error) {
	rc, err := px.SubFetch(ctx, br, offset, length)
	if err != nil {
		return "", err
	}
	defer rc.Close()
	got, err := io.ReadAll(rc)
	return string(got), err
}

func TestSubFetchMissesShareOriginFetch(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		origin := &countingStorage{Storage: memory.NewCache(0), release: make(chan struct{})}
		tb := &test.Blob{Contents: "Some big blob"}
		tb.MustUpload(t, origin)
		px := New(cacheSize, memory.NewCache(0), origin)
		px.populateOnSubFetch = true

		var wg sync.WaitGroup
		for range 3 {
			wg.Go(func() {
				got, err := subFetchString(ctxbg, px, tb.BlobRef(), 5, 3)
				if err != nil || got != "big" {
					t.Errorf("SubFetch = %q, %v; want %q", got, err, "big")
				}
			})
		}
		synctest.Wait()
		close(origin.release)
		wg.Wait()

		if n := origin.fetches.Load(); n != 1 {
			t.Errorf("origin fetches = %d; want 1", n)
		}
	})
}

func TestSubFetchCanceledStillPopulates(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		origin := &countingStorage{Storage: memory.NewCache(0), release: make(chan struct{})}
		tb := &test.Blob{Contents: "Some big blob"}
		tb.MustUpload(t, origin)
		px := New(cacheSize, memory.NewCache(0), origin)
		px.populateOnSubFetch = true

		ctx, cancel := context.WithCancel(ctxbg)
		errc := make(chan error, 1)
		go func() {
			_, err := subFetchString(ctx, px, tb.BlobRef(), 5, 3)
			errc <- err
		}()
		synctest.Wait()
		cancel()
		if err := <-errc; !errors.Is(err, context.Canceled) {
			t.Errorf("SubFetch error = %v; want %v", err, context.Canceled)
		}

		close(origin.release)
		synctest.Wait()
		if _, _, err := px.cache.Fetch(ctxbg, tb.BlobRef()); err != nil {
			t.Errorf("blob should have been populated into the cache after the caller gave up: %v", err)
		}
	})
}

func TestSubFetchFromNonRangedCache(t *testing.T) {
	origin := &countingStorage{Storage: memory.NewCache(0)}
	tb := &test.Blob{Contents: "Some big blob"}
	tb.MustUpload(t, origin)
	px := New(cacheSize, struct{ blobserver.Storage }{memory.NewCache(0)}, origin)
	px.populateOnSubFetch = true

	for range 2 {
		got, err := subFetchString(ctxbg, px, tb.BlobRef(), 5, 3)
		if err != nil || got != "big" {
			t.Errorf("SubFetch = %q, %v; want %q", got, err, "big")
		}
	}
	if _, err := px.SubFetch(ctxbg, tb.BlobRef(), int64(len(tb.Contents))+1, 1); !errors.Is(err, blob.ErrOutOfRangeOffsetSubFetch) {
		t.Errorf("out of range SubFetch error = %v; want %v", err, blob.ErrOutOfRangeOffsetSubFetch)
	}
	if n := origin.fetches.Load(); n != 1 {
		t.Errorf("origin fetches = %d; want 1, with later reads served from the cache", n)
	}
}

func TestProxyCachePopulateOnSubFetch(t *testing.T) {
	px, ds := NewProxiedDisk(t)
	defer cleanUp(ds)
	px.populateOnSubFetch = true

	// Upload to origin only, so the cache starts cold.
	tb := &test.Blob{Contents: "Some big blob"}
	tb.MustUpload(t, ds)

	if _, _, err := px.cache.Fetch(ctxbg, tb.BlobRef()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("blob should not be in the cache yet; got err %v", err)
	}

	rc, err := px.SubFetch(ctxbg, tb.BlobRef(), 5, 3)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := rc.Close(); err != nil {
			t.Error(err)
		}
	})
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "big" {
		t.Errorf("SubFetch got %q; want %q", got, "big")
	}

	cached, size, err := px.cache.Fetch(ctxbg, tb.BlobRef())
	if err != nil {
		t.Fatalf("blob should have been populated into the cache: %v", err)
	}
	t.Cleanup(func() {
		if err := cached.Close(); err != nil {
			t.Error(err)
		}
	})
	if int(size) != len(tb.Contents) {
		t.Errorf("cached blob size = %d; want %d", size, len(tb.Contents))
	}
}

func TestSubFetchHitTouchesLRU(t *testing.T) {
	const blobsize = 1 << 10
	ds := NewDiskStorage(t)
	defer cleanUp(ds)
	px := New(3*blobsize, memory.NewCache(0), ds)
	px.populateOnSubFetch = true

	// Upload to origin only, so the first SubFetch populates the cache.
	tb := test.RandomBlob(t, blobsize)
	tb.MustUpload(t, ds)

	subFetch := func() {
		t.Helper()
		rc, err := px.SubFetch(ctxbg, tb.BlobRef(), 0, 1)
		if err != nil {
			t.Fatal(err)
		}
		if err := rc.Close(); err != nil {
			t.Fatal(err)
		}
	}

	subFetch()
	oldest := test.RandomBlob(t, blobsize)
	oldest.MustUpload(t, px)
	subFetch()
	test.RandomBlob(t, blobsize).MustUpload(t, px)
	subFetch()
	// The cache holds three blobs, so this evicts the least recently used.
	test.RandomBlob(t, blobsize).MustUpload(t, px)

	if _, _, err := px.cache.Fetch(ctxbg, tb.BlobRef()); err != nil {
		t.Errorf("blob read via SubFetch should still be in the cache: %v", err)
	}
	if _, _, err := px.cache.Fetch(ctxbg, oldest.BlobRef()); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("least recently used blob should have been evicted; got err %v", err)
	}
}

func TestConfig(t *testing.T) {
	const maxBytes = 1 << 5
	px, ds := NewProxiedDisk(t)

	ld := test.NewLoader()
	ld.SetStorage("origin", ds)
	ld.SetStorage("cache", px)

	cache, err := newFromConfig(ld, jsonconfig.Obj{
		"origin":             "origin",
		"cache":              "cache",
		"maxCacheBytes":      float64(maxBytes),
		"populateOnSubFetch": true,
		"debug":              true,
	})

	if err != nil {
		t.Fatal(err)
	}

	sto := cache.(*Storage)
	if sto.maxCacheBytes != maxBytes {
		t.Fatalf("incorrectly read maxCacheBytes. saw: %d expected: %d",
			sto.maxCacheBytes, maxBytes)
	}
	if !sto.populateOnSubFetch {
		t.Error("incorrectly read populateOnSubFetch. saw: false expected: true")
	}
	if !sto.debug {
		t.Error("incorrectly read debug. saw: false expected: true")
	}
}
