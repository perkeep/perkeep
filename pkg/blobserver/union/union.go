/*
Copyright 2017 The Perkeep Authors.

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

// Package union registers the "union" read-only blobserver storage type
// to read from the given subsets, serving the first responding.
package union // import "perkeep.org/pkg/blobserver/union"

import (
	"context"
	"errors"
	"io"
	"sync"

	"go4.org/jsonconfig"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver"
)

type unionStorage struct {
	subsets []blobserver.Storage
}

func newFromConfig(ld blobserver.Loader, conf jsonconfig.Obj) (blobserver.Storage, error) {
	sto := &unionStorage{}

	reads := conf.RequiredList("subsets")
	if err := conf.Validate(); err != nil {
		return nil, err
	}

	for _, s := range reads {
		rs, err := ld.GetStorage(s)
		if err != nil {
			return nil, err
		}
		sto.subsets = append(sto.subsets, rs)
	}

	return sto, nil
}

// ReceiveBlob would receive the blobs, but now just returns ErrReadonly.
func (sto *unionStorage) ReceiveBlob(ctx context.Context, br blob.Ref, src io.Reader) (sb blob.SizedRef, err error) {
	return blob.SizedRef{}, blobserver.ErrReadonly
}

// RemoveBlobs would remove the given blobs, but now just returns ErrReadonly.
func (sto *unionStorage) RemoveBlobs(ctx context.Context, blobs []blob.Ref) error {
	return blobserver.ErrReadonly
}

// Fetch the blob by trying all configured read Storage concurrently,
// returning the first successful response, or the first error if there's no match.
func (sto *unionStorage) Fetch(ctx context.Context, b blob.Ref) (file io.ReadCloser, size uint32, err error) {
	type result struct {
		file io.ReadCloser
		size uint32
		err  error
	}
	results := make(chan result, len(sto.subsets))
	var wg sync.WaitGroup
	for _, bs := range sto.subsets {
		wg.Go(func() {
			var res result
			res.file, res.size, res.err = bs.Fetch(ctx, b)
			results <- res
		})
	}
	go func() {
		wg.Wait()
		close(results)
	}()
	var firstErr error
	var firstRes result
	for r := range results {
		if r.err != nil {
			if firstErr == nil {
				firstErr = r.err
			}
			continue
		}
		if firstRes.file != nil {
			if r.file != nil {
				r.file.Close() // don't need, we already have a successful Fetch
			}
			continue
		}

		firstRes = r
	}
	if firstRes.file != nil {
		return firstRes.file, firstRes.size, nil
	}
	return nil, 0, firstErr
}

// StatBlobs on all BlobStatter reads sequentially, returning the first error.
func (sto *unionStorage) StatBlobs(ctx context.Context, blobs []blob.Ref, f func(blob.SizedRef) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	// need to dedup the blobs
	maybeDup := make(chan blob.SizedRef)
	errCh := make(chan error, 1)
	var wg sync.WaitGroup
	var any bool
	for _, s := range sto.subsets {
		if bs, ok := s.(blobserver.BlobStatter); ok {
			any = true
			wg.Go(func() {
				if err := bs.StatBlobs(ctx, blobs, func(sr blob.SizedRef) error {
					maybeDup <- sr
					return nil
				}); err != nil {
					errCh <- err
				}
			})
		}
	}
	if !any {
		return errors.New("union: No BlobStatter reader configured")
	}

	var closeChanOnce sync.Once
	go func() {
		wg.Wait()
		closeChanOnce.Do(func() { close(maybeDup) })
	}()

	seen := make(map[blob.Ref]struct{}, len(blobs))
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-errCh:
			closeChanOnce.Do(func() { close(maybeDup) })
			return err
		case sr, ok := <-maybeDup:
			if !ok {
				return nil
			}
			if _, ok = seen[sr.Ref]; !ok {
				seen[sr.Ref] = struct{}{}
				if err := f(sr); err != nil {
					return err
				}
			}
		}
	}
}

// EnumerateBlobs concurrently on the readers, returning one of the errors.
func (sto *unionStorage) EnumerateBlobs(ctx context.Context, dest chan<- blob.SizedRef, after string, limit int) error {
	return blobserver.MergedEnumerateStorage(ctx, dest, sto.subsets, after, limit)
}

func init() {
	blobserver.RegisterStorageConstructor("union", blobserver.StorageConstructor(newFromConfig))
}
