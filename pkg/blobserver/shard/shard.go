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

/*
Package shard registers the "shard" blobserver storage type,
predictably spraying out blobs out over the provided backends
based on their blobref. Each blob maps to exactly one backend.

Example low-level config:

     "/foo/": {
         "handler": "storage-shard",
         "handlerArgs": {
             "backends": ["/s1/", "/s2/"]
          }
     },

*/
package shard // import "perkeep.org/pkg/blobserver/shard"

import (
	"context"
	"errors"
	"io"
	"sync"

	"go4.org/jsonconfig"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver"
)

type shardStorage struct {
	shardPrefixes []string
	shards        []blobserver.Storage
}

func newFromConfig(ld blobserver.Loader, config jsonconfig.Obj) (storage blobserver.Storage, err error) {
	sto := &shardStorage{
		shardPrefixes: config.RequiredList("backends"),
	}
	if err := config.Validate(); err != nil {
		return nil, err
	}
	if len(sto.shardPrefixes) == 0 {
		return nil, errors.New("shard: need at least one shard")
	}
	sto.shards = make([]blobserver.Storage, len(sto.shardPrefixes))
	for i, prefix := range sto.shardPrefixes {
		shardSto, err := ld.GetStorage(prefix)
		if err != nil {
			return nil, err
		}
		sto.shards[i] = shardSto
	}
	return sto, nil
}

func (sto *shardStorage) shard(b blob.Ref) blobserver.Storage {
	return sto.shards[int(sto.shardNum(b))]
}

func (sto *shardStorage) shardNum(b blob.Ref) uint32 {
	return b.Sum32() % uint32(len(sto.shards))
}

func (sto *shardStorage) Fetch(ctx context.Context, b blob.Ref) (file io.ReadCloser, size uint32, err error) {
	return sto.shard(b).Fetch(ctx, b)
}

func (sto *shardStorage) ReceiveBlob(ctx context.Context, b blob.Ref, source io.Reader) (sb blob.SizedRef, err error) {
	return sto.shard(b).ReceiveBlob(ctx, b, source)
}

func (sto *shardStorage) batchedShards(ctx context.Context, blobs []blob.Ref, fn func(blobserver.Storage, []blob.Ref) error) error {
	m := make(map[uint32][]blob.Ref)
	for _, b := range blobs {
		sn := sto.shardNum(b)
		m[sn] = append(m[sn], b)
	}
	ch := make(chan error, len(m))
	for sn := range m {
		sblobs := m[sn]
		s := sto.shards[sn]
		go func() {
			ch <- fn(s, sblobs)
		}()
	}
	var reterr error
	for range m {
		if err := <-ch; err != nil {
			reterr = err
		}
	}
	return reterr
}

func (sto *shardStorage) RemoveBlobs(ctx context.Context, blobs []blob.Ref) error {
	return sto.batchedShards(context.TODO(), blobs, func(s blobserver.Storage, blobs []blob.Ref) error {
		return s.RemoveBlobs(ctx, blobs)
	})
}

func (sto *shardStorage) StatBlobs(ctx context.Context, blobs []blob.Ref, fn func(blob.SizedRef) error) error {
	var (
		fnMu   sync.Mutex // serializes calls to fn, guards failed
		failed bool
	)
	// TODO: do a context.WithCancel and abort all shards' context
	// once one fails, but don't do that until we can guarantee
	// that the first failure we report is the real one, not
	// another goroutine getting its context canceled before our
	// real first failure returns from its goroutine. That is, we
	// should use golang.org/x/sync/errgroup, but we need to
	// integrate it with batchedShards and audit callers. Or not
	// use batchedShards here, or only use batchedShards to
	// collect work to do and then use errgroup directly ourselves
	// here.
	return sto.batchedShards(ctx, blobs, func(s blobserver.Storage, blobs []blob.Ref) error {
		return s.StatBlobs(ctx, blobs, func(sb blob.SizedRef) error {
			fnMu.Lock()
			defer fnMu.Unlock()
			if failed {
				return nil
			}
			if err := fn(sb); err != nil {
				failed = true
				return err
			}
			return nil
		})
	})
}

func (sto *shardStorage) EnumerateBlobs(ctx context.Context, dest chan<- blob.SizedRef, after string, limit int) error {
	return blobserver.MergedEnumerateStorage(ctx, dest, sto.shards, after, limit)
}

func init() {
	blobserver.RegisterStorageConstructor("shard", blobserver.StorageConstructor(newFromConfig))
}
