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

package googletakeout

import (
	"context"
	"errors"
	"io"
	"sync"

	"perkeep.org/internal/takeoutimport"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver"
	"perkeep.org/pkg/importer"
	"perkeep.org/pkg/schema"
	"perkeep.org/pkg/search"
)

// hostServer implements [takeoutimport.Server] with an importer Host,
// in the server that it imports into.
type hostServer struct {
	h *importer.Host
}

var _ takeoutimport.Server = hostServer{}

func (s hostServer) Query(ctx context.Context, q *search.SearchQuery) (*search.SearchResult, error) {
	return s.h.Searcher().Query(ctx, q)
}

func (s hostServer) Describe(ctx context.Context, req *search.DescribeRequest) (*search.DescribeResponse, error) {
	return s.h.Searcher().Describe(ctx, req)
}

// claimser is implemented by the search handler.
type claimser interface {
	GetClaims(*search.ClaimsRequest) (*search.ClaimsResponse, error)
}

func (s hostServer) GetClaims(ctx context.Context, req *search.ClaimsRequest) (*search.ClaimsResponse, error) {
	c, ok := s.h.Searcher().(claimser)
	if !ok {
		return nil, errors.New("googletakeout: the server's search handler can't get claims")
	}
	return c.GetClaims(req)
}

func (s hostServer) FetchSchemaBlob(ctx context.Context, br blob.Ref) (*schema.Blob, error) {
	rc, _, err := s.h.BlobSource().Fetch(ctx, br)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return schema.BlobFromReader(br, rc)
}

func (s hostServer) PutBlob(ctx context.Context, contents string) (blob.Ref, error) {
	sb, err := blobserver.ReceiveString(ctx, s.h.Target(), contents)
	return sb.Ref, err
}

func (s hostServer) PutFile(ctx context.Context, name string, r io.Reader) (file, whole blob.Ref, err error) {
	h := blob.NewHash()
	file, err = schema.WriteFileMap(ctx, s.h.Target(), schema.NewFileMap(name), io.TeeReader(r, h))
	if err != nil {
		return blob.Ref{}, blob.Ref{}, err
	}
	return file, blob.RefFromHash(h), nil
}

// NewBatch returns a Batch storing its blobs in groups, as an upload
// session does.
func (s hostServer) NewBatch(ctx context.Context) (takeoutimport.Batch, error) {
	return &groupBatch{s: s}, nil
}

// A group is a Batch's group of blobs up to groupBlobs blobs, or of about
// groupBytes bytes, whichever is reached first, which it stores together.
const (
	groupBlobs = 256
	groupBytes = 32 << 20
)

// groupBatch is a Batch storing the blobs put in it in groups, with
// blobserver.ReceiveBlobs, so that storage shares the cost of making them
// durable, such as syncs to disk, among each group's blobs, rather than
// paying it for each, as storing blobs one at a time does.
type groupBatch struct {
	s hostServer

	mu      sync.Mutex
	pending []*blob.Blob // not yet being stored
	size    int64        // of pending
	err     error        // the first error storing a group
	storing sync.WaitGroup
}

func (b *groupBatch) Put(ctx context.Context, contents string) error {
	data := []byte(contents)
	bl := blob.NewBlob(blob.RefFromBytes(data), uint32(len(data)), func(context.Context) ([]byte, error) { return data, nil })
	b.mu.Lock()
	if b.err != nil {
		defer b.mu.Unlock()
		return b.err
	}
	b.pending = append(b.pending, bl)
	b.size += int64(len(data))
	if len(b.pending) < groupBlobs && b.size < groupBytes {
		b.mu.Unlock()
		return nil
	}
	group := b.take()
	b.mu.Unlock()
	return b.store(ctx, group)
}

// take returns the pending blobs, as a group being stored. b.mu must be
// held.
func (b *groupBatch) take() []*blob.Blob {
	group := b.pending
	b.pending, b.size = nil, 0
	b.storing.Add(1)
	return group
}

// store stores group, returned by take.
func (b *groupBatch) store(ctx context.Context, group []*blob.Blob) error {
	defer b.storing.Done()
	err := blobserver.ReceiveBlobs(ctx, b.s.h.Target(), group)
	if err != nil {
		b.mu.Lock()
		if b.err == nil {
			b.err = err
		}
		b.mu.Unlock()
	}
	return err
}

// Flush stores the pending blobs, and waits for the groups being stored.
func (b *groupBatch) Flush(ctx context.Context) error {
	b.mu.Lock()
	var group []*blob.Blob
	if len(b.pending) > 0 && b.err == nil {
		group = b.take()
	}
	b.mu.Unlock()
	if group != nil {
		b.store(ctx, group)
	}
	b.storing.Wait()
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.err
}

// Close drops the blobs not yet stored.
func (b *groupBatch) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.pending, b.size = nil, 0
	return nil
}
