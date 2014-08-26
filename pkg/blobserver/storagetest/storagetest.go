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
	"io/ioutil"
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
)

type Opts struct {
	// New is required and must return the storage server to test, along with a func to
	// clean it up. The cleanup may be nil.
	New func(*testing.T) (sto blobserver.Storage, cleanup func())

	// Retries specifies how long to wait to retry after each failure
	// that may be an eventual consistency issue (enumerate, stat), etc.
	Retries []time.Duration
}

func Test(t *testing.T, fn func(*testing.T) (sto blobserver.Storage, cleanup func())) {
	TestOpt(t, Opts{New: fn})
}

type run struct {
	t   *testing.T
	opt Opts
	sto blobserver.Storage
}

func TestOpt(t *testing.T, opt Opts) {
	sto, cleanup := opt.New(t)
	defer func() {
		if t.Failed() {
			t.Logf("test %T FAILED, skipping cleanup!", sto)
		} else {
			if cleanup != nil {
				cleanup()
			}
		}
	}()
	r := &run{
		t:   t,
		opt: opt,
		sto: sto,
	}
	t.Logf("Testing blobserver storage %T", sto)

	t.Logf("Testing Enumerate for empty")
	r.testEnumerate(nil)

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
	for i, x := range contents {
		b1 := &test.Blob{x}
		if testing.Short() {
			t.Logf("blob[%d] = %s: %q", i, b1.BlobRef(), x)
		}
		b1s, err := sto.ReceiveBlob(b1.BlobRef(), b1.Reader())
		if err != nil {
			t.Fatalf("ReceiveBlob of %s: %v", b1, err)
		}
		if b1s != b1.SizedRef() {
			t.Fatalf("Received %v; want %v", b1s, b1.SizedRef())
		}
		blobs = append(blobs, b1)
		blobRefs = append(blobRefs, b1.BlobRef())
		blobSizedRefs = append(blobSizedRefs, b1.SizedRef())

		switch len(blobSizedRefs) {
		case 1, 5, 100:
			t.Logf("Testing Enumerate for %d blobs", len(blobSizedRefs))
			r.testEnumerate(blobSizedRefs)
		}
	}
	b1 := blobs[0]

	t.Logf("Testing Fetch")
	for i, b2 := range blobs {
		rc, size, err := sto.Fetch(b2.BlobRef())
		if err != nil {
			t.Fatalf("error fetching %d. %s: %v", i, b2, err)
		}
		defer rc.Close()
		testSizedBlob(t, rc, b2.BlobRef(), int64(size))
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
	r.testEnumerate(blobSizedRefs)

	t.Logf("Testing Enumerate 'limit' param")
	r.testEnumerate(blobSizedRefs[:3], 3)

	// Enumerate 'after'
	{
		after := blobSizedRefs[2].Ref.String()
		t.Logf("Testing Enumerate 'after' param; after %q", after)
		r.testEnumerate(blobSizedRefs[3:], after)
	}

	// Enumerate 'after' + limit
	{
		after := blobSizedRefs[2].Ref.String()
		t.Logf("Testing Enumerate 'after' + 'limit' param; after %q, limit 1", after)
		r.testEnumerate(blobSizedRefs[3:4], after, 1)
	}

	// Enumerate 'after' with prefix of a blobref + limit
	{
		after := "a"
		t.Logf("Testing Enumerate 'after' + 'limit' param; after %q, limit 1", after)
		r.testEnumerate(blobSizedRefs[:1], after, 1)
	}

	t.Logf("Testing Remove")
	if err := sto.RemoveBlobs(blobRefs); err != nil {
		if strings.Contains(err.Error(), "not implemented") {
			t.Logf("RemoveBlob %s: %v", b1, err)
		} else {
			t.Fatalf("RemoveBlob %s: %v", b1, err)
		}
	}

	testSubFetcher(t, sto)
}

func testSubFetcher(t *testing.T, sto blobserver.Storage) {
	sf, ok := sto.(blob.SubFetcher)
	if !ok {
		t.Logf("%T is not a SubFetcher", sto)
		return
	}
	t.Logf("Testing SubFetch")
	big := &test.Blob{"Some big blob"}
	if _, err := sto.ReceiveBlob(big.BlobRef(), big.Reader()); err != nil {
		t.Fatal(err)
	}
	regions := []struct {
		off, limit int64
		want       string
		errok      bool
	}{
		{5, 3, "big", false},
		{5, 8, "big blob", false},
		{5, 100, "big blob", true},
	}
	for _, tt := range regions {
		r, err := sf.SubFetch(big.BlobRef(), tt.off, tt.limit)
		if err != nil {
			t.Fatal("Error fetching big blob for SubFetch: %v", err)
		}
		all, err := ioutil.ReadAll(r)
		r.Close()
		if err != nil && !tt.errok {
			t.Errorf("Unexpected error reading SubFetch region %+v: %v", tt, err)
		}
		if string(all) != tt.want {
			t.Errorf("SubFetch region %+v got %q; want %q", tt, all, tt.want)
		}
	}
}

func testSizedBlob(t *testing.T, r io.Reader, b1 blob.Ref, size int64) {
	h := b1.Hash()
	n, err := io.Copy(h, r)
	if err != nil {
		t.Fatalf("error reading from %s: %v", r, err)
	}
	if n != size {
		t.Fatalf("read %d bytes from %s, metadata said %d!", n, r, size)
	}
	b2 := blob.RefFromHash(h)
	if b2 != b1 {
		t.Fatalf("content mismatch (awaited %s, got %s)", b1, b2)
	}
}

func CheckEnumerate(sto blobserver.Storage, wantUnsorted []blob.SizedRef, opts ...interface{}) error {
	var after string
	var n = 1000
	for _, opt := range opts {
		switch v := opt.(type) {
		case string:
			after = v
		case int:
			n = v
		default:
			panic("bad option of type " + fmt.Sprintf("%T", v))
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
			return fmt.Errorf("EnumerateBlobs(%q, %d): %v", after, n, err)
		}
		return nil
	})
	grp.Go(func() error {
		var lastRef blob.Ref
		for sb := range sbc {
			if !sb.Valid() {
				return fmt.Errorf("invalid blobref %#v received in enumerate", sb)
			}
			got = append(got, sb)
			if lastRef.Valid() && sb.Ref.Less(lastRef) {
				return fmt.Errorf("blobs appearing out of order")
			}
			lastRef = sb.Ref
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
		return fmt.Errorf("Enumerate error: %v", err)
	}
	if len(got) == 0 && len(want) == 0 {
		return nil
	}
	var gotSet = map[blob.SizedRef]bool{}
	for _, sb := range got {
		if gotSet[sb] {
			return fmt.Errorf("duplicate blob %v returned in enumerate", sb)
		}
		gotSet[sb] = true
	}

	if !reflect.DeepEqual(got, want) {
		return fmt.Errorf("Enumerate mismatch. Got %d; want %d.\n Got: %v\nWant: %v\n",
			len(got), len(want), got, want)
	}
	return nil
}

func (r *run) testEnumerate(wantUnsorted []blob.SizedRef, opts ...interface{}) {
	if err := r.withRetries(func() error {
		return CheckEnumerate(r.sto, wantUnsorted, opts...)
	}); err != nil {
		r.t.Fatalf("%v", err)
	}
}

func (r *run) withRetries(fn func() error) error {
	delays := r.opt.Retries
	for {
		err := fn()
		if err == nil || len(delays) == 0 {
			return err
		}
		r.t.Logf("(operation failed; retrying after %v)", delays[0])
		time.Sleep(delays[0])
		delays = delays[1:]
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
