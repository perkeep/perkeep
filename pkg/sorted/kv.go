/*
Copyright 2013 The Camlistore Authors

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

// Package sorted provides a KeyValue interface and constructor registry.
package sorted

import (
	"errors"
	"fmt"

	"camlistore.org/pkg/jsonconfig"
)

var ErrNotFound = errors.New("index: key not found")

// KeyValue is a sorted, enumerable key-value interface supporting
// batch mutations.
type KeyValue interface {
	// Get gets the value for the given key. It returns ErrNotFound if the DB
	// does not contain the key.
	Get(key string) (string, error)

	Set(key, value string) error
	Delete(key string) error

	BeginBatch() BatchMutation
	CommitBatch(b BatchMutation) error

	// Find returns an iterator positioned before the first key/value pair
	// whose key is 'greater than or equal to' the given key. There may be no
	// such pair, in which case the iterator will return false on Next.
	//
	// Any error encountered will be implicitly returned via the iterator. An
	// error-iterator will yield no key/value pairs and closing that iterator
	// will return that error.
	Find(key string) Iterator

	// Close is a polite way for the server to shut down the storage.
	// Implementations should never lose data after a Set, Delete,
	// or CommmitBatch, though.
	Close() error
}

// Iterator iterates over an index KeyValue's key/value pairs in key order.
//
// An iterator must be closed after use, but it is not necessary to read an
// iterator until exhaustion.
//
// An iterator is not necessarily goroutine-safe, but it is safe to use
// multiple iterators concurrently, with each in a dedicated goroutine.
type Iterator interface {
	// Next moves the iterator to the next key/value pair.
	// It returns false when the iterator is exhausted.
	Next() bool

	// Key returns the key of the current key/value pair.
	// Only valid after a call to Next returns true.
	Key() string

	// Value returns the value of the current key/value pair.
	// Only valid after a call to Next returns true.
	Value() string

	// Close closes the iterator and returns any accumulated error. Exhausting
	// all the key/value pairs in a table is not considered to be an error.
	// It is valid to call Close multiple times. Other methods should not be
	// called after the iterator has been closed.
	Close() error
}

type BatchMutation interface {
	Set(key, value string)
	Delete(key string)
}

type Mutation interface {
	Key() string
	Value() string
	IsDelete() bool
}

type mutation struct {
	key    string
	value  string // used if !delete
	delete bool   // if to be deleted
}

func (m mutation) Key() string {
	return m.key
}

func (m mutation) Value() string {
	return m.value
}

func (m mutation) IsDelete() bool {
	return m.delete
}

func NewBatchMutation() BatchMutation {
	return &batch{}
}

type batch struct {
	m []Mutation
}

func (b *batch) Mutations() []Mutation {
	return b.m
}

func (b *batch) Delete(key string) {
	b.m = append(b.m, mutation{key: key, delete: true})
}

func (b *batch) Set(key, value string) {
	b.m = append(b.m, mutation{key: key, value: value})
}

var (
	ctors = make(map[string]func(jsonconfig.Obj) (KeyValue, error))
)

func RegisterKeyValue(typ string, fn func(jsonconfig.Obj) (KeyValue, error)) {
	if typ == "" || fn == nil {
		panic("zero type or func")
	}
	if _, dup := ctors[typ]; dup {
		panic("duplication registration of type " + typ)
	}
	ctors[typ] = fn
}

func NewKeyValue(cfg jsonconfig.Obj) (KeyValue, error) {
	var s KeyValue
	var err error
	typ := cfg.RequiredString("type")
	ctor, ok := ctors[typ]
	if typ != "" && !ok {
		return nil, fmt.Errorf("Invalidate index storage type %q", typ)
	}
	if ok {
		s, err = ctor(cfg)
		if err != nil {
			return nil, err
		}
	}
	return s, cfg.Validate()
}
