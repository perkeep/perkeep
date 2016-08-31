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

package sorted

import (
	"errors"
	"log"
	"sync"

	"github.com/syndtr/goleveldb/leveldb/comparer"
	"github.com/syndtr/goleveldb/leveldb/iterator"
	"github.com/syndtr/goleveldb/leveldb/memdb"
	"github.com/syndtr/goleveldb/leveldb/util"
	"go4.org/jsonconfig"
)

// NewMemoryKeyValue returns a KeyValue implementation that's backed only
// by memory. It's mostly useful for tests and development.
func NewMemoryKeyValue() KeyValue {
	db := memdb.New(comparer.DefaultComparer, 128)
	return &memKeys{db: db}
}

// memKeys is a naive in-memory implementation of KeyValue for test & development
// purposes only.
type memKeys struct {
	mu sync.Mutex // guards db
	db *memdb.DB
}

// memIter converts from leveldb's iterator.Iterator interface, which
// operates on []byte, to Camlistore's index.Iterator, which operates
// on string.
type memIter struct {
	lit  iterator.Iterator // underlying leveldb iterator
	k, v *string           // if nil, not stringified yet
}

func (t *memIter) Next() bool {
	t.k, t.v = nil, nil
	return t.lit.Next()
}

func (s *memIter) Close() error {
	s.lit.Release()
	return s.lit.Error()
}

func (s *memIter) KeyBytes() []byte {
	return s.lit.Key()
}

func (s *memIter) ValueBytes() []byte {
	return s.lit.Value()
}

func (s *memIter) Key() string {
	if s.k != nil {
		return *s.k
	}
	str := string(s.KeyBytes())
	s.k = &str
	return str
}

func (s *memIter) Value() string {
	if s.v != nil {
		return *s.v
	}
	str := string(s.ValueBytes())
	s.v = &str
	return str
}

func (mk *memKeys) Get(key string) (string, error) {
	mk.mu.Lock()
	defer mk.mu.Unlock()
	k, err := mk.db.Get([]byte(key))
	if err == memdb.ErrNotFound {
		return "", ErrNotFound
	}
	return string(k), err
}

func (mk *memKeys) Find(start, end string) Iterator {
	mk.mu.Lock()
	defer mk.mu.Unlock()
	var startB, endB []byte
	if start != "" {
		startB = []byte(start)
	}
	if end != "" {
		endB = []byte(end)
	}
	lit := mk.db.NewIterator(&util.Range{Start: startB, Limit: endB})
	return &memIter{lit: lit}
}

func (mk *memKeys) Set(key, value string) error {
	if err := CheckSizes(key, value); err != nil {
		log.Printf("Skipping storing (%q:%q): %v", key, value, err)
		return nil
	}
	mk.mu.Lock()
	defer mk.mu.Unlock()
	return mk.db.Put([]byte(key), []byte(value))
}

func (mk *memKeys) Delete(key string) error {
	mk.mu.Lock()
	defer mk.mu.Unlock()
	err := mk.db.Delete([]byte(key))
	if err == memdb.ErrNotFound {
		return nil
	}
	return err
}

func (mk *memKeys) BeginBatch() BatchMutation {
	return &batch{}
}

func (mk *memKeys) CommitBatch(bm BatchMutation) error {
	b, ok := bm.(*batch)
	if !ok {
		return errors.New("invalid batch type; not an instance returned by BeginBatch")
	}
	mk.mu.Lock()
	defer mk.mu.Unlock()
	for _, m := range b.Mutations() {
		if m.IsDelete() {
			if err := mk.db.Delete([]byte(m.Key())); err != nil && err != memdb.ErrNotFound {
				return err
			}
		} else {
			// TODO(mpl): we need to force a reindex when we have a proper solution
			// for storing these too large attributes, if ever.
			if err := CheckSizes(m.Key(), m.Value()); err != nil {
				log.Printf("Skipping storing (%q:%q): %v", m.Key(), m.Value(), err)
				continue
			}
			if err := mk.db.Put([]byte(m.Key()), []byte(m.Value())); err != nil {
				return err
			}
		}
	}
	return nil
}

func (mk *memKeys) Close() error { return nil }

func init() {
	RegisterKeyValue("memory", func(cfg jsonconfig.Obj) (KeyValue, error) {
		if err := cfg.Validate(); err != nil {
			return nil, err
		}
		return NewMemoryKeyValue(), nil
	})
}
