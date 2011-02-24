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
	"crypto/sha1"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
)

func cleanUp(ds *diskStorage) {
	os.RemoveAll(ds.root)
}

var (
	epochLock sync.Mutex
	rootEpoch = 0
)

func NewStorage(t *testing.T) *diskStorage {
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
	return ds.(*diskStorage)
}

type testBlob struct {
	val string
}

func (tb *testBlob) BlobRef() *blobref.BlobRef {
	h := sha1.New()
	h.Write([]byte(tb.val))
	return blobref.FromHash("sha1", h)
}

func (tb *testBlob) Size() int64 {
	return int64(len(tb.val))
}

func (tb *testBlob) Reader() io.Reader {
	return strings.NewReader(tb.val)
}

func TestReceiveStat(t *testing.T) {
	ds := NewStorage(t)
	defer cleanUp(ds)
	t.Logf("Storage is at: %q", ds.root)

	tb := &testBlob{"Foo"}
	sb, err := ds.ReceiveBlob(tb.BlobRef(), tb.Reader(), nil)
	if err != nil {
		t.Fatalf("ReceiveBlob error: %v", err)
	}
	checkSizedBlob := func() {
		if sb.Size != tb.Size() {
			t.Fatalf("Got size %d; expected %d", sb.Size, tb.Size())
		}
		if sb.BlobRef.String() != tb.BlobRef().String() {
			t.Fatalf("Got blob %q; expected %q", sb.BlobRef.String(), tb.BlobRef())
		}
	}
	checkSizedBlob()

	ch := make(chan *blobref.SizedBlobRef, 0)
	errch := make(chan os.Error, 0)
	go func() {
		errch <- ds.Stat(ch, blobserver.DefaultPartition, []*blobref.BlobRef{tb.BlobRef()}, 0)
	}()
	got := 0
	for sb = range ch {
		got++
		checkSizedBlob()
	}
	if got != 1 {
		t.Fatalf("Expected %d stat results; got %d", 1, got)
	}
	if err = <-errch; err != nil {
		t.Fatalf("Got error from stat: %v", err)
	}
}

func TestStatWait(t *testing.T) {
	ds := NewStorage(t)
	defer cleanUp(ds)
	t.Logf("Storage is at: %q", ds.root)
}
