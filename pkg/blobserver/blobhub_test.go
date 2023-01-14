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
	"testing"
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

func TestHubFiring(t *testing.T) {
	hub := &memHub{}
	ch := make(chan blob.Ref)
	bch := make(chan blob.Ref)
	blob1 := blob.MustParse("sha1-0beec7b5ea3f0fdbc95d0dd47f3c5bc275da8a33")
	blobsame := blob.MustParse("sha1-0beec7b5ea3f0fdbc95d0dd47f3c5bc275da8a33")
	ctx := context.Background()

	hub.NotifyBlobReceived(ctx, blob.SizedRef{Ref: blob1, Size: 123}) // no-op

	hub.RegisterListener(ch)
	hub.RegisterBlobListener(blob1, bch)

	hub.NotifyBlobReceived(ctx, blob.SizedRef{Ref: blobsame, Size: 456})

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
