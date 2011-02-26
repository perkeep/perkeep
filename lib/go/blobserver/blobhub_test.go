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

package blobserver

import (
	"camli/blobref"
	"testing"
	"time"
)

func Expect(t *testing.T, got bool, what string) {
	if !got {
		t.Errorf("%s: got %v; expected %v", what, got, true)
	}
}

func ExpectBool(t *testing.T, expect, got bool, what string) {
	if expect != got {
		t.Errorf("%s: got %v; expected %v", what, got, expect)
	}
}

func ExpectInt(t *testing.T, expect, got int, what string) {
	if expect != got {
		t.Errorf("%s: got %d; expected %d", what, got, expect)
	}
}

func TestHubRegistration(t *testing.T) {
	hub := &SimpleBlobHub{}
	ch := make(chan *blobref.BlobRef)
	ch2 := make(chan *blobref.BlobRef)
	b1 := blobref.Parse("sha1-0beec7b5ea3f0fdbc95d0dd47f3c5bc275da8a33")
	b2 := blobref.Parse("sha1-62cdb7020ff920e5aa642c3d4066950dd1f01f4d")

	Expect(t, hub.listeners == nil, "hub.listeners is nil before RegisterListener")

	hub.RegisterListener(ch)
	ExpectInt(t, 1, len(hub.listeners), "len(hub.listeners) after RegisterListener")

	hub.RegisterListener(ch2)
	ExpectInt(t, 2, len(hub.listeners), "len(hub.listeners) after ch2 RegisterListener")

	hub.UnregisterListener(ch)
	ExpectInt(t, 1, len(hub.listeners), "len(hub.listeners) after UnregisterListener")

	hub.UnregisterListener(ch2)
	ExpectInt(t, 0, len(hub.listeners), "len(hub.listeners) after UnregisterListener")

	Expect(t, hub.blobListeners == nil, "hub.blobListeners is nil before RegisterBlobListener")

	hub.RegisterBlobListener(b1, ch)
	Expect(t, hub.blobListeners != nil, "hub.blobListeners is not nil before RegisterBlobListener")
	Expect(t, hub.blobListeners[b1.String()] != nil, "b1 in hub.blobListeners map")
	ExpectInt(t, 1, len(hub.blobListeners[b1.String()]), "hub.blobListeners[b1] size")
	ExpectInt(t, 1, len(hub.blobListeners), "hub.blobListeners size")

	hub.RegisterBlobListener(b2, ch)
	ExpectInt(t, 1, len(hub.blobListeners[b2.String()]), "hub.blobListeners[b1] size")
	ExpectInt(t, 2, len(hub.blobListeners), "hub.blobListeners size")

	hub.UnregisterBlobListener(b2, ch)
	Expect(t, hub.blobListeners[b2.String()] == nil, "b2 not in hub.blobListeners")
	ExpectInt(t, 1, len(hub.blobListeners), "hub.blobListeners size")

	hub.UnregisterBlobListener(b1, ch)
	Expect(t, hub.blobListeners[b1.String()] == nil, "b1 not in hub.blobListeners")
	ExpectInt(t, 0, len(hub.blobListeners), "hub.blobListeners size")
}

func TestHubFiring(t *testing.T) {
	hub := &SimpleBlobHub{}
	ch := make(chan *blobref.BlobRef)
	bch := make(chan *blobref.BlobRef)
	blob := blobref.Parse("sha1-0beec7b5ea3f0fdbc95d0dd47f3c5bc275da8a33")
	blobsame := blobref.Parse("sha1-0beec7b5ea3f0fdbc95d0dd47f3c5bc275da8a33")

	hub.NotifyBlobReceived(blob) // no-op

	hub.RegisterListener(ch)
	hub.RegisterBlobListener(blob, bch)

	hub.NotifyBlobReceived(blobsame)

	tmr1 := time.NewTimer(1e9)
	select {
	case <-tmr1.C:
		t.Fatal("timer expired on receiving from ch")
	case got := <-ch:
		if !blob.Equals(got) {
			t.Fatalf("got wrong blob")
		}
	}

	select {
	case <-tmr1.C:
		t.Fatal("timer expired on receiving from bch")
	case got := <-bch:
		if !blob.Equals(got) {
			t.Fatalf("got wrong blob")
		}
	}
	tmr1.Stop()
}
