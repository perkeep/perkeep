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

	// A plain error is every blob's.
	errAll := errors.New("storage closed")
	sto.result = func([]*blob.Blob) error { return errAll }
	err = blobserver.ReceiveBlobs(ctxbg, sto, []*blob.Blob{a.Blob(), c.Blob()})
	for _, br := range []blob.Ref{a.BlobRef(), c.BlobRef()} {
		if got := blobserver.BlobErr(err, br); got != errAll {
			t.Errorf("BlobErr(%v) = %v; want %v", br, got, errAll)
		}
	}
}
