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

// NewBatch returns a Batch storing each blob as it's put, as storing a
// blob in the server, rather than over HTTP, is quick enough.
func (s hostServer) NewBatch(ctx context.Context) (takeoutimport.Batch, error) {
	return directBatch{s}, nil
}

type directBatch struct{ s hostServer }

func (b directBatch) Put(ctx context.Context, contents string) error {
	_, err := b.s.PutBlob(ctx, contents)
	return err
}

func (directBatch) Flush(context.Context) error { return nil }
func (directBatch) Close() error                { return nil }
