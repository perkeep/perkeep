/*
Copyright 2014 The Camlistore Authors.

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

// Package leveldb provides an implementation of sorted.KeyValue
// on top of a single mutable database file on disk using
// github.com/syndtr/goleveldb.
package leveldb

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"camlistore.org/pkg/env"
	"camlistore.org/pkg/sorted"
	"go4.org/jsonconfig"

	"camlistore.org/third_party/github.com/syndtr/goleveldb/leveldb"
	"camlistore.org/third_party/github.com/syndtr/goleveldb/leveldb/filter"
	"camlistore.org/third_party/github.com/syndtr/goleveldb/leveldb/iterator"
	"camlistore.org/third_party/github.com/syndtr/goleveldb/leveldb/opt"
	"camlistore.org/third_party/github.com/syndtr/goleveldb/leveldb/util"
)

var _ sorted.Wiper = (*kvis)(nil)

func init() {
	sorted.RegisterKeyValue("leveldb", newKeyValueFromJSONConfig)
}

// NewStorage is a convenience that calls newKeyValueFromJSONConfig
// with file as the leveldb storage file.
func NewStorage(file string) (sorted.KeyValue, error) {
	return newKeyValueFromJSONConfig(jsonconfig.Obj{"file": file})
}

// newKeyValueFromJSONConfig returns a KeyValue implementation on top of a
// github.com/syndtr/goleveldb/leveldb file.
func newKeyValueFromJSONConfig(cfg jsonconfig.Obj) (sorted.KeyValue, error) {
	file := cfg.RequiredString("file")
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	strictness := opt.DefaultStrict
	if env.IsDev() {
		// Be more strict in dev mode.
		strictness = opt.StrictAll
	}
	opts := &opt.Options{
		// The default is 10,
		// 8 means 2.126% or 1/47th disk check rate,
		// 10 means 0.812% error rate (1/2^(bits/1.44)) or 1/123th disk check rate,
		// 12 means 0.31% or 1/322th disk check rate.
		// TODO(tgulacsi): decide which number is the best here. Till that go with the default.
		Filter: filter.NewBloomFilter(10),
		Strict: strictness,
	}
	db, err := leveldb.OpenFile(file, opts)
	if err != nil {
		return nil, err
	}
	is := &kvis{
		db:       db,
		path:     file,
		opts:     opts,
		readOpts: &opt.ReadOptions{Strict: strictness},
		// On machine crash we want to reindex anyway, and
		// fsyncs may impose great performance penalty.
		writeOpts: &opt.WriteOptions{Sync: false},
	}
	return is, nil
}

type kvis struct {
	path      string
	db        *leveldb.DB
	opts      *opt.Options
	readOpts  *opt.ReadOptions
	writeOpts *opt.WriteOptions
	txmu      sync.Mutex
}

func (is *kvis) Get(key string) (string, error) {
	val, err := is.db.Get([]byte(key), is.readOpts)
	if err != nil {
		if err == leveldb.ErrNotFound {
			return "", sorted.ErrNotFound
		}
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
	return is.db.Put([]byte(key), []byte(value), is.writeOpts)
}

func (is *kvis) Delete(key string) error {
	return is.db.Delete([]byte(key), is.writeOpts)
}

func (is *kvis) Find(start, end string) sorted.Iterator {
	var startB, endB []byte
	// A nil Range.Start is treated as a key before all keys in the DB.
	if start != "" {
		startB = []byte(start)
	}
	// A nil Range.Limit is treated as a key after all keys in the DB.
	if end != "" {
		endB = []byte(end)
	}
	it := &iter{
		it: is.db.NewIterator(
			&util.Range{Start: startB, Limit: endB},
			is.readOpts,
		),
	}
	return it
}

func (is *kvis) Wipe() error {
	// Close the already open DB.
	if err := is.db.Close(); err != nil {
		return err
	}
	if err := os.RemoveAll(is.path); err != nil {
		return err
	}

	db, err := leveldb.OpenFile(is.path, is.opts)
	if err != nil {
		return fmt.Errorf("error creating %s: %v", is.path, err)
	}
	is.db = db
	return nil
}

func (is *kvis) BeginBatch() sorted.BatchMutation {
	return &lvbatch{batch: new(leveldb.Batch)}
}

type lvbatch struct {
	errMu sync.Mutex
	err   error // Set if one of the mutations had too large a key or value. Sticky.

	batch *leveldb.Batch
}

func (lvb *lvbatch) Set(key, value string) {
	lvb.errMu.Lock()
	defer lvb.errMu.Unlock()
	if lvb.err != nil {
		return
	}
	if err := sorted.CheckSizes(key, value); err != nil {
		if err == sorted.ErrKeyTooLarge {
			lvb.err = fmt.Errorf("%v: %v", err, key)
		} else {
			lvb.err = fmt.Errorf("%v: %v", err, value)
		}
		return
	}
	lvb.batch.Put([]byte(key), []byte(value))
}

func (lvb *lvbatch) Delete(key string) {
	lvb.batch.Delete([]byte(key))
}

func (is *kvis) CommitBatch(bm sorted.BatchMutation) error {
	b, ok := bm.(*lvbatch)
	if !ok {
		return errors.New("invalid batch type")
	}
	b.errMu.Lock()
	defer b.errMu.Unlock()
	if b.err != nil {
		return b.err
	}
	return is.db.Write(b.batch, is.writeOpts)
}

func (is *kvis) Close() error {
	return is.db.Close()
}

type iter struct {
	it iterator.Iterator

	key, val   []byte
	skey, sval *string // for caching string values

	err    error
	closed bool
}

func (it *iter) Close() error {
	it.closed = true
	it.it.Release()
	return nil
}

func (it *iter) KeyBytes() []byte {
	return it.it.Key()
}

func (it *iter) Key() string {
	if it.skey != nil {
		return *it.skey
	}
	str := string(it.it.Key())
	it.skey = &str
	return str
}

func (it *iter) ValueBytes() []byte {
	return it.it.Value()
}

func (it *iter) Value() string {
	if it.sval != nil {
		return *it.sval
	}
	str := string(it.it.Value())
	it.sval = &str
	return str
}

func (it *iter) Next() bool {
	if err := it.it.Error(); err != nil {
		return false
	}
	if it.closed {
		panic("Next called after Next returned value")
	}
	it.skey, it.sval = nil, nil
	return it.it.Next()
}
