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

	"go4.org/jsonconfig"
)

const (
	MaxKeySize   = 767   // Maximum size, in bytes, for a key in any store implementing KeyValue.
	MaxValueSize = 63000 // Maximum size, in bytes, for a value in any store implementing KeyValue. MaxKeySize and MaxValueSize values originate from InnoDB and MySQL limitations.
)

const DefaultKVFileType = "leveldb"

var (
	ErrNotFound      = errors.New("sorted: key not found")
	ErrKeyTooLarge   = fmt.Errorf("sorted: key size is over %v", MaxKeySize)
	ErrValueTooLarge = fmt.Errorf("sorted: value size is over %v", MaxValueSize)
)

// KeyValue is a sorted, enumerable key-value interface supporting
// batch mutations.
type KeyValue interface {
	// Get gets the value for the given key. It returns ErrNotFound if the DB
	// does not contain the key.
	Get(key string) (string, error)

	Set(key, value string) error

	// Delete deletes keys. Deleting a non-existent key does not return an error.
	Delete(key string) error

	BeginBatch() BatchMutation
	CommitBatch(b BatchMutation) error

	// Find returns an iterator positioned before the first key/value pair
	// whose key is 'greater than or equal to' the given key. There may be no
	// such pair, in which case the iterator will return false on Next.
	//
	// The optional end value specifies the exclusive upper
	// bound. If the empty string, the iterator returns keys
	// where "key >= start".
	// If non-empty, the iterator returns keys where
	// "key >= start && key < endHint".
	//
	// Any error encountered will be implicitly returned via the iterator. An
	// error-iterator will yield no key/value pairs and closing that iterator
	// will return that error.
	Find(start, end string) Iterator

	// Close is a polite way for the server to shut down the storage.
	// Implementations should never lose data after a Set, Delete,
	// or CommmitBatch, though.
	Close() error
}

// Wiper is an optional interface that may be implemented by storage
// implementations.
type Wiper interface {
	KeyValue

	// Wipe removes all key/value pairs.
	Wipe() error
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

	// KeyBytes returns the key as bytes. The returned bytes
	// should not be written and are invalid after the next call
	// to Next or Close.
	// TODO(bradfitz): rename this and change it to return a
	// mem.RO instead?
	KeyBytes() []byte

	// Value returns the value of the current key/value pair.
	// Only valid after a call to Next returns true.
	Value() string

	// ValueBytes returns the value as bytes. The returned bytes
	// should not be written and are invalid after the next call
	// to Next or Close.
	// TODO(bradfitz): rename this and change it to return a
	// mem.RO instead?
	ValueBytes() []byte

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
		return nil, fmt.Errorf("Invalid sorted.KeyValue type %q", typ)
	}
	if ok {
		s, err = ctor(cfg)
		if err != nil {
			return nil, fmt.Errorf("error from %q KeyValue: %v", typ, err)
		}
	}
	return s, cfg.Validate()
}

// Foreach runs fn for each key/value pair in kv. If fn returns an error,
// that same error is returned from Foreach and iteration stops.
func Foreach(kv KeyValue, fn func(key, value string) error) error {
	return ForeachInRange(kv, "", "", fn)
}

// ForeachInRange runs fn for each key/value pair in kv in the range
// of start and end, which behave the same as kv.Find. If fn returns
// an error, that same error is returned from Foreach and iteration
// stops.
func ForeachInRange(kv KeyValue, start, end string, fn func(key, value string) error) error {
	it := kv.Find(start, end)
	for it.Next() {
		if err := fn(it.Key(), it.Value()); err != nil {
			it.Close()
			return err
		}
	}
	return it.Close()
}

// CheckSizes returns ErrKeyTooLarge if key does not respect KeyMaxSize or
// ErrValueTooLarge if value does not respect ValueMaxSize
func CheckSizes(key, value string) error {
	if len(key) > MaxKeySize {
		return ErrKeyTooLarge
	}
	if len(value) > MaxValueSize {
		return ErrValueTooLarge
	}
	return nil
}
