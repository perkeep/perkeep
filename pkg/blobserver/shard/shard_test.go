/*
Copyright 2016 The Perkeep Authors

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
	"context"
	"testing"

	"perkeep.org/pkg/blobserver"
	"perkeep.org/pkg/blobserver/storagetest"
	"perkeep.org/pkg/test"
)

var ctxbg = context.Background()

type testStorage struct {
	sto    *shardStorage
	shards []*test.Fetcher
	t      *testing.T
}

func newTestStorage(t *testing.T) *testStorage {
	a, b := &test.Fetcher{}, &test.Fetcher{}
	sto := &shardStorage{
		shardPrefixes: []string{
			"/a/",
			"/b/",
		},
		shards: []blobserver.Storage{a, b},
	}

	ts := &testStorage{
		sto:    sto,
		shards: []*test.Fetcher{a, b},
		t:      t,
	}

	return ts
}

func TestShardBasic(t *testing.T) {
	storagetest.Test(t, func(t *testing.T) blobserver.Storage {
		return newTestStorage(t).sto
	})
}

func TestShard(t *testing.T) {
	thingA := &test.Blob{Contents: "thing A"} // sha224-2b18a3b52a7211954fb97145cf50a29a6e189a6443f7f1e0fa4529f9, shard 1
	thingB := &test.Blob{Contents: "thing B"} // sha224-f19faf56e53a22bc6f84595b5533e943c98d263b232131881f6ace8f, shard 0

	ts := newTestStorage(t)

	ts.sto.ReceiveBlob(ctxbg, thingA.BlobRef(), thingA.Reader())
	ts.sto.ReceiveBlob(ctxbg, thingB.BlobRef(), thingB.Reader())

	ts.checkShard(thingA, 1)
	ts.checkShard(thingB, 0)
}

// checkShard iterates through shards and find the blob. error if it is not found in expectShard, found somewhere else, or not found at all
func (sto testStorage) checkShard(b *test.Blob, expectShard int) {
	for shardN, shard := range sto.shards {
		_, _, err := shard.Fetch(ctxbg, b.BlobRef())
		if err != nil && shardN == expectShard {
			sto.t.Errorf("expected ref %v in shard %d, but didn't find it there", b.BlobRef(), expectShard)
			continue
		}

		if err != nil {
			// node wasn't found here, as expected
			continue
		}

		if shardN != expectShard {
			sto.t.Errorf("found ref %v in shard %d, expected in shard %d", b.BlobRef(), shardN, expectShard)
		}

		// node was found, and we expected it
	}
}
