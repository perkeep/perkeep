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

package blobserver

import (
	"context"
	"errors"
	"io"
	"slices"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"perkeep.org/pkg/blob"
)

func TestHubRegistration(t *testing.T) {
	hub := &memHub{}
	ch := make(chan blob.Ref)
	ch2 := make(chan blob.Ref)
	b1 := blob.MustParse("sha1-0beec7b5ea3f0fdbc95d0dd47f3c5bc275da8a33")
	b2 := blob.MustParse("sha1-62cdb7020ff920e5aa642c3d4066950dd1f01f4d")

	if hub.listeners != nil {
		t.Fatalf("hub.listeners is not nil before RegisterListener")
	}

	hub.RegisterListener(ch)
	if want := 1; len(hub.listeners) != want {
		t.Fatalf("len(hub.listeners) after RegisterListener: got: %d, expected: %d", len(hub.listeners), want)
	}

	hub.RegisterListener(ch2)
	if want := 2; len(hub.listeners) != want {
		t.Fatalf("len(hub.listeners) after ch2 RegisterListener: got: %d, expected: %d", len(hub.listeners), want)
	}

	hub.UnregisterListener(ch)
	if want := 1; len(hub.listeners) != want {
		t.Fatalf("len(hub.listeners) after UnregisterListener: got: %d, expected: %d", len(hub.listeners), want)
	}

	hub.UnregisterListener(ch2)
	if want := 0; len(hub.listeners) != want {
		t.Fatalf("len(hub.listeners) after UnregisterListener: got: %d, expected: %d", len(hub.listeners), want)
	}

	if hub.blobListeners != nil {
		t.Fatalf("hub.blobListeners is not nil before RegisterBlobListener")
	}

	hub.RegisterBlobListener(b1, ch)
	if hub.blobListeners == nil {
		t.Fatal("hub.blobListeners is nil after RegisterBlobListener")
	}
	if hub.blobListeners[b1] == nil {
		t.Fatal("b1 in hub.blobListeners map is nil")
	}
	if want := 1; len(hub.blobListeners[b1]) != want {
		t.Fatalf("hub.blobListeners[b1] size: got: %d, expected: %d", len(hub.blobListeners[b1]), want)
	}
	if want := 1; len(hub.blobListeners) != want {
		t.Fatalf("hub.blobListeners size: got: %d, expected: %d", len(hub.listeners), want)
	}

	hub.RegisterBlobListener(b2, ch)
	if want := 1; len(hub.blobListeners[b2]) != want {
		t.Fatalf("hub.blobListeners[b2] size: got: %d, expected: %d", len(hub.blobListeners[b2]), want)
	}
	if want := 2; len(hub.blobListeners) != want {
		t.Fatalf("hub.blobListeners size: got: %d, expected: %d", len(hub.listeners), want)
	}

	hub.UnregisterBlobListener(b2, ch)
	if hub.blobListeners[b2] != nil {
		t.Fatalf("b2 in hub.blobListeners map is not nil")
	}
	if want := 1; len(hub.blobListeners) != want {
		t.Fatalf("hub.blobListeners size: got: %d, expected: %d", len(hub.blobListeners), want)
	}

	hub.UnregisterBlobListener(b1, ch)
	if hub.blobListeners[b1] != nil {
		t.Fatalf("b1 in hub.blobListeners map is not nil")
	}
	if want := 0; len(hub.blobListeners) != want {
		t.Fatalf("hub.blobListeners size: got: %d, expected: %d", len(hub.blobListeners), want)
	}
}

func TestHubNotifyBlobs(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		hub := &memHub{}
		blobs := []*blob.Blob{memBlob("a"), memBlob("b"), memBlob("c")}
		var sbs []blob.SizedRef
		for _, b := range blobs {
			sbs = append(sbs, b.SizedRef())
		}
		errB, errC := errors.New("b failed"), errors.New("c failed")

		var mu sync.Mutex
		var perBlob []blob.Ref
		var groups [][]blob.Ref
		hub.AddReceiveHook(func(sb blob.SizedRef) error {
			mu.Lock()
			defer mu.Unlock()
			perBlob = append(perBlob, sb.Ref)
			if sb.Ref == sbs[1].Ref {
				return errB
			}
			return nil
		})
		hub.AddReceiveBlobsHook(func(got []blob.SizedRef) error {
			var refs []blob.Ref
			be := &BlobsError{}
			for _, sb := range got {
				refs = append(refs, sb.Ref)
				if sb.Ref == sbs[2].Ref {
					be.Add(sb.Ref, errC)
				}
			}
			mu.Lock()
			groups = append(groups, refs)
			mu.Unlock()
			return be.OrNil()
		})
		type ctxKey struct{}
		ctx := context.WithValue(context.Background(), ctxKey{}, "the receive's")
		var contents []string
		hub.AddReceiveContentsHook(func(ctx context.Context, got []*blob.Blob) error {
			if v := ctx.Value(ctxKey{}); v != "the receive's" {
				t.Errorf("contents hook's context value = %v; want the receive's", v)
			}
			for _, b := range got {
				r, err := b.ReadAll(ctx)
				if err != nil {
					return err
				}
				mu.Lock()
				all, _ := io.ReadAll(r) // a bytes.Reader can't fail
				contents = append(contents, string(all))
				mu.Unlock()
			}
			return nil
		})
		if !hub.wantsContents() {
			t.Error("wantsContents = false with a contents hook")
		}
		ch := make(chan blob.Ref, len(sbs))
		hub.RegisterListener(ch)

		err := hub.NotifyBlobsReceived(ctx, blobs)
		for i, want := range []error{nil, errB, errC} {
			if got := BlobErr(err, sbs[i].Ref); got != want {
				t.Errorf("BlobErr(blob %d) = %v; want %v", i, got, want)
			}
		}
		refs := []blob.Ref{sbs[0].Ref, sbs[1].Ref, sbs[2].Ref}
		if !slices.Equal(perBlob, refs) {
			t.Errorf("per-blob hook called with %v; want %v", perBlob, refs)
		}
		if want := [][]blob.Ref{refs}; !slices.EqualFunc(groups, want, slices.Equal) {
			t.Errorf("group hook called with %v; want %v", groups, want)
		}
		if want := []string{"a", "b", "c"}; !slices.Equal(contents, want) {
			t.Errorf("contents hook read %q; want %q", contents, want)
		}
		// Only the blob whose hooks succeeded is announced.
		synctest.Wait()
		if len(ch) != 1 || <-ch != sbs[0].Ref {
			t.Errorf("listener notified of %d blobs; want only %v", len(ch)+1, sbs[0].Ref)
		}

		// BlobErr gets a hook's error for its blob as is.
		if err := BlobErr(hub.NotifyBlobsReceived(ctx, blobs[1:2]), sbs[1].Ref); err != errB {
			t.Errorf("BlobErr(NotifyBlobsReceived(blob 1)) = %v; want %v", err, errB)
		}
	})
}

func TestHubFiring(t *testing.T) {
	hub := &memHub{}
	ch := make(chan blob.Ref)
	bch := make(chan blob.Ref)
	blob1 := blob.MustParse("sha1-0beec7b5ea3f0fdbc95d0dd47f3c5bc275da8a33")
	blobsame := blob.MustParse("sha1-0beec7b5ea3f0fdbc95d0dd47f3c5bc275da8a33")

	ctx := context.Background()
	hub.NotifyBlobsReceived(ctx, []*blob.Blob{blob.NewBlob(blob1, 123, nil)}) // no-op

	hub.RegisterListener(ch)
	hub.RegisterBlobListener(blob1, bch)

	hub.NotifyBlobsReceived(ctx, []*blob.Blob{blob.NewBlob(blobsame, 456, nil)})

	tmr1 := time.NewTimer(1 * time.Second)
	select {
	case <-tmr1.C:
		t.Fatal("timer expired on receiving from ch")
	case got := <-ch:
		if got != blob1 {
			t.Fatalf("got wrong blob")
		}
	}

	select {
	case <-tmr1.C:
		t.Fatal("timer expired on receiving from bch")
	case got := <-bch:
		if got != blob1 {
			t.Fatalf("got wrong blob")
		}
	}
	tmr1.Stop()
}

// memBlob returns a blob of s, in memory.
func memBlob(s string) *blob.Blob {
	return blob.NewBlob(blob.RefFromString(s), uint32(len(s)), func(context.Context) ([]byte, error) {
		return []byte(s), nil
	})
}
