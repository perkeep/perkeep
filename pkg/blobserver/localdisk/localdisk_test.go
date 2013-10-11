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
	"strconv"
	"sync"
	"testing"

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
		errch <- ds.StatBlobs(ch, tb.BlobRefSlice())
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

func TestMultiStat(t *testing.T) {
	ds := NewStorage(t)
	defer cleanUp(ds)

	blobfoo := &test.Blob{"foo"}
	blobbar := &test.Blob{"bar!"}
	blobfoo.MustUpload(t, ds)
	blobbar.MustUpload(t, ds)

	need := make(map[blob.Ref]bool)
	need[blobfoo.BlobRef()] = true
	need[blobbar.BlobRef()] = true

	blobs := []blob.Ref{blobfoo.BlobRef(), blobbar.BlobRef()}

	// In addition to the two "foo" and "bar" blobs, add
	// maxParallelStats other dummy blobs, to exercise the stat
	// rate-limiting (which had a deadlock once after a cleanup)
	for i := 0; i < maxParallelStats; i++ {
		blobs = append(blobs, blob.SHA1FromString(strconv.Itoa(i)))
	}

	ch := make(chan blob.SizedRef, 0)
	errch := make(chan error, 1)
	go func() {
		errch <- ds.StatBlobs(ch, blobs)
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
