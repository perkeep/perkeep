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

package shard

import (
	"io"
	"os"

	"camli/blobref"
	"camli/blobserver"
	"camli/jsonconfig"
)

type shardStorage struct {
	*blobserver.SimpleBlobHubPartitionMap

	shardPrefixes []string
	shards        []blobserver.Storage
}

func (sto *shardStorage) GetBlobHub() blobserver.BlobHub {
	return sto.SimpleBlobHubPartitionMap.GetBlobHub()
}

func newFromConfig(ld blobserver.Loader, config jsonconfig.Obj) (storage blobserver.Storage, err os.Error) {
	sto := &shardStorage{
		SimpleBlobHubPartitionMap: &blobserver.SimpleBlobHubPartitionMap{},
	}
	sto.shardPrefixes = config.RequiredList("backends")
	if err := config.Validate(); err != nil {
		return nil, err
	}
	if len(sto.shardPrefixes) == 0 {
		return nil, os.NewError("shard: need at least one shard")
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

func (sto *shardStorage) shard(b *blobref.BlobRef) blobserver.Storage {
	return sto.shards[int(sto.shardNum(b))]
}

func (sto *shardStorage) shardNum(b *blobref.BlobRef) uint32 {
	return b.Sum32() % uint32(len(sto.shards))
}

func (sto *shardStorage) FetchStreaming(b *blobref.BlobRef) (file io.ReadCloser, size int64, err os.Error) {
	return sto.shard(b).FetchStreaming(b)
}

func (sto *shardStorage) ReceiveBlob(b *blobref.BlobRef, source io.Reader) (sb blobref.SizedBlobRef, err os.Error) {
	sb, err = sto.shard(b).ReceiveBlob(b, source)
	if err == nil {
		hub := sto.GetBlobHub()
		hub.NotifyBlobReceived(b)
	}
	return
}

func (sto *shardStorage) batchedShards(blobs []*blobref.BlobRef, fn func(blobserver.Storage, []*blobref.BlobRef) os.Error) os.Error {
	m := make(map[uint32][]*blobref.BlobRef)
	for _, b := range blobs {
		sn := sto.shardNum(b)
		m[sn] = append(m[sn], b)
	}
	ch := make(chan os.Error, len(m))
	for sn := range m {
		sblobs := m[sn]
		s := sto.shards[sn]
		go func() {
			ch <- fn(s, sblobs)
		}()
	}
	var reterr os.Error
	for _ = range m {
		if err := <-ch; err != nil {
			reterr = err
		}
	}
	return reterr
}

func (sto *shardStorage) RemoveBlobs(blobs []*blobref.BlobRef) os.Error {
	return sto.batchedShards(blobs, func(s blobserver.Storage, blobs []*blobref.BlobRef) os.Error {
		return s.RemoveBlobs(blobs)
	})
}

func (sto *shardStorage) StatBlobs(dest chan<- blobref.SizedBlobRef, blobs []*blobref.BlobRef, waitSeconds int) os.Error {
	return sto.batchedShards(blobs, func(s blobserver.Storage, blobs []*blobref.BlobRef) os.Error {
		return s.StatBlobs(dest, blobs, waitSeconds)
	})
}

func (sto *shardStorage) EnumerateBlobs(dest chan<- blobref.SizedBlobRef, after string, limit uint, waitSeconds int) os.Error {
	return blobserver.MergedEnumerate(dest, sto.shards, after, limit, waitSeconds)
}

func init() {
	blobserver.RegisterStorageConstructor("shard", blobserver.StorageConstructor(newFromConfig))
}
