/*
Copyright 2013 The Camlistore Authors

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

// Package storagetest tests blobserver.Storage implementations
package storagetest

import (
	"errors"
	"fmt"
	"io"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/blobserver"
	"camlistore.org/pkg/context"
	"camlistore.org/pkg/syncutil"
	"camlistore.org/pkg/test"
	"camlistore.org/pkg/types"
)

func Test(t *testing.T, fn func(*testing.T) (sto blobserver.Storage, cleanup func())) {
	sto, cleanup := fn(t)
	defer func() {
		if t.Failed() {
			t.Logf("test %T FAILED, skipping cleanup!", sto)
		} else {
			cleanup()
		}
	}()
	t.Logf("Testing blobserver storage %T", sto)

	t.Logf("Testing Enumerate for empty")
	testEnumerate(t, sto, nil)

	var blobs []*test.Blob
	var blobRefs []blob.Ref
	var blobSizedRefs []blob.SizedRef

	contents := []string{"foo", "quux", "asdf", "qwerty", "0123456789"}
	if !testing.Short() {
		for i := 0; i < 95; i++ {
			contents = append(contents, "foo-"+strconv.Itoa(i))
		}
	}
	t.Logf("Testing receive")
	for _, x := range contents {
		b1 := &test.Blob{x}
		b1s, err := sto.ReceiveBlob(b1.BlobRef(), b1.Reader())
		if err != nil {
			t.Fatalf("ReceiveBlob of %s: %v", b1, err)
		}
		if b1s != b1.SizedRef() {
			t.Fatal("Received %v; want %v", b1s, b1.SizedRef())
		}
		blobs = append(blobs, b1)
		blobRefs = append(blobRefs, b1.BlobRef())
		blobSizedRefs = append(blobSizedRefs, b1.SizedRef())

		switch len(blobSizedRefs) {
		case 1, 5, 100:
			t.Logf("Testing Enumerate for %d blobs", len(blobSizedRefs))
			testEnumerate(t, sto, blobSizedRefs)
		}
	}
	b1 := blobs[0]

	// finish here if you want to examine the test directory
	//t.Fatalf("FINISH")

	t.Logf("Testing FetchStreaming")
	for i, b2 := range blobs {
		rc, size, err := sto.FetchStreaming(b2.BlobRef())
		if err != nil {
			t.Fatalf("error fetching %d. %s: %v", i, b2, err)
		}
		defer rc.Close()
		testSizedBlob(t, rc, b2.BlobRef(), size)
	}

	if fetcher, ok := sto.(fetcher); ok {
		rsc, size, err := fetcher.Fetch(b1.BlobRef())
		if err != nil {
			t.Fatalf("error fetching %s: %v", b1, err)
		}
		defer rsc.Close()
		n, err := rsc.Seek(0, 0)
		if err != nil {
			t.Fatalf("error seeking in %s: %v", rsc, err)
		}
		if n != 0 {
			t.Fatalf("after seeking to 0, we are at %d!", n)
		}
		testSizedBlob(t, rsc, b1.BlobRef(), size)
	}

	t.Logf("Testing Stat")
	dest := make(chan blob.SizedRef)
	go func() {
		if err := sto.StatBlobs(dest, blobRefs); err != nil {
			t.Fatalf("error stating blobs %s: %v", blobRefs, err)
		}
	}()
	testStat(t, dest, blobSizedRefs)

	// Enumerate tests.
	sort.Sort(blob.SizedByRef(blobSizedRefs))

	t.Logf("Testing Enumerate on all")
	testEnumerate(t, sto, blobSizedRefs)

	t.Logf("Testing Enumerate 'limit' param")
	testEnumerate(t, sto, blobSizedRefs[:3], 3)

	// Enumerate 'after'
	{
		after := blobSizedRefs[2].Ref.String()
		t.Logf("Testing Enumerate 'after' param; after %q", after)
		testEnumerate(t, sto, blobSizedRefs[3:], after)
	}

	// Enumerate 'after' + limit
	{
		after := blobSizedRefs[2].Ref.String()
		t.Logf("Testing Enumerate 'after' + 'limit' param; after %q, limit 1", after)
		testEnumerate(t, sto, blobSizedRefs[3:4], after, 1)
	}

	t.Logf("Testing Remove")
	if err := sto.RemoveBlobs(blobRefs); err != nil {
		if strings.Index(err.Error(), "not implemented") >= 0 {
			t.Logf("RemoveBlob %s: %v", b1, err)
		} else {
			t.Fatalf("RemoveBlob %s: %v", b1, err)
		}
	}
}

type fetcher interface {
	Fetch(blob blob.Ref) (types.ReadSeekCloser, int64, error)
}

func testSizedBlob(t *testing.T, r io.Reader, b1 blob.Ref, size int64) {
	h := b1.Hash()
	n, err := io.Copy(h, r)
	if err != nil {
		t.Fatalf("error reading from %s: %v", r, err)
	}
	if n != size {
		t.Fatalf("read %d bytes from %s, metadata said %d!", n, size)
	}
	b2 := blob.RefFromHash(h)
	if b2 != b1 {
		t.Fatalf("content mismatch (awaited %s, got %s)", b1, b2)
	}
}

func testEnumerate(t *testing.T, sto blobserver.Storage, wantUnsorted []blob.SizedRef, opts ...interface{}) {
	var after string
	var n = 1000
	for _, opt := range opts {
		switch v := opt.(type) {
		case string:
			after = v
		case int:
			n = v
		default:
			panic("bad option of type " + fmt.Sprint("%T", v))
		}
	}

	want := append([]blob.SizedRef(nil), wantUnsorted...)
	sort.Sort(blob.SizedByRef(want))

	sbc := make(chan blob.SizedRef, 10)

	var got []blob.SizedRef
	var grp syncutil.Group
	sawEnd := make(chan bool, 1)
	grp.Go(func() error {
		if err := sto.EnumerateBlobs(context.New(), sbc, after, n); err != nil {
			return fmt.Errorf("EnumerateBlobs(%q, %d): %v", after, n)
		}
		return nil
	})
	grp.Go(func() error {
		for sb := range sbc {
			if !sb.Valid() {
				return fmt.Errorf("invalid blobref %#v received in enumerate", sb)
			}
			got = append(got, sb)
		}
		sawEnd <- true
		return nil

	})
	grp.Go(func() error {
		select {
		case <-sawEnd:
			return nil
		case <-time.After(10 * time.Second):
			return errors.New("timeout waiting for EnumerateBlobs to close its channel")
		}

	})
	if err := grp.Err(); err != nil {
		t.Fatalf("Enumerate error: %v", err)
		return
	}
	if len(got) == 0 && len(want) == 0 {
		return
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Enumerate mismatch. Got %d; want %d.\n Got: %v\nWant: %v\n",
			len(got), len(want), got, want)
	}
}

func testStat(t *testing.T, enum <-chan blob.SizedRef, want []blob.SizedRef) {
	// blobs may arrive in ANY order
	m := make(map[string]int, len(want))
	for i, sb := range want {
		m[sb.Ref.String()] = i
	}

	i := 0
	for sb := range enum {
		if !sb.Valid() {
			break
		}
		wanted := want[m[sb.Ref.String()]]
		if wanted.Size != sb.Size {
			t.Fatalf("received blob size is %d, wanted %d for &%d", sb.Size, wanted.Size, i)
		}
		if wanted.Ref != sb.Ref {
			t.Fatalf("received blob ref mismatch &%d: wanted %s, got %s", i, sb.Ref, wanted.Ref)
		}
		i++
		if i >= len(want) {
			break
		}
	}
}
