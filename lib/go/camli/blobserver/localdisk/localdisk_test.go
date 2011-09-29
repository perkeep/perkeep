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
	"camli/blobref"
	"camli/blobserver"
	. "camli/test/asserts"
	"crypto/sha1"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
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

type testBlob struct {
	val string
}

func (tb *testBlob) BlobRef() *blobref.BlobRef {
	h := sha1.New()
	h.Write([]byte(tb.val))
	return blobref.FromHash("sha1", h)
}

func (tb *testBlob) BlobRefSlice() []*blobref.BlobRef {
	return []*blobref.BlobRef{tb.BlobRef()}
}

func (tb *testBlob) Size() int64 {
	return int64(len(tb.val))
}

func (tb *testBlob) Reader() io.Reader {
	return strings.NewReader(tb.val)
}

func (tb *testBlob) AssertMatches(t *testing.T, sb blobref.SizedBlobRef) {
	if sb.Size != tb.Size() {
		t.Fatalf("Got size %d; expected %d", sb.Size, tb.Size())
	}
	if sb.BlobRef.String() != tb.BlobRef().String() {
		t.Fatalf("Got blob %q; expected %q", sb.BlobRef.String(), tb.BlobRef())
	}
}

func (tb *testBlob) ExpectUploadBlob(t *testing.T, ds blobserver.BlobReceiver) {
	sb, err := ds.ReceiveBlob(tb.BlobRef(), tb.Reader())
	if err != nil {
		t.Fatalf("ReceiveBlob error: %v", err)
	}
	tb.AssertMatches(t, sb)
}

func TestUploadDup(t *testing.T) {
	ds := NewStorage(t)
	defer cleanUp(ds)
	ds.CreateQueue("some-queue")
	tb := &testBlob{"Foo"}
	tb.ExpectUploadBlob(t, ds)
	tb.ExpectUploadBlob(t, ds)
}

func TestReceiveStat(t *testing.T) {
	ds := NewStorage(t)
	defer cleanUp(ds)

	tb := &testBlob{"Foo"}
	tb.ExpectUploadBlob(t, ds)

	ch := make(chan blobref.SizedBlobRef, 0)
	errch := make(chan os.Error, 1)
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
	tb := &testBlob{"Foo"}

	// Do a stat before the blob exists, but wait 2 seconds for it to arrive.
	const waitSeconds = 2
	ch := make(chan blobref.SizedBlobRef, 0)
	errch := make(chan os.Error, 1)
	go func() {
		errch <- ds.StatBlobs(ch, tb.BlobRefSlice(), waitSeconds)
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
	tb.ExpectUploadBlob(t, ds)

	AssertInt(t, 1, <-statCountCh, "number stat results")
}

func TestMultiStat(t *testing.T) {
	ds := NewStorage(t)
	defer cleanUp(ds)

	blobfoo := &testBlob{"foo"}
	blobbar := &testBlob{"bar!"}
	blobfoo.ExpectUploadBlob(t, ds)
	blobbar.ExpectUploadBlob(t, ds)

	need := make(map[string]bool)
	need[blobfoo.BlobRef().String()] = true
	need[blobbar.BlobRef().String()] = true

	ch := make(chan blobref.SizedBlobRef, 0)
	errch := make(chan os.Error, 1)
	go func() {
		errch <- ds.StatBlobs(ch,
			[]*blobref.BlobRef{blobfoo.BlobRef(), blobbar.BlobRef()},
			0)
		close(ch)
	}()
	got := 0
	for sb := range ch {
		got++
		br := sb.BlobRef
		brstr := br.String()
		Expect(t, need[brstr], "need stat of blobref "+brstr)
		need[brstr] = false, false
	}
	ExpectInt(t, 2, got, "number stat results")
	ExpectNil(t, <-errch, "result from stat")
	ExpectInt(t, 0, len(need), "all stat results needed returned")
}

func TestMissingGetReturnsNoEnt(t *testing.T) {
	ds := NewStorage(t)
	defer cleanUp(ds)
	foo := &testBlob{"foo"}

	blob, _, err := ds.Fetch(foo.BlobRef())
	if err != os.ENOENT {
		t.Errorf("expected ENOENT; got %v", err)
	}
	if blob != nil {
		t.Errorf("expected nil blob; got a value")
	}
}
