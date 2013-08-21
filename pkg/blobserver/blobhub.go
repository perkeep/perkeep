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
	"sync"
	"time"

	"camlistore.org/pkg/blob"
)

type BlobHub interface {
	// For new blobs to notify
	NotifyBlobReceived(blob blob.Ref)

	RegisterListener(ch chan<- blob.Ref)
	UnregisterListener(ch chan<- blob.Ref)

	RegisterBlobListener(blob blob.Ref, ch chan<- blob.Ref)
	UnregisterBlobListener(blob blob.Ref, ch chan<- blob.Ref)
}

var (
	hubmu  sync.Mutex
	stohub = map[interface{}]BlobHub{} // Storage -> hub
)

// GetHub return a BlobHub for the give storage implementation.
func GetHub(storage interface{}) BlobHub {
	hubmu.Lock()
	defer hubmu.Unlock()
	h, ok := stohub[storage]
	if ok {
		return h
	}
	h = new(memHub)
	stohub[storage] = h
	return h
}

// canLongPoll is set to false on App Engine. (multi-process environment...)
var canLongPoll = true

// WaitForBlob waits until deadline for blobs to arrive. If blobs is empty, any
// blobs are waited on.  Otherwise, those specific blobs are waited on.
// When WaitForBlob returns, nothing may have happened.
func WaitForBlob(storage interface{}, deadline time.Time, blobs []blob.Ref) {
	hub := GetHub(storage)
	ch := make(chan blob.Ref, 1)
	if len(blobs) == 0 {
		hub.RegisterListener(ch)
		defer hub.UnregisterListener(ch)
	}
	for _, br := range blobs {
		hub.RegisterBlobListener(br, ch)
		defer hub.UnregisterBlobListener(br, ch)
	}
	var tc <-chan time.Time
	if !canLongPoll {
		tc = time.After(2 * time.Second)
	}
	select {
	case <-ch:
	case <-tc:
	case <-time.After(deadline.Sub(time.Now())):
	}
}

type memHub struct {
	l sync.Mutex

	listeners     map[chan<- blob.Ref]bool
	blobListeners map[string]map[chan<- blob.Ref]bool
}

func (h *memHub) NotifyBlobReceived(br blob.Ref) {
	h.l.Lock()
	defer h.l.Unlock()

	// Callback channels to notify, nil until non-empty
	var notify []chan<- blob.Ref

	// Append global listeners
	for ch, _ := range h.listeners {
		notify = append(notify, ch)
	}

	// Append blob-specific listeners
	if h.blobListeners != nil {
		blobstr := br.String()
		if set, ok := h.blobListeners[blobstr]; ok {
			for ch, _ := range set {
				notify = append(notify, ch)
			}
		}
	}

	if len(notify) > 0 {
		// Run in a separate Goroutine so NotifyBlobReceived doesn't block
		// callers if callbacks are slow.
		go func() {
			for _, ch := range notify {
				ch <- br
			}
		}()
	}
}

func (h *memHub) RegisterListener(ch chan<- blob.Ref) {
	h.l.Lock()
	defer h.l.Unlock()
	if h.listeners == nil {
		h.listeners = make(map[chan<- blob.Ref]bool)
	}
	h.listeners[ch] = true
}

func (h *memHub) UnregisterListener(ch chan<- blob.Ref) {
	h.l.Lock()
	defer h.l.Unlock()
	if h.listeners == nil {
		panic("blobhub: UnregisterListener called without RegisterListener")
	}
	delete(h.listeners, ch)
}

func (h *memHub) RegisterBlobListener(br blob.Ref, ch chan<- blob.Ref) {
	h.l.Lock()
	defer h.l.Unlock()
	if h.blobListeners == nil {
		h.blobListeners = make(map[string]map[chan<- blob.Ref]bool)
	}
	bstr := br.String()
	_, ok := h.blobListeners[bstr]
	if !ok {
		h.blobListeners[bstr] = make(map[chan<- blob.Ref]bool)
	}
	h.blobListeners[bstr][ch] = true
}

func (h *memHub) UnregisterBlobListener(blob blob.Ref, ch chan<- blob.Ref) {
	h.l.Lock()
	defer h.l.Unlock()
	if h.blobListeners == nil {
		panic("blobhub: UnregisterBlobListener called without RegisterBlobListener")
	}
	bstr := blob.String()
	set, ok := h.blobListeners[bstr]
	if !ok {
		panic("blobhub: UnregisterBlobListener called without RegisterBlobListener for " + bstr)
	}
	delete(set, ch)
	if len(set) == 0 {
		delete(h.blobListeners, bstr)
	}
}

type memHubPartitionMap struct {
	hubLock sync.Mutex
	hub     BlobHub
}

func (spm *memHubPartitionMap) GetBlobHub() BlobHub {
	spm.hubLock.Lock()
	defer spm.hubLock.Unlock()
	if spm.hub == nil {
		// TODO: in the future, allow for different blob hub
		// implementations rather than the
		// everything-in-memory-on-a-single-machine memHub.
		spm.hub = new(memHub)
	}
	return spm.hub
}
