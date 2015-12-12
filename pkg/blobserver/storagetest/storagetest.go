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
	"camlistore.org/pkg/test"
	"golang.org/x/net/context"

	"go4.org/syncutil"
)

type Opts struct {
	// New is required and must return the storage server to test, along with a func to
	// clean it up. The cleanup may be nil.
	New func(*testing.T) (sto blobserver.Storage, cleanup func())

	// Retries specifies how long to wait to retry after each failure
	// that may be an eventual consistency issue (enumerate, stat), etc.
	Retries []time.Duration

	SkipEnum bool // for when EnumerateBlobs is not implemented
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
	errc := make(chan error, 1)
	go func() {
		errc <- sto.StatBlobs(dest, blobRefs)
	}()
	testStat(t, dest, blobSizedRefs)
	if err := <-errc; err != nil {
		t.Fatalf("error stating blobs %s: %v", blobRefs, err)
	}

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

	r.testRemove(blobRefs)

	r.testSubFetcher()
}

func (r *run) testRemove(blobRefs []blob.Ref) {
	t, sto := r.t, r.sto
	t.Logf("Testing Remove")
	if err := sto.RemoveBlobs(blobRefs); err != nil {
		if strings.Contains(err.Error(), "not implemented") {
			t.Logf("RemoveBlobs: %v", err)
		} else {
			t.Fatalf("RemoveBlobs: %v", err)
		}
	}
	r.testEnumerate(nil) // verify they're all gone
}

func (r *run) testSubFetcher() {
	t, sto := r.t, r.sto
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
			t.Fatalf("Error fetching big blob for SubFetch: %v", err)
		}
		if r == nil {
			t.Fatal("SubFetch returned nil, nil")
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

	// test invalid offsets
	invalids := []struct {
		off, limit int64
	}{
		{int64(len(big.Contents)) + 1, 1},
		{-1, 1},
		{1, -1},
	}
	for _, tt := range invalids {
		r, err := sf.SubFetch(big.BlobRef(), tt.off, tt.limit)
		if err == nil {
			r.Close()
			t.Errorf("No error fetching with off=%d limit=%d; wanted an error", tt.off, tt.limit)
			continue
		}
		if err != blob.ErrNegativeSubFetch && err != blob.ErrOutOfRangeOffsetSubFetch {
			t.Errorf("Unexpected error fetching with off=%d limit=%d: %v", tt.off, tt.limit, err)
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
		ctx, cancel := context.WithCancel(context.TODO())
		defer cancel()
		if err := sto.EnumerateBlobs(ctx, sbc, after, n); err != nil {
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
	if r.opt.SkipEnum {
		r.t.Log("Skipping enum test")
		return
	}
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

type StreamerTestOpt interface {
	verify(got []blob.SizedRef) error
}

// WantN is a wanted condition, that the caller wants N of the items.
type WantN int

func (want WantN) verify(got []blob.SizedRef) error {
	if int(want) != len(got) {
		return fmt.Errorf("got %d streamed blobs; want %d", len(got), int(want))
	}
	return nil
}

type WantSizedRefs []blob.SizedRef

func (s WantSizedRefs) verify(got []blob.SizedRef) error {
	want := []blob.SizedRef(s)
	if !reflect.DeepEqual(got, want) {
		return fmt.Errorf("Mismatch:\n got %d blobs: %q\nwant %d blobs: %q\n", len(got), got, len(want), want)
	}
	return nil
}

// TestStreamer tests that the BlobStreamer bs implements all of the
// promised interface behavior and ultimately yields the provided
// blobs.
//
// If bs also implements BlobEnumerator, the two are compared for
// consistency.
func TestStreamer(t *testing.T, bs blobserver.BlobStreamer, opts ...StreamerTestOpt) {

	var sawEnum map[blob.SizedRef]bool
	if enumer, ok := bs.(blobserver.BlobEnumerator); ok {
		sawEnum = make(map[blob.SizedRef]bool)
		// First do an enumerate over all blobs as a baseline. The Streamer should
		// yield the same blobs, even if it's in a different order.
		enumCtx, cancel := context.WithCancel(context.TODO())
		defer cancel()
		if err := blobserver.EnumerateAll(enumCtx, enumer, func(sb blob.SizedRef) error {
			sawEnum[sb] = true
			return nil
		}); err != nil {
			t.Fatalf("Enumerate: %v", err)
		}
	}

	// See if, without cancelation, it yields the right
	// result and without errors.
	ch := make(chan blobserver.BlobAndToken)
	errCh := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithCancel(context.TODO())
		defer cancel()
		errCh <- bs.StreamBlobs(ctx, ch, "")
	}()
	var gotRefs []blob.SizedRef
	sawStreamed := map[blob.Ref]int{}
	for b := range ch {
		sawStreamed[b.Ref()]++
		sbr := b.SizedRef()
		if sawEnum != nil {
			if _, ok := sawEnum[sbr]; ok {
				delete(sawEnum, sbr)
			} else {
				t.Errorf("Streamer yielded blob not returned by Enumerate: %v", sbr)
			}
		}
		gotRefs = append(gotRefs, sbr)
	}
	if err := <-errCh; err != nil {
		t.Errorf("initial uninterrupted StreamBlobs error: %v", err)
	}
	for br, n := range sawStreamed {
		if n > 1 {
			t.Errorf("Streamed returned duplicate %v, %d times", br, n)
		}
	}
	nMissing := 0
	for sbr := range sawEnum {
		t.Errorf("Enumerate found %v but Streamer didn't return it", sbr)
		nMissing++
		if nMissing == 10 && len(sawEnum) > 10 {
			t.Errorf("... etc ...")
			break
		}
	}
	for _, opt := range opts {
		if err := opt.verify(gotRefs); err != nil {
			t.Errorf("error after first uninterrupted StreamBlobs pass: %v", err)
		}
	}
	if t.Failed() {
		return
	}

	// Next, the "complex pass": test a cancelation at each point,
	// to test that resume works properly.
	//
	// Basic strategy:
	// -- receive 1 blob, note the blobref, cancel.
	// -- start again with that blobref, receive 2, cancel. first should be same,
	//    second should be new. note its blobref.
	// Each iteration should yield 1 new unique blob and all but
	// the first and last will return 2 blobs.
	wantRefs := append([]blob.SizedRef(nil), gotRefs...) // copy
	sawStreamed = map[blob.Ref]int{}
	gotRefs = gotRefs[:0]
	contToken := ""
	for i := 0; i < len(wantRefs); i++ {
		ctx, cancel := context.WithCancel(context.TODO())
		ch := make(chan blobserver.BlobAndToken)
		errc := make(chan error, 1)
		go func() {
			errc <- bs.StreamBlobs(ctx, ch, contToken)
		}()
		nrecv := 0
		nextToken := ""
		for bt := range ch {
			nrecv++
			sbr := bt.Blob.SizedRef()
			isNew := len(gotRefs) == 0 || sbr != gotRefs[len(gotRefs)-1]
			if isNew {
				if sawStreamed[sbr.Ref] > 0 {
					t.Fatalf("In complex pass, returned duplicate blob %v\n\nSo far, before interrupting:\n%v\n\nWant:\n%v", sbr, gotRefs, wantRefs)
				}
				sawStreamed[sbr.Ref]++
				gotRefs = append(gotRefs, sbr)
				nextToken = bt.Token
				cancel()
				break
			} else if i == 0 {
				t.Fatalf("first iteration should receive a new value")
			} else if nrecv == 2 {
				t.Fatalf("at cut point %d of testStream, Streamer received 2 values, both not unique. Looping?", i)
			}
		}
		err := <-errc
		if err != nil && err != context.Canceled {
			t.Fatalf("StreamBlobs on iteration %d (token %q) returned error: %v", i, contToken, err)
		}
		if err == nil {
			break
		}
		contToken = nextToken
	}
	if !reflect.DeepEqual(gotRefs, wantRefs) {
		t.Errorf("Mismatch on complex pass (got %d, want %d):\n got %q\nwant %q\n", len(gotRefs), len(wantRefs), gotRefs, wantRefs)
		wantMap := map[blob.SizedRef]bool{}
		for _, sbr := range wantRefs {
			wantMap[sbr] = true
		}
		for _, sbr := range gotRefs {
			if _, ok := wantMap[sbr]; ok {
				delete(wantMap, sbr)
			} else {
				t.Errorf("got has unwanted: %v", sbr)
			}
		}
		missing := wantMap // found stuff has been deleted
		for sbr := range missing {
			t.Errorf("got is missing: %v", sbr)
		}

		t.FailNow()
	}
}
