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
	"sync"

	"camlistore.org/pkg/jsonconfig"
	"camlistore.org/third_party/code.google.com/p/leveldb-go/leveldb/db"
	"camlistore.org/third_party/code.google.com/p/leveldb-go/leveldb/memdb"
)

// NewMemoryKeyValue returns a KeyValue implementation that's backed only
// by memory. It's mostly useful for tests and development.
func NewMemoryKeyValue() KeyValue {
	db := memdb.New(nil)
	return &memKeys{db: db}
}

// memKeys is a naive in-memory implementation of index.Storage for test & development
// purposes only.
type memKeys struct {
	mu sync.Mutex // guards db
	db db.DB
}

// stringIterator converts from leveldb's db.Iterator interface, which
// operates on []byte, to Camlistore's index.Iterator, which operates
// on string.
type stringIterator struct {
	lit  db.Iterator // underlying leveldb iterator
	k, v *string     // if nil, not stringified yet
}

func (s *stringIterator) Next() bool {
	s.k, s.v = nil, nil
	return s.lit.Next()
}

func (s *stringIterator) Close() error {
	err := s.lit.Close()
	*s = stringIterator{} // to cause crashes on future access
	return err
}

func (s *stringIterator) KeyBytes() []byte {
	return s.lit.Key()
}

func (s *stringIterator) ValueBytes() []byte {
	return s.lit.Value()
}

func (s *stringIterator) Key() string {
	if s.k != nil {
		return *s.k
	}
	str := string(s.KeyBytes())
	s.k = &str
	return str
}

func (s *stringIterator) Value() string {
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
	k, err := mk.db.Get([]byte(key), nil)
	if err == db.ErrNotFound {
		return "", ErrNotFound
	}
	return string(k), err
}

func (mk *memKeys) Find(key string) Iterator {
	mk.mu.Lock()
	defer mk.mu.Unlock()
	lit := mk.db.Find([]byte(key), nil)
	return &stringIterator{lit: lit}
}

func (mk *memKeys) Set(key, value string) error {
	mk.mu.Lock()
	defer mk.mu.Unlock()
	return mk.db.Set([]byte(key), []byte(value), nil)
}

func (mk *memKeys) Delete(key string) error {
	mk.mu.Lock()
	defer mk.mu.Unlock()
	return mk.db.Delete([]byte(key), nil)
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
			if err := mk.db.Delete([]byte(m.Key()), nil); err != nil {
				return err
			}
		} else {
			if err := mk.db.Set([]byte(m.Key()), []byte(m.Value()), nil); err != nil {
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
