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
	"sync"
)

type BlobHub interface {
	// For new blobs to notify
	NotifyBlobReceived(blob *blobref.BlobRef)

	RegisterListener(ch chan *blobref.BlobRef)
	UnregisterListener(ch chan *blobref.BlobRef)

	RegisterBlobListener(blob *blobref.BlobRef, ch chan *blobref.BlobRef)
	UnregisterBlobListener(blob *blobref.BlobRef, ch chan *blobref.BlobRef)
}

type SimpleBlobHub struct {
	l sync.Mutex

	listeners     map[chan *blobref.BlobRef]bool
	blobListeners map[string]map[chan *blobref.BlobRef]bool
}

func (h *SimpleBlobHub) NotifyBlobReceived(blob *blobref.BlobRef) {
	h.l.Lock()
	defer h.l.Unlock()

	// Callback channels to notify, nil until non-empty
	var notify []chan *blobref.BlobRef

	// Append global listeners
	for ch, _ := range h.listeners {
		notify = append(notify, ch)
	}

	// Append blob-specific listeners
	if h.blobListeners != nil {
		blobstr := blob.String()
		if set, ok := h.blobListeners[blobstr]; ok {
			for ch, _ := range set {
				notify = append(notify, ch)
			}
		}
	}

	// Run in a separate Goroutine so NotifyBlobReceived doesn't block
	// callers if callbacks are slow.
	go func() {
		for _, ch := range notify {
			ch <- blob
		}
	}()
}

func (h *SimpleBlobHub) RegisterListener(ch chan *blobref.BlobRef) {
	h.l.Lock()
	defer h.l.Unlock()
	if h.listeners == nil {
		h.listeners = make(map[chan *blobref.BlobRef]bool)
	}
	h.listeners[ch] = true
}

func (h *SimpleBlobHub) UnregisterListener(ch chan *blobref.BlobRef) {
	h.l.Lock()
	defer h.l.Unlock()
	if h.listeners == nil {
		panic("blobhub: UnregisterListener called without RegisterListener")
	}
	h.listeners[ch] = false, false
}

func (h *SimpleBlobHub) RegisterBlobListener(blob *blobref.BlobRef, ch chan *blobref.BlobRef) {
	h.l.Lock()
	defer h.l.Unlock()
	if h.blobListeners == nil {
		h.blobListeners = make(map[string]map[chan *blobref.BlobRef]bool)
	}
	bstr := blob.String()
	_, ok := h.blobListeners[bstr]
	if !ok {
		h.blobListeners[bstr] = make(map[chan *blobref.BlobRef]bool)
	}
	h.blobListeners[bstr][ch] = true
}

func (h *SimpleBlobHub) UnregisterBlobListener(blob *blobref.BlobRef, ch chan *blobref.BlobRef) {
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
	set[ch] = false, false
	if len(set) == 0 {
		h.blobListeners[bstr] = nil, false
	}
}

type SimpleBlobHubPartitionMap struct {
	hubLock sync.Mutex
	hub     BlobHub
}

func (spm *SimpleBlobHubPartitionMap) GetBlobHub() BlobHub {
	spm.hubLock.Lock()
	defer spm.hubLock.Unlock()
	if spm.hub == nil {
		// TODO: in the future, allow for different blob hub
		// implementations rather than the
		// everything-in-memory-on-a-single-machine SimpleBlobHub.
		spm.hub = new(SimpleBlobHub)
	}
	return spm.hub
}
