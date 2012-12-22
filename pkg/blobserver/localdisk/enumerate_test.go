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
	"io/ioutil"
	"os"
	"sort"
	"testing"
	"time"

	. "camlistore.org/pkg/test/asserts"
	"camlistore.org/pkg/blobref"
)

func TestEnumerate(t *testing.T) {
	ds := NewStorage(t)
	defer cleanUp(ds)

	// For test simplicity foo, bar, and baz all have ascending
	// sha1s and lengths.
	foo := &testBlob{"foo"}   // 0beec7b5ea3f0fdbc95d0dd47f3c5bc275da8a33
	bar := &testBlob{"baar"}  // b23361951dde70cb3eca44c0c674181673a129dc
	baz := &testBlob{"bazzz"} // e0eb17003ce1c2812ca8f19089fff44ca32b3710
	foo.ExpectUploadBlob(t, ds)
	bar.ExpectUploadBlob(t, ds)
	baz.ExpectUploadBlob(t, ds)

	limit := 5000
	waitSeconds := time.Duration(0)
	ch := make(chan blobref.SizedBlobRef)
	errCh := make(chan error)
	go func() {
		errCh <- ds.EnumerateBlobs(ch, "", limit, waitSeconds)
	}()

	var (
		sb blobref.SizedBlobRef
		ok bool
	)
	sb, ok = <-ch
	Assert(t, ok, "got 1st blob")
	ExpectInt(t, 3, int(sb.Size), "1st blob size")
	sb, ok = <-ch
	Assert(t, ok, "got 2nd blob")
	ExpectInt(t, 4, int(sb.Size), "2nd blob size")
	sb, ok = <-ch
	Assert(t, ok, "got 3rd blob")
	ExpectInt(t, 5, int(sb.Size), "3rd blob size")
	sb, ok = <-ch
	Assert(t, !ok, "got channel close")
	ExpectNil(t, <-errCh, "EnumerateBlobs return value")

	// Now again, but skipping foo's blob
	ch = make(chan blobref.SizedBlobRef)
	go func() {
		errCh <- ds.EnumerateBlobs(ch,
			foo.BlobRef().String(),
			limit, waitSeconds)
	}()
	sb, ok = <-ch
	Assert(t, ok, "got 1st blob, skipping foo")
	ExpectInt(t, 4, int(sb.Size), "blob size")
	sb, ok = <-ch
	Assert(t, ok, "got 2nd blob, skipping foo")
	ExpectInt(t, 5, int(sb.Size), "blob size")
	sb, ok = <-ch
	Assert(t, !ok, "got final nil")
	ExpectNil(t, <-errCh, "EnumerateBlobs return value")
}

func TestEnumerateEmpty(t *testing.T) {
	ds := NewStorage(t)
	defer cleanUp(ds)

	limit := 5000
	wait := time.Duration(0)
	ch := make(chan blobref.SizedBlobRef)
	errCh := make(chan error)
	go func() {
		errCh <- ds.EnumerateBlobs(ch, "", limit, wait)
	}()

	_, ok := <-ch
	Expect(t, !ok, "no first blob")
	ExpectNil(t, <-errCh, "EnumerateBlobs return value")
}

func TestEnumerateEmptyLongPoll(t *testing.T) {
	ds := NewStorage(t)
	defer cleanUp(ds)

	limit := 5000
	wait := 1 * time.Second
	ch := make(chan blobref.SizedBlobRef)
	errCh := make(chan error)
	go func() {
		errCh <- ds.EnumerateBlobs(ch, "", limit, wait)
	}()

	foo := &testBlob{"foo"} // 0beec7b5ea3f0fdbc95d0dd47f3c5bc275da8a33
	go func() {
		time.Sleep(100e6) // 100 ms
		foo.ExpectUploadBlob(t, ds)
	}()

	sb, ok := <-ch
	Assert(t, ok, "got a blob")
	ExpectInt(t, 3, int(sb.Size), "blob size")
	ExpectString(t, "sha1-0beec7b5ea3f0fdbc95d0dd47f3c5bc275da8a33", sb.BlobRef.String(), "got the right blob")

	sb, ok = <-ch
	Expect(t, !ok, "only one blob returned")
	ExpectNil(t, <-errCh, "EnumerateBlobs return value")
}

type SortedSizedBlobs []blobref.SizedBlobRef

func (sb SortedSizedBlobs) Len() int {
	return len(sb)
}

func (sb SortedSizedBlobs) Less(i, j int) bool {
	return sb[i].BlobRef.String() < sb[j].BlobRef.String()
}

func (sb SortedSizedBlobs) Swap(i, j int) {
	panic("not needed")
}

func TestEnumerateIsSorted(t *testing.T) {
	ds := NewStorage(t)
	defer cleanUp(ds)

	const blobsToMake = 250
	t.Logf("Uploading test blobs...")
	for i := 0; i < blobsToMake; i++ {
		blob := &testBlob{fmt.Sprintf("blob-%d", i)}
		blob.ExpectUploadBlob(t, ds)
	}

	// Make some fake blobs in other partitions to confuse the
	// enumerate code.
	fakeDir := ds.root + "/partition/queue-indexer/sha1/1f0/710"
	ExpectNil(t, os.MkdirAll(fakeDir, 0755), "creating fakeDir")
	ExpectNil(t, ioutil.WriteFile(fakeDir+"/sha1-1f07105465650aa243cfc1b1bbb1c68ea95c6812.dat",
		[]byte("fake file"), 0644), "writing fake blob")

	// And the same for a "cache" directory, used by the default configuration.
	fakeDir = ds.root + "/cache/sha1/1f0/710"
	ExpectNil(t, os.MkdirAll(fakeDir, 0755), "creating cache fakeDir")
	ExpectNil(t, ioutil.WriteFile(fakeDir+"/sha1-1f07105465650aa243cfc1b1bbb1c68ea95c6812.dat",
		[]byte("fake file"), 0644), "writing fake blob")

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
		ch := make(chan blobref.SizedBlobRef)
		errCh := make(chan error)
		go func() {
			errCh <- ds.EnumerateBlobs(ch, test.after, limit, 0)
		}()
		var got = make([]blobref.SizedBlobRef, 0, blobsToMake)
		for sb := range ch {
			got = append(got, sb)
		}
		if !sort.IsSorted(SortedSizedBlobs(got)) {
			t.Errorf("expected sorted; offset=%q, limit=%d", test.after, limit)
		}
	}
}
