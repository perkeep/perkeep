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
	"testing"
	"time"

	"camlistore.org/pkg/blob"
	. "camlistore.org/pkg/test/asserts"
)

func TestHubRegistration(t *testing.T) {
	hub := &memHub{}
	ch := make(chan blob.Ref)
	ch2 := make(chan blob.Ref)
	b1 := blob.MustParse("sha1-0beec7b5ea3f0fdbc95d0dd47f3c5bc275da8a33")
	b2 := blob.MustParse("sha1-62cdb7020ff920e5aa642c3d4066950dd1f01f4d")

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
	Expect(t, hub.blobListeners[b1] != nil, "b1 in hub.blobListeners map")
	ExpectInt(t, 1, len(hub.blobListeners[b1]), "hub.blobListeners[b1] size")
	ExpectInt(t, 1, len(hub.blobListeners), "hub.blobListeners size")

	hub.RegisterBlobListener(b2, ch)
	ExpectInt(t, 1, len(hub.blobListeners[b2]), "hub.blobListeners[b1] size")
	ExpectInt(t, 2, len(hub.blobListeners), "hub.blobListeners size")

	hub.UnregisterBlobListener(b2, ch)
	Expect(t, hub.blobListeners[b2] == nil, "b2 not in hub.blobListeners")
	ExpectInt(t, 1, len(hub.blobListeners), "hub.blobListeners size")

	hub.UnregisterBlobListener(b1, ch)
	Expect(t, hub.blobListeners[b1] == nil, "b1 not in hub.blobListeners")
	ExpectInt(t, 0, len(hub.blobListeners), "hub.blobListeners size")
}

func TestHubFiring(t *testing.T) {
	hub := &memHub{}
	ch := make(chan blob.Ref)
	bch := make(chan blob.Ref)
	blob1 := blob.MustParse("sha1-0beec7b5ea3f0fdbc95d0dd47f3c5bc275da8a33")
	blobsame := blob.MustParse("sha1-0beec7b5ea3f0fdbc95d0dd47f3c5bc275da8a33")

	hub.NotifyBlobReceived(blob.SizedRef{blob1, 123}) // no-op

	hub.RegisterListener(ch)
	hub.RegisterBlobListener(blob1, bch)

	hub.NotifyBlobReceived(blob.SizedRef{blobsame, 456})

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
