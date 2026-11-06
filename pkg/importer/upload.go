/*
Copyright 2026 The Perkeep Authors

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

package importer

import (
	"context"
	"errors"
	"sync"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver"
)

// maxUploadBatch is the most blobs an uploadQueue stores together.
const maxUploadBatch = 256

// An uploadQueue stores the importers' schema blobs, such as their new
// permanodes and claims, many together when many are uploaded at once,
// as by an importer's concurrent imports. A blob uploaded while others are
// being stored waits for them, and is then stored with the others that
// waited, with one blobserver.ReceiveBlobs call, which can sync them to
// disk together, and notifies the storage's hub, and so the index, of them
// together, rather than one at a time. A blob uploaded while none are being
// stored is stored at once.
//
// The zero value is an empty queue.
type uploadQueue struct {
	mu      sync.Mutex
	busy    bool // whether a goroutine is storing batches
	pending []*pendingUpload
}

type pendingUpload struct {
	dst  blobserver.BlobReceiver
	b    *blob.Blob
	done chan error // of the blob's upload, buffered
}

// upload stores b in dst, with others uploaded at the same time, and
// returns when it's stored, or ctx is done.
func (q *uploadQueue) upload(ctx context.Context, dst blobserver.BlobReceiver, b *blob.Blob) error {
	p := &pendingUpload{dst: dst, b: b, done: make(chan error, 1)}
	q.mu.Lock()
	q.pending = append(q.pending, p)
	if !q.busy {
		q.busy = true
		go q.run()
	}
	q.mu.Unlock()
	select {
	case err := <-p.done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// run stores the queue's pending blobs, a batch at a time, until there are
// none.
func (q *uploadQueue) run() {
	for {
		q.mu.Lock()
		n := min(len(q.pending), maxUploadBatch)
		if n == 0 {
			q.busy = false
			q.mu.Unlock()
			return
		}
		batch := q.pending[:n:n]
		q.pending = q.pending[n:]
		q.mu.Unlock()
		q.store(batch)
	}
}

// store stores batch, a batch to each of its destinations.
func (q *uploadQueue) store(batch []*pendingUpload) {
	byDst := make(map[blobserver.BlobReceiver][]*pendingUpload)
	var dsts []blobserver.BlobReceiver
	for _, p := range batch {
		if _, ok := byDst[p.dst]; !ok {
			dsts = append(dsts, p.dst)
		}
		byDst[p.dst] = append(byDst[p.dst], p)
	}
	for _, dst := range dsts {
		ps := byDst[dst]
		blobs := make([]*blob.Blob, len(ps))
		for i, p := range ps {
			blobs[i] = p.b
		}
		// The blobs are of many callers, so of none of their contexts:
		// each caller stops waiting when its context is done.
		err := blobserver.ReceiveBlobs(context.Background(), dst, blobs)
		be, isBlobsErr := errors.AsType[*blobserver.BlobsError](err)
		for _, p := range ps {
			switch {
			case err == nil:
				p.done <- nil
			case isBlobsErr:
				p.done <- be.Err(p.b.Ref())
			default:
				p.done <- err
			}
		}
	}
}
