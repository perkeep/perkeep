/*
Copyright 2013 The Camlistore Authors.

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

// Package kvfile provides an implementation of sorted.KeyValue
// on top of a single mutable database file on disk using
// github.com/cznic/kv.
package kvfile

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"sync"

	"camlistore.org/pkg/kvutil"
	"camlistore.org/pkg/sorted"
	"go4.org/jsonconfig"

	"camlistore.org/third_party/github.com/cznic/kv"
)

var _ sorted.Wiper = (*kvis)(nil)

func init() {
	sorted.RegisterKeyValue("kv", newKeyValueFromJSONConfig)
}

// NewStorage is a convenience that calls newKeyValueFromJSONConfig
// with file as the kv storage file.
func NewStorage(file string) (sorted.KeyValue, error) {
	return newKeyValueFromJSONConfig(jsonconfig.Obj{"file": file})
}

// newKeyValueFromJSONConfig returns a KeyValue implementation on top of a
// github.com/cznic/kv file.
func newKeyValueFromJSONConfig(cfg jsonconfig.Obj) (sorted.KeyValue, error) {
	file := cfg.RequiredString("file")
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	opts := &kv.Options{}
	db, err := kvutil.Open(file, opts)
	if err != nil {
		return nil, err
	}
	is := &kvis{
		db:   db,
		opts: opts,
		path: file,
	}
	return is, nil
}

type kvis struct {
	path string
	db   *kv.DB
	opts *kv.Options
	txmu sync.Mutex
}

// TODO: use bytepool package.
func getBuf(n int) []byte { return make([]byte, n) }
func putBuf([]byte)       {}

func (is *kvis) Get(key string) (string, error) {
	buf := getBuf(200)
	defer putBuf(buf)
	val, err := is.db.Get(buf, []byte(key))
	if err != nil {
		return "", err
	}
	if val == nil {
		return "", sorted.ErrNotFound
	}
	return string(val), nil
}

func (is *kvis) Set(key, value string) error {
	if err := sorted.CheckSizes(key, value); err != nil {
		return err
	}
	return is.db.Set([]byte(key), []byte(value))
}

func (is *kvis) Delete(key string) error {
	return is.db.Delete([]byte(key))
}

func (is *kvis) Find(start, end string) sorted.Iterator {
	it := &iter{
		db:       is.db,
		startKey: start,
		endKey:   []byte(end),
	}
	it.enum, _, it.err = it.db.Seek([]byte(start))
	return it
}

func (is *kvis) BeginBatch() sorted.BatchMutation {
	return sorted.NewBatchMutation()
}

func (is *kvis) Wipe() error {
	// Unlock the already open DB.
	if err := is.db.Close(); err != nil {
		return err
	}
	if err := os.Remove(is.path); err != nil {
		return err
	}

	db, err := kv.Create(is.path, is.opts)
	if err != nil {
		return fmt.Errorf("error creating %s: %v", is.path, err)
	}
	is.db = db
	return nil
}

type batch interface {
	Mutations() []sorted.Mutation
}

func (is *kvis) CommitBatch(bm sorted.BatchMutation) error {
	b, ok := bm.(batch)
	if !ok {
		return errors.New("invalid batch type")
	}
	is.txmu.Lock()
	defer is.txmu.Unlock()

	good := false
	defer func() {
		if !good {
			is.db.Rollback()
		}
	}()

	if err := is.db.BeginTransaction(); err != nil {
		return err
	}
	for _, m := range b.Mutations() {
		if m.IsDelete() {
			if err := is.db.Delete([]byte(m.Key())); err != nil {
				return err
			}
		} else {
			if err := sorted.CheckSizes(m.Key(), m.Value()); err != nil {
				return err
			}
			if err := is.db.Set([]byte(m.Key()), []byte(m.Value())); err != nil {
				return err
			}
		}
	}

	good = true
	return is.db.Commit()
}

func (is *kvis) Close() error {
	log.Printf("Closing kvfile database %s", is.path)
	return is.db.Close()
}

type iter struct {
	db       *kv.DB
	startKey string
	endKey   []byte

	enum *kv.Enumerator

	valid      bool
	key, val   []byte
	skey, sval *string // non-nil if valid

	err    error
	closed bool
}

func (it *iter) Close() error {
	it.closed = true
	return it.err
}

func (it *iter) KeyBytes() []byte {
	if !it.valid {
		panic("not valid")
	}
	return it.key
}

func (it *iter) Key() string {
	if !it.valid {
		panic("not valid")
	}
	if it.skey != nil {
		return *it.skey
	}
	str := string(it.key)
	it.skey = &str
	return str
}

func (it *iter) ValueBytes() []byte {
	if !it.valid {
		panic("not valid")
	}
	return it.val
}

func (it *iter) Value() string {
	if !it.valid {
		panic("not valid")
	}
	if it.sval != nil {
		return *it.sval
	}
	str := string(it.val)
	it.sval = &str
	return str
}

func (it *iter) end() bool {
	it.valid = false
	it.closed = true
	return false
}

func (it *iter) Next() bool {
	if it.err != nil {
		return false
	}
	if it.closed {
		panic("Next called after Next returned value")
	}
	it.skey, it.sval = nil, nil
	var err error
	it.key, it.val, err = it.enum.Next()
	if err == io.EOF {
		it.err = nil
		return it.end()
	}
	if err != nil {
		it.err = err
		return it.end()
	}
	if len(it.endKey) > 0 && bytes.Compare(it.key, it.endKey) >= 0 {
		return it.end()
	}
	it.valid = true
	return true
}
