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

package files_test

import (
	"context"
	"fmt"
	"os"
	"sort"
	"sync"
	"testing"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver"
	"perkeep.org/pkg/blobserver/files"
	"perkeep.org/pkg/test"
)

var (
	epochLock sync.Mutex
	rootEpoch = 0
)

func NewTestStorage(t *testing.T) (sto blobserver.Storage, root string) {
	epochLock.Lock()
	rootEpoch++
	path := fmt.Sprintf("%s/camli-testroot-%d-%d", os.TempDir(), os.Getpid(), rootEpoch)
	epochLock.Unlock()
	if err := os.MkdirAll(path, 0755); err != nil {
		t.Fatalf("Failed to create temp directory %q: %v", path, err)
	}
	return files.NewStorage(files.OSFS(), path), path
}

func TestEnumerate(t *testing.T) {
	ds, root := NewTestStorage(t)
	defer os.RemoveAll(root)

	// For test simplicity foo, bar, and baz all have ascending
	// sha1s and lengths.
	foo := &test.Blob{Contents: "foo"}   // 0beec7b5ea3f0fdbc95d0dd47f3c5bc275da8a33
	bar := &test.Blob{Contents: "baar"}  // b23361951dde70cb3eca44c0c674181673a129dc
	baz := &test.Blob{Contents: "bazzz"} // e0eb17003ce1c2812ca8f19089fff44ca32b3710
	foo.MustUpload(t, ds)
	bar.MustUpload(t, ds)
	baz.MustUpload(t, ds)

	limit := 5000
	ch := make(chan blob.SizedRef)
	errCh := make(chan error)
	go func() {
		errCh <- ds.EnumerateBlobs(context.TODO(), ch, "", limit)
	}()

	assertBlobSize := func(expected uint32, sb blob.SizedRef, ok bool) {
		t.Helper()
		if !ok {
			t.Error("expected to get blob, but did not")
		}
		if sb.Size != expected {
			t.Errorf("expected size %v; got %v", expected, sb.Size)
		}
	}

	sb, ok := <-ch
	assertBlobSize(3, sb, ok)
	sb, ok = <-ch
	assertBlobSize(4, sb, ok)
	sb, ok = <-ch
	assertBlobSize(5, sb, ok)
	assertNoBlobsOrErrs(t, ch, errCh)

	// Now again, but skipping foo's blob
	ch = make(chan blob.SizedRef)
	go func() {
		errCh <- ds.EnumerateBlobs(context.TODO(),
			ch,
			foo.BlobRef().String(),
			limit)
	}()
	sb, ok = <-ch
	assertBlobSize(4, sb, ok)
	sb, ok = <-ch
	assertBlobSize(5, sb, ok)
	assertNoBlobsOrErrs(t, ch, errCh)
}

func TestEnumerateEmpty(t *testing.T) {
	ds, root := NewTestStorage(t)
	defer os.RemoveAll(root)

	limit := 5000
	ch := make(chan blob.SizedRef)
	errCh := make(chan error)
	go func() {
		errCh <- ds.EnumerateBlobs(context.TODO(), ch, "", limit)
	}()

	assertNoBlobsOrErrs(t, ch, errCh)
}

type SortedSizedBlobs []blob.SizedRef

func (sb SortedSizedBlobs) Len() int {
	return len(sb)
}

func (sb SortedSizedBlobs) Less(i, j int) bool {
	return sb[i].Ref.String() < sb[j].Ref.String()
}

func (sb SortedSizedBlobs) Swap(i, j int) {
	panic("not needed")
}

func TestEnumerateIsSorted(t *testing.T) {
	ds, root := NewTestStorage(t)
	defer os.RemoveAll(root)

	const blobsToMake = 250
	t.Logf("Uploading test blobs...")
	for i := range blobsToMake {
		blob := &test.Blob{Contents: fmt.Sprintf("blob-%d", i)}
		blob.MustUpload(t, ds)
	}

	// Make some fake blobs in other partitions to confuse the
	// enumerate code.
	// TODO(bradfitz): remove this eventually.
	fakeDir := root + "/partition/queue-indexer/sha1/1f0/710"
	if err := os.MkdirAll(fakeDir, 0755); err != nil {
		t.Fatalf("error creating fakedir: %v", err)
	}
	if err := os.WriteFile(
		fakeDir+"/sha1-1f07105465650aa243cfc1b1bbb1c68ea95c6812.dat",
		[]byte("fake file"),
		0644,
	); err != nil {
		t.Fatalf("error writing fake blob: %v", err)
	}

	// And the same for a "cache" directory, used by the default configuration.
	fakeDir = root + "/cache/sha1/1f0/710"
	if err := os.MkdirAll(fakeDir, 0755); err != nil {
		t.Fatalf("error creating cachedir: %v", err)
	}
	if err := os.WriteFile(
		fakeDir+"/sha1-1f07105465650aa243cfc1b1bbb1c68ea95c6812.dat",
		[]byte("fake file"),
		0644,
	); err != nil {
		t.Fatalf("error writing fake file: %v", err)
	}

	var tests = []struct {
		limit int
		after string
	}{
		{200, ""},
		{blobsToMake, ""},
		{200, "sha1-2"},
		{200, "sha1-3"},
		{200, "sha1-4"},
		{200, "sha1-5"},
		{200, "sha1-e"},
		{200, "sha1-f"},
		{200, "sha1-ff"},
	}
	for _, test := range tests {
		limit := test.limit
		ch := make(chan blob.SizedRef)
		errCh := make(chan error)
		go func() {
			errCh <- ds.EnumerateBlobs(context.TODO(), ch, test.after, limit)
		}()
		got := make([]blob.SizedRef, 0, blobsToMake)
		for sb := range ch {
			got = append(got, sb)
		}
		if err := <-errCh; err != nil {
			t.Errorf("case %+v; enumerate error: %v", test, err)
			continue
		}
		if !sort.IsSorted(SortedSizedBlobs(got)) {
			t.Errorf("case %+v: expected sorted; got: %q", test, got)
		}
	}
}

func assertNoBlobsOrErrs(t *testing.T, blobChannel <-chan blob.SizedRef, errChannel <-chan error) {
	t.Helper()

	if sb, ok := <-blobChannel; ok {
		t.Errorf("expected blob channel to be closed, but got %v", sb)
	}
	if err := <-errChannel; err != nil {
		t.Errorf("expected error channel to have a nil err; got %v", err)
	}
}
