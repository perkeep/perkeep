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

package localdisk

import (
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/test"
	. "camlistore.org/pkg/test/asserts"
)

func cleanUp(ds *DiskStorage) {
	os.RemoveAll(ds.root)
}

var (
	epochLock sync.Mutex
	rootEpoch = 0
)

func NewStorage(t *testing.T) *DiskStorage {
	epochLock.Lock()
	rootEpoch++
	path := fmt.Sprintf("%s/camli-testroot-%d-%d", os.TempDir(), os.Getpid(), rootEpoch)
	epochLock.Unlock()
	if err := os.Mkdir(path, 0755); err != nil {
		t.Fatalf("Failed to create temp directory %q: %v", path, err)
	}
	ds, err := New(path)
	if err != nil {
		t.Fatalf("Failed to run New: %v", err)
	}
	return ds
}

func TestUploadDup(t *testing.T) {
	ds := NewStorage(t)
	defer cleanUp(ds)
	ds.CreateQueue("some-queue")
	tb := &test.Blob{"Foo"}
	tb.MustUpload(t, ds)
	tb.MustUpload(t, ds)
}

func TestReceiveStat(t *testing.T) {
	ds := NewStorage(t)
	defer cleanUp(ds)

	tb := &test.Blob{"Foo"}
	tb.MustUpload(t, ds)

	ch := make(chan blob.SizedRef, 0)
	errch := make(chan error, 1)
	go func() {
		errch <- ds.StatBlobs(ch, tb.BlobRefSlice(), 0)
		close(ch)
	}()
	got := 0
	for sb := range ch {
		got++
		tb.AssertMatches(t, sb)
		break
	}
	AssertInt(t, 1, got, "number stat results")
	AssertNil(t, <-errch, "result from stat")
}

func TestStatWait(t *testing.T) {
	ds := NewStorage(t)
	defer cleanUp(ds)
	tb := &test.Blob{"Foo"}

	// Do a stat before the blob exists, but wait 2 seconds for it to arrive.
	wait := 2 * time.Second
	ch := make(chan blob.SizedRef, 0)
	errch := make(chan error, 1)
	go func() {
		errch <- ds.StatBlobs(ch, tb.BlobRefSlice(), wait)
		close(ch)
	}()

	// Sum and verify the stat results, writing the total number of returned matches
	// to statCountCh (expected: 1)
	statCountCh := make(chan int)
	go func() {
		got := 0
		for sb := range ch {
			got++
			tb.AssertMatches(t, sb)
		}
		statCountCh <- got
	}()

	// Now upload the blob, now that everything else is in-flight.
	// Sleep a bit to make sure the ds.Stat above has had a chance to fail and sleep.
	time.Sleep(1e9 / 5) // 200ms in nanos
	tb.MustUpload(t, ds)

	AssertInt(t, 1, <-statCountCh, "number stat results")
}

func TestMultiStat(t *testing.T) {
	ds := NewStorage(t)
	defer cleanUp(ds)

	blobfoo := &test.Blob{"foo"}
	blobbar := &test.Blob{"bar!"}
	blobfoo.MustUpload(t, ds)
	blobbar.MustUpload(t, ds)

	need := make(map[string]bool)
	need[blobfoo.BlobRef().String()] = true
	need[blobbar.BlobRef().String()] = true

	ch := make(chan blob.SizedRef, 0)
	errch := make(chan error, 1)
	go func() {
		errch <- ds.StatBlobs(ch,
			[]blob.Ref{blobfoo.BlobRef(), blobbar.BlobRef()},
			0)
		close(ch)
	}()
	got := 0
	for sb := range ch {
		got++
		br := sb.Ref
		brstr := br.String()
		Expect(t, need[brstr], "need stat of blobref "+brstr)
		delete(need, brstr)
	}
	ExpectInt(t, 2, got, "number stat results")
	ExpectNil(t, <-errch, "result from stat")
	ExpectInt(t, 0, len(need), "all stat results needed returned")
}

func TestMissingGetReturnsNoEnt(t *testing.T) {
	ds := NewStorage(t)
	defer cleanUp(ds)
	foo := &test.Blob{"foo"}

	blob, _, err := ds.Fetch(foo.BlobRef())
	if err != os.ErrNotExist {
		t.Errorf("expected ErrNotExist; got %v", err)
	}
	if blob != nil {
		t.Errorf("expected nil blob; got a value")
	}
}
