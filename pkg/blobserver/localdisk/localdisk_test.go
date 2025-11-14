/*
Copyright 2011 The Perkeep Authors

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
	"context"
	"fmt"
	"os"
	"strconv"
	"sync"
	"testing"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver"
	"perkeep.org/pkg/blobserver/storagetest"
	"perkeep.org/pkg/test"
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
	tb := &test.Blob{Contents: "Foo"}
	tb.MustUpload(t, ds)
	tb.MustUpload(t, ds)
}

func TestReceiveStat(t *testing.T) {
	ds := NewStorage(t)
	defer cleanUp(ds)

	tb := &test.Blob{Contents: "Foo"}
	tb.MustUpload(t, ds)

	ctx := context.Background()
	got, err := blobserver.StatBlobs(ctx, ds, tb.BlobRefSlice())
	if err != nil {
		t.Fatalf("StatBlobs: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("got %d stat blobs; expected 1", len(got))
	}
	sb, ok := got[tb.BlobRef()]
	if !ok {
		t.Fatalf("stat response lacked information for %v", tb.BlobRef())
	}
	tb.AssertMatches(t, sb)
}

func TestMultiStat(t *testing.T) {
	ds := NewStorage(t)
	defer cleanUp(ds)

	blobfoo := &test.Blob{Contents: "foo"}
	blobbar := &test.Blob{Contents: "bar!"}
	blobfoo.MustUpload(t, ds)
	blobbar.MustUpload(t, ds)

	need := make(map[blob.Ref]bool)
	need[blobfoo.BlobRef()] = true
	need[blobbar.BlobRef()] = true

	blobs := []blob.Ref{blobfoo.BlobRef(), blobbar.BlobRef()}

	// In addition to the two "foo" and "bar" blobs, add
	// maxParallelStats other dummy blobs, to exercise the stat
	// rate-limiting (which had a deadlock once after a cleanup)
	const maxParallelStats = 20
	for i := range maxParallelStats {
		blobs = append(blobs, blob.RefFromString(strconv.Itoa(i)))
	}

	ctx := context.Background()
	gotStat, err := blobserver.StatBlobs(ctx, ds, blobs)
	if err != nil {
		t.Fatalf("StatBlobs: %v", err)
	}
	got := 0
	for _, sb := range gotStat {
		got++
		if !need[sb.Ref] {
			t.Errorf("didn't need %s", sb.Ref)
		}
		delete(need, sb.Ref)
	}
	if want := 2; got != want {
		t.Errorf("number stats = %d; want %d", got, want)
	}
	if len(need) != 0 {
		t.Errorf("Not all stat results returned; still need %d", len(need))
	}
}

func TestMissingGetReturnsNoEnt(t *testing.T) {
	ds := NewStorage(t)
	defer cleanUp(ds)
	foo := &test.Blob{Contents: "foo"}

	blob, _, err := ds.Fetch(context.Background(), foo.BlobRef())
	if err != os.ErrNotExist {
		t.Errorf("expected ErrNotExist; got %v", err)
	}
	if blob != nil {
		t.Errorf("expected nil blob; got a value")
	}
}

func TestLocaldisk(t *testing.T) {
	storagetest.Test(t, func(t *testing.T) blobserver.Storage {
		ds := NewStorage(t)
		t.Cleanup(func() { cleanUp(ds) })
		return ds
	})
}
