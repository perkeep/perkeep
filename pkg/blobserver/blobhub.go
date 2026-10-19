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
	"sync"
	"time"

	"perkeep.org/pkg/blob"
)

type BlobHub interface {
	// TODO(bradfitz): no need for this to be an interface anymore. make GetHub return
	// a concrete type instead? also, maybe remove all the async listener stuff, or
	// audit its users and remove anything that is now unnecessary.

	// NotifyBlobsReceived notes that a storage target has successfully
	// received blobs, runs the synchronous receive hooks, and then
	// asynchronously notifies registered listeners.
	//
	// It runs each receive hook once for all of the blobs: a hook added
	// with AddReceiveBlobsHook or AddReceiveContentsHook is called with all
	// of them, and one added with AddReceiveHook is called for each blob in
	// turn. ctx is passed to the hooks added with AddReceiveContentsHook.
	// It returns nil, or a *BlobsError listing the blobs whose hooks
	// failed. Listeners are only notified of the other blobs.
	NotifyBlobsReceived(ctx context.Context, blobs []*blob.Blob) error

	// AddReceiveHook adds a hook that is synchronously run
	// whenever blobs are received.  All registered hooks are run
	// on each blob upload but if more than one returns an error for a
	// blob, NotifyBlobsReceived will only return one of the errors.
	AddReceiveHook(func(blob.SizedRef) error)

	// AddReceiveBlobsHook is like AddReceiveHook, but its hook is called
	// once for all the blobs passed to NotifyBlobsReceived, so it can share
	// work among them. The
	// hook returns an error as described by BlobsReceiver: nil, a
	// *BlobsError listing the blobs it failed for, or another error if it
	// failed for all of them.
	AddReceiveBlobsHook(func([]blob.SizedRef) error)

	// AddReceiveContentsHook is like AddReceiveBlobsHook, but its hook is
	// called with the blobs themselves, to read their contents, and with
	// the context of the receive. While a storage's hub has such a hook,
	// Receive keeps each blob it receives into the storage in memory for
	// it, rather than the hook having to fetch it back.
	AddReceiveContentsHook(func(context.Context, []*blob.Blob) error)

	RegisterListener(ch chan<- blob.Ref)
	UnregisterListener(ch chan<- blob.Ref)

	RegisterBlobListener(blob blob.Ref, ch chan<- blob.Ref)
	UnregisterBlobListener(blob blob.Ref, ch chan<- blob.Ref)
}

var (
	hubmu  sync.RWMutex
	stohub = map[any]BlobHub{} // Storage -> hub
)

// GetHub return a BlobHub for the given storage implementation.
func GetHub(storage any) BlobHub {
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

func getHub(storage any) (BlobHub, bool) {
	hubmu.RLock()
	defer hubmu.RUnlock()
	h, ok := stohub[storage]
	return h, ok
}

// WaitForBlob waits until deadline for blobs to arrive. If blobs is empty, any
// blobs are waited on.  Otherwise, those specific blobs are waited on.
// When WaitForBlob returns, nothing may have happened.
func WaitForBlob(storage any, deadline time.Time, blobs []blob.Ref) {
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
	t := time.NewTimer(time.Until(deadline))
	defer t.Stop()

	select {
	case <-ch:
	case <-t.C:
	}
}

type memHub struct {
	mu sync.RWMutex

	hooks         []func(blob.SizedRef) error
	blobsHooks    []func([]blob.SizedRef) error
	contentsHooks []func(context.Context, []*blob.Blob) error
	listeners     map[chan<- blob.Ref]bool
	blobListeners map[blob.Ref]map[chan<- blob.Ref]bool
}

func (h *memHub) NotifyBlobsReceived(ctx context.Context, blobs []*blob.Blob) error {
	if len(blobs) == 0 {
		return nil
	}
	h.mu.RLock()
	defer h.mu.RUnlock()

	refs := make([]blob.Ref, len(blobs))
	sbs := make([]blob.SizedRef, len(blobs))
	for i, b := range blobs {
		refs[i] = b.Ref()
		sbs[i] = b.SizedRef()
	}

	// Synchronous hooks, each in its own goroutine. A blob whose
	// hooks fail isn't announced to listeners.
	var (
		mu sync.Mutex
		be = &BlobsError{Op: "receive hook"}
		wg sync.WaitGroup
	)
	for _, hook := range h.hooks {
		wg.Go(func() {
			for _, sb := range sbs {
				if err := hook(sb); err != nil {
					mu.Lock()
					be.Add(sb.Ref, err)
					mu.Unlock()
				}
			}
		})
	}
	for _, hook := range h.blobsHooks {
		wg.Go(func() {
			err := hook(sbs)
			mu.Lock()
			be.AddResult(refs, err)
			mu.Unlock()
		})
	}
	for _, hook := range h.contentsHooks {
		wg.Go(func() {
			err := hook(ctx, blobs)
			mu.Lock()
			be.AddResult(refs, err)
			mu.Unlock()
		})
	}
	wg.Wait()

	for _, br := range refs {
		if be.Err(br) != nil {
			continue
		}
		// Global listeners
		for ch := range h.listeners {
			go func() { ch <- br }()
		}
		// Blob-specific listeners
		for ch := range h.blobListeners[br] {
			go func() { ch <- br }()
		}
	}
	return be.OrNil()
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

func (h *memHub) AddReceiveBlobsHook(hook func([]blob.SizedRef) error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.blobsHooks = append(h.blobsHooks, hook)
}

func (h *memHub) AddReceiveContentsHook(hook func(context.Context, []*blob.Blob) error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.contentsHooks = append(h.contentsHooks, hook)
}

// wantsContents reports whether h has hooks added with
// AddReceiveContentsHook.
func (h *memHub) wantsContents() bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.contentsHooks) > 0
}
