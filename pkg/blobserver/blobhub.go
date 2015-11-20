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

	"go4.org/syncutil"
)

type BlobHub interface {
	// TODO(bradfitz): no need for this to be an interface anymore. make GetHub return
	// a concrete type instead? also, maybe remove all the async listener stuff, or
	// audit its users and remove anything that is now unnecessary.

	// NotifyBlobReceived notes that a storage target has successfully received
	// a blob and asynchronously notifies registered listeners.
	//
	// If any synchronous receive hooks are registered, they're run before
	// NotifyBlobReceived returns and their error is returned.
	NotifyBlobReceived(blob.SizedRef) error

	// AddReceiveHook adds a hook that is synchronously run
	// whenever blobs are received.  All registered hooks are run
	// on each blob upload but if more than one returns an error,
	// NotifyBlobReceived will only return one of the errors.
	AddReceiveHook(func(blob.SizedRef) error)

	RegisterListener(ch chan<- blob.Ref)
	UnregisterListener(ch chan<- blob.Ref)

	RegisterBlobListener(blob blob.Ref, ch chan<- blob.Ref)
	UnregisterBlobListener(blob blob.Ref, ch chan<- blob.Ref)
}

var (
	hubmu  sync.RWMutex
	stohub = map[interface{}]BlobHub{} // Storage -> hub
)

// GetHub return a BlobHub for the given storage implementation.
func GetHub(storage interface{}) BlobHub {
	if h, ok := getHub(storage); ok {
		return h
	}
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

func getHub(storage interface{}) (BlobHub, bool) {
	hubmu.RLock()
	defer hubmu.RUnlock()
	h, ok := stohub[storage]
	return h, ok
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
	mu sync.RWMutex

	hooks         []func(blob.SizedRef) error
	listeners     map[chan<- blob.Ref]bool
	blobListeners map[blob.Ref]map[chan<- blob.Ref]bool
}

func (h *memHub) NotifyBlobReceived(sb blob.SizedRef) error {
	h.mu.RLock()
	defer h.mu.RUnlock()

	br := sb.Ref

	// Synchronous hooks. If error, prevents notifying other
	// subscribers.
	var grp syncutil.Group
	for i := range h.hooks {
		hook := h.hooks[i]
		grp.Go(func() error { return hook(sb) })
	}
	if err := grp.Err(); err != nil {
		return err
	}

	// Global listeners
	for ch := range h.listeners {
		ch := ch
		go func() { ch <- br }()
	}

	// Blob-specific listeners
	for ch := range h.blobListeners[br] {
		ch := ch
		go func() { ch <- br }()
	}
	return nil
}

func (h *memHub) RegisterListener(ch chan<- blob.Ref) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.listeners == nil {
		h.listeners = make(map[chan<- blob.Ref]bool)
	}
	h.listeners[ch] = true
}

func (h *memHub) UnregisterListener(ch chan<- blob.Ref) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.listeners == nil {
		panic("blobhub: UnregisterListener called without RegisterListener")
	}
	delete(h.listeners, ch)
}

func (h *memHub) RegisterBlobListener(br blob.Ref, ch chan<- blob.Ref) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.blobListeners == nil {
		h.blobListeners = make(map[blob.Ref]map[chan<- blob.Ref]bool)
	}
	_, ok := h.blobListeners[br]
	if !ok {
		h.blobListeners[br] = make(map[chan<- blob.Ref]bool)
	}
	h.blobListeners[br][ch] = true
}

func (h *memHub) UnregisterBlobListener(br blob.Ref, ch chan<- blob.Ref) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.blobListeners == nil {
		panic("blobhub: UnregisterBlobListener called without RegisterBlobListener")
	}
	set, ok := h.blobListeners[br]
	if !ok {
		panic("blobhub: UnregisterBlobListener called without RegisterBlobListener for " + br.String())
	}
	delete(set, ch)
	if len(set) == 0 {
		delete(h.blobListeners, br)
	}
}

func (h *memHub) AddReceiveHook(hook func(blob.SizedRef) error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.hooks = append(h.hooks, hook)
}
