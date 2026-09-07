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
	"testing"

	"go4.org/jsonconfig"

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
		return px
	})
	px.origin = memory.NewCache(0)
	px.populateOnSubFetch = true
	storagetest.Test(t, func(t *testing.T) blobserver.Storage {
		t.Cleanup(func() { cleanUp(ds) })
		return px
	})
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
		if err = rc.Close(); err != nil {
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
		if err = cached.Close(); err != nil {
			t.Error(err)
		}
	})
	if int(size) != len(tb.Contents) {
		t.Errorf("cached blob size = %d; want %d", size, len(tb.Contents))
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
