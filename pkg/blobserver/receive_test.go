/*
Copyright 2013 The Perkeep Authors

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

package blobserver_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver"
	"perkeep.org/pkg/test"
)

var ctxbg = context.Background()

func TestReceive(t *testing.T) {
	sto := new(test.Fetcher)
	data := []byte("some blob")
	br := blob.RefFromBytes(data)

	hub := blobserver.GetHub(sto)
	ch := make(chan blob.Ref, 1)
	hub.RegisterListener(ch)

	sb, err := blobserver.Receive(ctxbg, sto, br, bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if sb.Size != uint32(len(data)) {
		t.Errorf("received blob size = %d; want %d", sb.Size, len(data))
	}
	if sb.Ref != br {
		t.Errorf("received blob = %v; want %v", sb.Ref, br)
	}
	select {
	case got := <-ch:
		if got != br {
			t.Errorf("blobhub notified about %v; want %v", got, br)
		}
	case <-time.After(5 * time.Second):
		t.Error("timeout waiting on blobhub")
	}
}

// receiveOnly is a BlobReceiver that can't be read from.
type receiveOnly struct{ sto *test.Fetcher }

func (r receiveOnly) ReceiveBlob(ctx context.Context, br blob.Ref, src io.Reader) (blob.SizedRef, error) {
	return r.sto.ReceiveBlob(ctx, br, src)
}

// hookOnReceive is storage that adds a contents hook to its hub when it
// receives a blob.
type hookOnReceive struct {
	*test.Fetcher
	hook func(context.Context, []*blob.Blob) error
}

func (s *hookOnReceive) ReceiveBlob(ctx context.Context, br blob.Ref, src io.Reader) (blob.SizedRef, error) {
	blobserver.GetHub(s).AddReceiveContentsHook(s.hook)
	return s.Fetcher.ReceiveBlob(ctx, br, src)
}

// TestReceiveContentsHook checks that Receive gives a contents hook the
// blob it received, from memory, without reading it back from storage, and
// that it reads it back for a hook that's added late.
func TestReceiveContentsHook(t *testing.T) {
	type ctxKey struct{}
	ctx := context.WithValue(ctxbg, ctxKey{}, "the receive's")
	var got []string
	contentsHook := func(ctx context.Context, blobs []*blob.Blob) error {
		if v := ctx.Value(ctxKey{}); v != "the receive's" {
			t.Errorf("hook's context value = %v; want the receive's", v)
		}
		for _, b := range blobs {
			r, err := b.ReadAll(ctx)
			if err != nil {
				return err
			}
			all, _ := io.ReadAll(r) // a bytes.Reader can't fail
			got = append(got, string(all))
		}
		return nil
	}

	dst := receiveOnly{new(test.Fetcher)}
	blobserver.GetHub(dst).AddReceiveContentsHook(contentsHook)
	if _, err := blobserver.ReceiveString(ctx, dst, "kept"); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, []string{"kept"}) {
		t.Errorf("hook read %q; want %q", got, "kept")
	}

	// A storage that can be read from is read from if Receive didn't
	// keep the blob, as when the hook was added mid-receive.
	got = nil
	sto := &hookOnReceive{Fetcher: new(test.Fetcher), hook: contentsHook}
	if _, err := blobserver.ReceiveString(ctx, sto, "read back"); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, []string{"read back"}) {
		t.Errorf("hook read %q; want %q", got, "read back")
	}
}

func TestReceiveCorrupt(t *testing.T) {
	sto := new(test.Fetcher)
	data := []byte("some blob")
	br := blob.RefFromBytes(data)
	data[0] = 'X' // corrupt it
	_, err := blobserver.Receive(ctxbg, sto, br, bytes.NewReader(data))
	if !errors.Is(err, blobserver.ErrCorruptBlob) {
		t.Errorf("Receive = %v; want ErrCorruptBlob", err)
	}
	if len(sto.BlobrefStrings()) > 0 {
		t.Errorf("nothing should be stored. Got %q", sto.BlobrefStrings())
	}
}

// receivedHook records the blobs that dst's hub is notified of.
func receivedHook(dst blobserver.BlobReceiver) func() []blob.Ref {
	var mu sync.Mutex
	var got []blob.Ref
	blobserver.GetHub(dst).AddReceiveHook(func(sb blob.SizedRef) error {
		mu.Lock()
		defer mu.Unlock()
		got = append(got, sb.Ref)
		return nil
	})
	return func() []blob.Ref {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(got)
	}
}

// corruptBlob returns a blob whose contents don't match its ref.
func corruptBlob(s string) *blob.Blob {
	return blob.NewBlob(blob.RefFromString(s), uint32(len(s)), func(context.Context) ([]byte, error) {
		return []byte(strings.ToUpper(s)), nil
	})
}

func refSet(refs []blob.Ref) map[blob.Ref]bool {
	m := map[blob.Ref]bool{}
	for _, br := range refs {
		m[br] = true
	}
	return m
}

func TestReceiveBlobsEach(t *testing.T) {
	sto := new(test.Fetcher)
	notified := receivedHook(sto)
	var blobs []*blob.Blob
	var want []blob.Ref
	for i := range 20 {
		tb := &test.Blob{Contents: fmt.Sprintf("blob %d", i)}
		blobs = append(blobs, tb.Blob())
		want = append(want, tb.BlobRef())
	}
	bad := corruptBlob("bad")
	blobs = append(blobs, bad, blobs[0]) // and a duplicate

	err := blobserver.ReceiveBlobs(ctxbg, sto, blobs)
	be, ok := errors.AsType[*blobserver.BlobsError](err)
	if !ok || be.Len() != 1 || !errors.Is(be.Err(bad.Ref()), blobserver.ErrCorruptBlob) {
		t.Fatalf("ReceiveBlobs = %v; want only %v to fail, with ErrCorruptBlob", err, bad.Ref())
	}
	if got := sto.NumBlobs(); got != len(want) {
		t.Errorf("stored %d blobs; want %d", got, len(want))
	}
	if got := notified(); len(got) != len(want) || !maps.Equal(refSet(got), refSet(want)) {
		t.Errorf("hub notified of %v; want %v", got, want)
	}
}

func TestReceiveBlobsEachFailure(t *testing.T) {
	errFail := errors.New("receive failed")
	sto := &test.Fetcher{ReceiveErr: errFail}
	notified := receivedHook(sto)
	blobs := []*blob.Blob{(&test.Blob{Contents: "a"}).Blob(), (&test.Blob{Contents: "b"}).Blob()}
	err := blobserver.ReceiveBlobs(ctxbg, sto, blobs)
	for _, b := range blobs {
		if got := blobserver.BlobErr(err, b.Ref()); got != errFail {
			t.Errorf("BlobErr(%v) = %v; want %v", b.Ref(), got, errFail)
		}
	}
	if got := notified(); len(got) != 0 {
		t.Errorf("hub notified of %v; want nothing", got)
	}

	ctx, cancel := context.WithCancel(ctxbg)
	cancel()
	err = blobserver.ReceiveBlobs(ctx, new(test.Fetcher), blobs)
	for _, b := range blobs {
		if got := blobserver.BlobErr(err, b.Ref()); !errors.Is(got, context.Canceled) {
			t.Errorf("with canceled ctx, BlobErr(%v) = %v; want context.Canceled", b.Ref(), got)
		}
	}
}

func TestReceiveBlobsHookError(t *testing.T) {
	sto := new(test.Fetcher)
	errHook := errors.New("hook failed")
	a, b := &test.Blob{Contents: "a"}, &test.Blob{Contents: "b"}
	blobserver.GetHub(sto).AddReceiveHook(func(sb blob.SizedRef) error {
		if sb.Ref == a.BlobRef() {
			return errHook
		}
		return nil
	})
	err := blobserver.ReceiveBlobs(ctxbg, sto, []*blob.Blob{a.Blob(), b.Blob()})
	if got := blobserver.BlobErr(err, a.BlobRef()); got != errHook {
		t.Errorf("BlobErr(a) = %v; want %v", got, errHook)
	}
	if got := blobserver.BlobErr(err, b.BlobRef()); got != nil {
		t.Errorf("BlobErr(b) = %v; want nil", got)
	}
}

// TestReceiveBlobsUnchecked checks that ReceiveBlobsUnchecked passes blobs
// on as they are, duplicates included, without notifying the blob hub.
func TestReceiveBlobsUnchecked(t *testing.T) {
	a, b := (&test.Blob{Contents: "a"}).Blob(), (&test.Blob{Contents: "b"}).Blob()
	blobs := []*blob.Blob{a, b, a}
	for _, batch := range []bool{false, true} {
		t.Run(fmt.Sprintf("BlobsReceiver=%v", batch), func(t *testing.T) {
			fetcher := new(test.Fetcher)
			var dst blobserver.BlobReceiver = fetcher
			bs := &blobsStore{Fetcher: fetcher}
			if batch {
				dst = bs
			}
			notified := receivedHook(dst)
			if err := blobserver.ReceiveBlobsUnchecked(ctxbg, dst, blobs); err != nil {
				t.Fatal(err)
			}
			if fetcher.NumBlobs() != 2 {
				t.Errorf("stored %d blobs; want 2", fetcher.NumBlobs())
			}
			if want := [][]blob.Ref{{a.Ref(), b.Ref(), a.Ref()}}; batch && !slices.EqualFunc(bs.calls, want, slices.Equal) {
				t.Errorf("ReceiveBlobs calls = %v; want %v", bs.calls, want)
			}
			if got := notified(); len(got) != 0 {
				t.Errorf("hub notified of %v; want nothing", got)
			}
		})
	}
}

// refRecorder is a BlobsReceiver that only records the refs it's given.
type refRecorder struct {
	blobserver.NoImplStorage
	got []blob.Ref
}

func (r *refRecorder) ReceiveBlobs(ctx context.Context, blobs []*blob.Blob) error {
	for _, b := range blobs {
		r.got = append(r.got, b.Ref())
	}
	return nil
}

// TestReceiveBlobsNoHash checks that ReceiveBlobsNoHash passes blobs on
// without checking their contents, and notifies the blob hub.
func TestReceiveBlobsNoHash(t *testing.T) {
	dst := new(refRecorder)
	notified := receivedHook(dst)
	good, bad := (&test.Blob{Contents: "good"}).Blob(), corruptBlob("bad")
	if err := blobserver.ReceiveBlobsNoHash(ctxbg, dst, []*blob.Blob{good, bad, good}); err != nil {
		t.Fatal(err)
	}
	want := []blob.Ref{good.Ref(), bad.Ref()} // duplicates still merged
	if !slices.Equal(dst.got, want) {
		t.Errorf("dst got %v; want %v", dst.got, want)
	}
	if got := notified(); !maps.Equal(refSet(got), refSet(want)) {
		t.Errorf("hub notified of %v; want %v", got, want)
	}
}

// blobsStore is a BlobsReceiver that records its ReceiveBlobs calls,
// stores the blobs in a test.Fetcher, and then returns result's error.
type blobsStore struct {
	*test.Fetcher
	calls  [][]blob.Ref
	result func(blobs []*blob.Blob) error
}

func (s *blobsStore) ReceiveBlobs(ctx context.Context, blobs []*blob.Blob) error {
	var refs []blob.Ref
	for _, b := range blobs {
		refs = append(refs, b.Ref())
		r, err := b.ReadAll(ctx)
		if err != nil {
			return err
		}
		if _, err := s.Fetcher.ReceiveBlob(ctx, b.Ref(), r); err != nil {
			return err
		}
	}
	s.calls = append(s.calls, refs)
	if s.result == nil {
		return nil
	}
	return s.result(blobs)
}

func TestReceiveBlobsBlobsReceiver(t *testing.T) {
	a, b, c := &test.Blob{Contents: "a"}, &test.Blob{Contents: "b"}, &test.Blob{Contents: "c"}
	bad := corruptBlob("bad")
	errB := errors.New("b failed")
	sto := &blobsStore{
		Fetcher: new(test.Fetcher),
		result: func(blobs []*blob.Blob) error {
			be := &blobserver.BlobsError{}
			be.Add(b.BlobRef(), errB)
			return be
		},
	}
	notified := receivedHook(sto)
	err := blobserver.ReceiveBlobs(ctxbg, sto, []*blob.Blob{a.Blob(), bad, b.Blob(), a.Blob(), c.Blob()})
	// Only valid blobs reach dst, once each, in one call.
	if want := [][]blob.Ref{{a.BlobRef(), b.BlobRef(), c.BlobRef()}}; !slices.EqualFunc(sto.calls, want, slices.Equal) {
		t.Errorf("ReceiveBlobs calls = %v; want %v", sto.calls, want)
	}
	for _, tt := range []struct {
		br   blob.Ref
		want error
	}{
		{a.BlobRef(), nil},
		{bad.Ref(), blobserver.ErrCorruptBlob},
		{b.BlobRef(), errB},
		{c.BlobRef(), nil},
	} {
		got := blobserver.BlobErr(err, tt.br)
		if (tt.want == nil && got != nil) || !errors.Is(got, tt.want) {
			t.Errorf("BlobErr(%v) = %v; want %v", tt.br, got, tt.want)
		}
	}
	if want := []blob.Ref{a.BlobRef(), c.BlobRef()}; !maps.Equal(refSet(notified()), refSet(want)) {
		t.Errorf("hub notified of %v; want %v", notified(), want)
	}

	// A plain error is every blob's, and returned as is.
	errAll := errors.New("storage closed")
	sto.result = func([]*blob.Blob) error { return errAll }
	err = blobserver.ReceiveBlobs(ctxbg, sto, []*blob.Blob{a.Blob(), c.Blob()})
	if err != errAll {
		t.Errorf("ReceiveBlobs = %v; want %v itself", err, errAll)
	}
	for _, br := range []blob.Ref{a.BlobRef(), c.BlobRef()} {
		if got := blobserver.BlobErr(err, br); got != errAll {
			t.Errorf("BlobErr(%v) = %v; want %v", br, got, errAll)
		}
	}
}
