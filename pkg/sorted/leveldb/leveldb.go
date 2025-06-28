/*
Copyright 2014 The Perkeep Authors.

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
package leveldb // import "perkeep.org/pkg/sorted/leveldb"

import (
	"errors"
	"fmt"
	"log"
	"os"
	"sync"

	"go4.org/jsonconfig"
	"perkeep.org/pkg/env"
	"perkeep.org/pkg/sorted"

	"github.com/syndtr/goleveldb/leveldb"
	"github.com/syndtr/goleveldb/leveldb/filter"
	"github.com/syndtr/goleveldb/leveldb/iterator"
	"github.com/syndtr/goleveldb/leveldb/opt"
	"github.com/syndtr/goleveldb/leveldb/util"
)

var _ sorted.Wiper = (*kvis)(nil)

func init() {
	sorted.RegisterKeyValue("leveldb", newKeyValueFromJSONConfig)
}

// NewStorage is a convenience that calls newKeyValueFromJSONConfig
// with file as the leveldb storage directory.
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
}

func (is *kvis) Get(key string) (string, error) {
	return getCommon(is.db, is.readOpts, key)
}

func (is *kvis) Set(key, value string) error {
	if err := sorted.CheckSizes(key, value); err != nil {
		log.Printf("Skipping storing (%q:%q): %v", key, value, err)
		return nil
	}
	return is.db.Put([]byte(key), []byte(value), is.writeOpts)
}

func (is *kvis) Delete(key string) error {
	return is.db.Delete([]byte(key), is.writeOpts)
}

func (is *kvis) Find(start, end string) sorted.Iterator {
	return findCommon(is.db, is.readOpts, start, end)
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

func (is *kvis) BeginReadTx() sorted.ReadTransaction {
	snapshot, err := is.db.GetSnapshot()
	return &lvsnapshot{
		snapshot: snapshot,
		readOpts: is.readOpts,
		err:      err,
	}
}

type lvsnapshot struct {
	snapshot *leveldb.Snapshot
	readOpts *opt.ReadOptions
	err      error
}

func (s *lvsnapshot) Get(key string) (string, error) {
	if s.err != nil {
		return "", s.err
	}
	return getCommon(s.snapshot, s.readOpts, key)
}

func (s *lvsnapshot) Find(start, end string) sorted.Iterator {
	if s.err != nil {
		return errIter{err: s.err}
	}
	return findCommon(s.snapshot, s.readOpts, start, end)
}

// An errIter is a dummy iterator that conceptually has errored. We need
// this because the Find() method of sorted.KeyValue doesn't provide a way
// to return an error, but an lvsnapshot may be in an errored state.
type errIter struct {
	err error
}

func (errIter) Next() bool         { return false }
func (errIter) Key() string        { return "" }
func (errIter) KeyBytes() []byte   { return nil }
func (errIter) Value() string      { return "" }
func (errIter) ValueBytes() []byte { return nil }

func (e errIter) Close() error {
	return e.err
}

func (s *lvsnapshot) Close() error {
	s.snapshot.Release()
	return nil
}

func getCommon(r leveldb.Reader, readOpts *opt.ReadOptions, key string) (string, error) {
	val, err := r.Get([]byte(key), readOpts)
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

func findCommon(r leveldb.Reader, readOpts *opt.ReadOptions, start, end string) sorted.Iterator {
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
		it: r.NewIterator(
			&util.Range{Start: startB, Limit: endB},
			readOpts,
		),
	}
	return it
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
		log.Printf("Skipping storing (%q:%q): %v", key, value, err)
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

	skey, sval *string // for caching string values

	// closed is not strictly necessary, but helps us detect programmer
	// errors like calling Next on a Closed iterator (which panics).
	closed bool
}

func (it *iter) Close() error {
	it.closed = true
	it.it.Release()
	return it.it.Error()
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
	if it.closed {
		panic("Next called on closed iterator")
	}
	it.skey, it.sval = nil, nil
	return it.it.Next()
}
