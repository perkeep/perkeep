/*
Copyright 2016 The Camlistore Authors.

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
	"fmt"
	"log"
	"os"
	"sync"
	"testing"

	"go4.org/jsonconfig"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/blobserver"
	"camlistore.org/pkg/blobserver/localdisk"
	"camlistore.org/pkg/blobserver/memory"
	"camlistore.org/pkg/blobserver/storagetest"
	"camlistore.org/pkg/test"
)

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

func NewProxiedDisk(t *testing.T) (*sto, *localdisk.DiskStorage) {
	ds := NewDiskStorage(t)
	return NewCache(cacheSize, memory.NewCache(cacheSize), ds).(*sto), ds
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

	_, _, err := px.cache.Fetch(tb.BlobRef())
	if err != nil {
		t.Fatal("ref should still be in the proxy:", err)
	}

	test.RandomBlob(t, blobsize).MustUpload(t, px)
	_, _, err = px.cache.Fetch(tb.BlobRef())
	if err == nil {
		t.Fatal("ref should have been evicted from the proxy")
	}

	_, _, err = px.Fetch(tb.BlobRef())
	if err != nil {
		t.Fatal("ref should be available via the proxy fetching from origin:", err)
	}
}

func TestReceiveStat(t *testing.T) {
	px, ds := NewProxiedDisk(t)
	defer cleanUp(ds)

	tb := &test.Blob{"Foo"}
	tb.MustUpload(t, ds)

	// get the stat via the cold proxycache
	ch := make(chan blob.SizedRef, 0)
	errch := make(chan error, 1)
	go func() {
		errch <- px.StatBlobs(ch, tb.BlobRefSlice())
		close(ch)
	}()
	got := 0
	for sb := range ch {
		got++
		tb.AssertMatches(t, sb)
	}
	if err := <-errch; err != nil {
		t.Fatalf("result from stat (cold cache): %v", err)
	}
	if got != 1 {
		t.Fatalf("number stat results (cold cache), expected %d, got %d", 1, got)
	}

	// get the stat via the warmed cache
	px.origin = blobserver.NoImplStorage{} // force using the warmed cache
	ch = make(chan blob.SizedRef, 0)
	errch = make(chan error, 1)
	go func() {
		errch <- px.statsCache.StatBlobs(ch, tb.BlobRefSlice())
		close(ch)
	}()
	got = 0
	for sb := range ch {
		got++
		tb.AssertMatches(t, sb)
	}

	if err := <-errch; err != nil {
		t.Fatalf("result from stat (warm cache): %v", err)
	}
	if got != 1 {
		t.Fatalf("number stat results (warm cache), expected %d, got %d", 1, got)
	}

}

func TestMultiStat(t *testing.T) {
	px, ds := NewProxiedDisk(t)
	defer cleanUp(ds)

	blobfoo := &test.Blob{"foo"}
	blobbar := &test.Blob{"bar!"}
	blobfoo.MustUpload(t, ds)
	blobbar.MustUpload(t, ds)

	need := make(map[blob.Ref]bool)
	need[blobfoo.BlobRef()] = true
	need[blobbar.BlobRef()] = true

	blobs := []blob.Ref{blobfoo.BlobRef(), blobbar.BlobRef()}

	ch := make(chan blob.SizedRef, 0)
	errch := make(chan error, 1)
	go func() {
		errch <- px.StatBlobs(ch, blobs)
		close(ch)
	}()
	got := 0
	for sb := range ch {
		got++
		if !need[sb.Ref] {
			t.Errorf("didn't need %s", sb.Ref)
		}
		delete(need, sb.Ref)
	}
	if want := 2; got != want {
		t.Errorf("number stats = %d; want %d", got, want)
	}
	if err := <-errch; err != nil {
		t.Errorf("StatBlobs: %v", err)
	}
	if len(need) != 0 {
		t.Errorf("Not all stat results returned; still need %d", len(need))
	}
}

func TestMissingGetReturnsNoEnt(t *testing.T) {
	px, ds := NewProxiedDisk(t)
	defer cleanUp(ds)
	foo := &test.Blob{"foo"}

	blob, _, err := px.Fetch(foo.BlobRef())
	if err != os.ErrNotExist {
		t.Errorf("expected ErrNotExist; got %v", err)
	}
	if blob != nil {
		t.Errorf("expected nil blob; got a value")
	}
}

func TestProxyCache(t *testing.T) {
	px, ds := NewProxiedDisk(t)
	storagetest.Test(t, func(t *testing.T) (blobserver.Storage, func()) {
		return px, func() {}
	})
	px.origin = memory.NewCache(0)
	storagetest.Test(t, func(t *testing.T) (blobserver.Storage, func()) {
		return px, func() { cleanUp(ds) }
	})
}

func TestVerify(t *testing.T) {
	px, ds := NewProxiedDisk(t)
	defer cleanUp(ds)

	blobfoo := &test.Blob{"foo"}
	blobbar := &test.Blob{"bar!"}
	blobbaz := &test.Blob{"baz~!"} // not going to upload this one

	blobfoo.MustUpload(t, px)
	blobbar.MustUpload(t, px)

	err := px.verifyCache(blobfoo.BlobRefSlice())
	if err != nil {
		t.Errorf("unexpected error verifying blobfoo: %v", err)
	}

	err = px.verifyCache(blobbaz.BlobRefSlice())
	if err, ok := err.(CacheMissingRefError); !ok {
		t.Errorf("expected error CacheMissingRefError verifying blobbaz")
	} else if err.Ref != blobbaz.BlobRef() {
		t.Errorf("error ref did not match blobbaz ref")
	}

	px.origin = memory.NewCache(0)
	err = px.verifyCache(nil)
	if err == nil {
		t.Error("expected some errors from verifyCache after we messed it up")
	}
	t.Log(err)
}

func TestConfig(t *testing.T) {
	const maxBytes = 1 << 5
	px, ds := NewProxiedDisk(t)

	ld := test.NewLoader()
	ld.SetStorage("origin", ds)
	ld.SetStorage("cache", px)

	cache, err := newFromConfig(ld, jsonconfig.Obj{
		"origin":        "origin",
		"cache":         "cache",
		"maxCacheBytes": float64(maxBytes),
	})

	if err != nil {
		t.Fatal(err)
	}

	if cache.(*sto).maxCacheBytes != maxBytes {
		t.Fatalf("incorrectly read maxCacheBytes. saw: %d expected: %d",
			cache.(*sto).maxCacheBytes, maxBytes)
	}
}
