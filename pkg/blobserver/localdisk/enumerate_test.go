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

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/test"
	. "camlistore.org/pkg/test/asserts"
	"golang.org/x/net/context"
)

func TestEnumerate(t *testing.T) {
	ds := NewStorage(t)
	defer cleanUp(ds)

	// For test simplicity foo, bar, and baz all have ascending
	// sha1s and lengths.
	foo := &test.Blob{"foo"}   // 0beec7b5ea3f0fdbc95d0dd47f3c5bc275da8a33
	bar := &test.Blob{"baar"}  // b23361951dde70cb3eca44c0c674181673a129dc
	baz := &test.Blob{"bazzz"} // e0eb17003ce1c2812ca8f19089fff44ca32b3710
	foo.MustUpload(t, ds)
	bar.MustUpload(t, ds)
	baz.MustUpload(t, ds)

	limit := 5000
	ch := make(chan blob.SizedRef)
	errCh := make(chan error)
	go func() {
		errCh <- ds.EnumerateBlobs(context.TODO(), ch, "", limit)
	}()

	var (
		sb blob.SizedRef
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
	ch = make(chan blob.SizedRef)
	go func() {
		errCh <- ds.EnumerateBlobs(context.TODO(),
			ch,
			foo.BlobRef().String(),
			limit)
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
	ch := make(chan blob.SizedRef)
	errCh := make(chan error)
	go func() {
		errCh <- ds.EnumerateBlobs(context.TODO(), ch, "", limit)
	}()

	_, ok := <-ch
	Expect(t, !ok, "no first blob")
	ExpectNil(t, <-errCh, "EnumerateBlobs return value")
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
	ds := NewStorage(t)
	defer cleanUp(ds)

	const blobsToMake = 250
	t.Logf("Uploading test blobs...")
	for i := 0; i < blobsToMake; i++ {
		blob := &test.Blob{fmt.Sprintf("blob-%d", i)}
		blob.MustUpload(t, ds)
	}

	// Make some fake blobs in other partitions to confuse the
	// enumerate code.
	// TODO(bradfitz): remove this eventually.
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
