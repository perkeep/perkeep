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

package index

import (
	"os"
	"sync"

	"camli/blobserver"
	"camli/jsonconfig"

	"leveldb-go.googlecode.com/hg/leveldb/db"
	"leveldb-go.googlecode.com/hg/leveldb/memdb"
)

func init() {
	blobserver.RegisterStorageConstructor("memory-only-dev-indexer",
		blobserver.StorageConstructor(newMemoryIndexFromConfig))
}

func newMemoryIndex() *Index {
	db := memdb.New(nil)
	memStorage := &memKeys{db: db}
	return New(memStorage)
}

func newMemoryIndexFromConfig(ld blobserver.Loader, config jsonconfig.Obj) (blobserver.Storage, os.Error) {
	blobPrefix := config.RequiredString("blobSource")
	if err := config.Validate(); err != nil {
		return nil, err
	}
	sto, err := ld.GetStorage(blobPrefix)
	if err != nil {
		return nil, err
	}

	ix := newMemoryIndex()
	ix.BlobSource = sto

	// Good enough, for now:
	ix.KeyFetcher = ix.BlobSource

	return ix, err
}

// memKeys is a naive in-memory implementation of IndexStorage for test & development
// purposes only.
type memKeys struct {
	mu sync.Mutex // guards db
	db db.DB
}

// stringIterator converts from leveldb's db.Iterator interface, which
// operates on []byte, to Camlistore's index.Iterator, which operates
// on string.
type stringIterator struct {
	db.Iterator
}

func (s stringIterator) Key() string {
	return string(s.Iterator.Key())
}

func (s stringIterator) Value() string {
	return string(s.Iterator.Value())
}

func (mk *memKeys) Get(key string) (string, os.Error) {
	mk.mu.Lock()
	defer mk.mu.Unlock()
	k, err := mk.db.Get([]byte(key))
	if err == db.ErrNotFound {
		return "", ErrNotFound
	}
	return string(k), err
}

func (mk *memKeys) Find(key string) Iterator {
	mk.mu.Lock()
	defer mk.mu.Unlock()
	dit := mk.db.Find([]byte(key))
	return stringIterator{dit}
}

func (mk *memKeys) Set(key, value string) os.Error {
	mk.mu.Lock()
	defer mk.mu.Unlock()
	return mk.db.Set([]byte(key), []byte(value))
}

func (mk *memKeys) Delete(key string) os.Error {
	mk.mu.Lock()
	defer mk.mu.Unlock()
	return mk.db.Delete([]byte(key))
}

func (mk *memKeys) BeginBatch() BatchMutation {
	return &batch{}
}

func (mk *memKeys) CommitBatch(bm BatchMutation) os.Error {
	b, ok := bm.(*batch)
	if !ok {
		return os.NewError("invalid batch type; not an instance returned by BeginBatch")
	}
	mk.mu.Lock()
	defer mk.mu.Unlock()
	for _, m := range b.m {
		if m.delete {
			if err := mk.db.Delete([]byte(m.key)); err != nil {
				return err
			}
		} else {
			if err := mk.db.Set([]byte(m.key), []byte(m.value)); err != nil {
				return err
			}
		}
	}
	return nil
}
