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

package takeoutimport

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"sync"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/client"
	"perkeep.org/pkg/upload"
)

// Client implements [Server] with a [client.Client], talking to a
// Perkeep server over HTTP, as pk takeout does.
type Client struct {
	*client.Client
	Limiter *upload.Limiter // or nil, for a default one

	// FlushEvery, if non-zero, is used instead of batchFlushEvery.
	FlushEvery int

	limOnce sync.Once
}

var _ Server = (*Client)(nil)

func (s *Client) limiter() *upload.Limiter {
	s.limOnce.Do(func() {
		if s.Limiter == nil {
			s.Limiter = upload.NewLimiter(upload.Limits{})
		}
	})
	return s.Limiter
}

func (s *Client) PutBlob(ctx context.Context, contents string) (blob.Ref, error) {
	res, err := s.Upload(ctx, client.NewUploadHandleFromString(contents))
	if err != nil {
		return blob.Ref{}, err
	}
	return res.BlobRef, nil
}

func (s *Client) PutFile(ctx context.Context, name string, r io.Reader) (file, whole blob.Ref, err error) {
	res, err := upload.NewFileFromReader(s.Client, name, r, &upload.Options{Limiter: s.limiter()}).Run(ctx)
	if err != nil {
		return blob.Ref{}, blob.Ref{}, err
	}
	if len(res.WholeRefs) == 0 {
		return blob.Ref{}, blob.Ref{}, fmt.Errorf("no whole-file ref for %v", res.FileRef)
	}
	return res.FileRef, res.WholeRefs[0], nil
}

// NewBatch returns a Batch of an upload session, if the server has them,
// or else one that uploads each blob as it's put.
func (s *Client) NewBatch(ctx context.Context) (Batch, error) {
	sess, err := s.NewUploadSession(ctx)
	if errors.Is(err, errors.ErrUnsupported) {
		return directBatch{s}, nil
	}
	if err != nil {
		return nil, err
	}
	return &sessionBatch{sess: sess, flushEvery: cmp.Or(s.FlushEvery, batchFlushEvery)}, nil
}

// batchFlushEvery is how many blobs a sessionBatch uploads into its
// session between waiting for them to be durable: well under the
// server's limit of blobs not acknowledged (100,000 by default).
const batchFlushEvery = 10_000

type directBatch struct{ s *Client }

func (b directBatch) Put(ctx context.Context, contents string) error {
	_, err := b.s.PutBlob(ctx, contents)
	return err
}

func (directBatch) Flush(context.Context) error { return nil }
func (directBatch) Close() error                { return nil }

// sessionBatch is a Batch of an upload session.
type sessionBatch struct {
	sess       *client.UploadSession
	flushEvery int

	// flushMu is held for reading by uploads into sess, and for writing
	// while waiting for them to be durable.
	flushMu sync.RWMutex

	mu      sync.Mutex
	pending []blob.Ref // uploaded into sess, to wait for
}

func (b *sessionBatch) Put(ctx context.Context, contents string) error {
	h := client.NewUploadHandleFromString(contents)
	// The server holds only so many blobs in a session, buffered or not
	// yet acknowledged, which the session does as it flushes, so every
	// so often, wait for those uploaded to be durable, with uploads
	// paused, as small ones can otherwise outrun the server's commits.
	b.flushMu.RLock()
	err := b.sess.Upload(ctx, h)
	b.mu.Lock()
	if err == nil {
		b.pending = append(b.pending, h.BlobRef)
	}
	var batch []blob.Ref
	if len(b.pending) >= b.flushEvery {
		batch, b.pending = b.pending, nil
	}
	b.mu.Unlock()
	b.flushMu.RUnlock()
	if err != nil {
		return err
	}
	if batch != nil {
		b.flushMu.Lock()
		err := b.sess.WaitDurable(ctx, batch)
		b.flushMu.Unlock()
		if err != nil {
			return err
		}
	}
	return nil
}

func (b *sessionBatch) Flush(ctx context.Context) error {
	b.mu.Lock()
	batch := b.pending
	b.pending = nil
	b.mu.Unlock()
	b.flushMu.Lock()
	defer b.flushMu.Unlock()
	return b.sess.WaitDurable(ctx, batch)
}

func (b *sessionBatch) Close() error {
	return b.sess.Close(context.Background())
}
