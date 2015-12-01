// +build appengine

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

package appengine

import (
	"io"

	"camlistore.org/pkg/blobserver"
	"camlistore.org/pkg/index"
	"camlistore.org/pkg/sorted"
	"go4.org/jsonconfig"

	"appengine"
	"appengine/datastore"
)

const indexDebug = false

var (
	indexRowKind = "IndexRow"
)

// A row of the index.  Keyed by "<namespace>|<keyname>"
type indexRowEnt struct {
	Value []byte
}

type indexStorage struct {
	ns string
}

func (is *indexStorage) key(c appengine.Context, key string) *datastore.Key {
	return datastore.NewKey(c, indexRowKind, key, 0, datastore.NewKey(c, indexRowKind, is.ns, 0, nil))
}

func (is *indexStorage) BeginBatch() sorted.BatchMutation {
	return sorted.NewBatchMutation()
}

func (is *indexStorage) CommitBatch(bm sorted.BatchMutation) error {
	type mutationser interface {
		Mutations() []sorted.Mutation
	}
	var muts []sorted.Mutation
	if m, ok := bm.(mutationser); ok {
		muts = m.Mutations()
	} else {
		panic("unexpected type")
	}
	tryFunc := func(c appengine.Context) error {
		for _, m := range muts {
			dk := is.key(c, m.Key())
			if m.IsDelete() {
				if err := datastore.Delete(c, dk); err != nil {
					return err
				}
			} else {
				// A put.
				ent := &indexRowEnt{
					Value: []byte(m.Value()),
				}
				if _, err := datastore.Put(c, dk, ent); err != nil {
					return err
				}
			}
		}
		return nil
	}
	c := ctxPool.Get()
	defer c.Return()
	return datastore.RunInTransaction(c, tryFunc, crossGroupTransaction)
}

func (is *indexStorage) Get(key string) (string, error) {
	c := ctxPool.Get()
	defer c.Return()
	row := new(indexRowEnt)
	err := datastore.Get(c, is.key(c, key), row)
	if indexDebug {
		c.Infof("indexStorage.Get(%q) = %q, %v", key, row.Value, err)
	}
	if err != nil {
		if err == datastore.ErrNoSuchEntity {
			err = sorted.ErrNotFound
		}
		return "", err
	}
	return string(row.Value), nil
}

func (is *indexStorage) Set(key, value string) error {
	c := ctxPool.Get()
	defer c.Return()
	row := &indexRowEnt{
		Value: []byte(value),
	}
	_, err := datastore.Put(c, is.key(c, key), row)
	return err
}

func (is *indexStorage) Delete(key string) error {
	c := ctxPool.Get()
	defer c.Return()
	return datastore.Delete(c, is.key(c, key))
}

func (is *indexStorage) Find(start, end string) sorted.Iterator {
	c := ctxPool.Get()
	if indexDebug {
		c.Infof("IndexStorage Find(%q, %q)", start, end)
	}
	it := &iter{
		is:     is,
		cl:     c,
		after:  start,
		endKey: end,
		nsk:    datastore.NewKey(c, indexRowKind, is.ns, 0, nil),
	}
	it.Closer = &onceCloser{fn: func() {
		c.Return()
		it.nsk = nil
	}}
	return it
}

func (is *indexStorage) Close() error { return nil }

type iter struct {
	cl     ContextLoan
	after  string
	endKey string // optional
	io.Closer
	nsk *datastore.Key
	is  *indexStorage

	it *datastore.Iterator
	n  int // rows seen for this batch

	key, value string
	end        bool
}

func (it *iter) Next() bool {
	if it.nsk == nil {
		// already closed
		return false
	}
	if it.it == nil {
		q := datastore.NewQuery(indexRowKind).Filter("__key__>=", it.is.key(it.cl, it.after))
		if it.endKey != "" {
			q = q.Filter("__key__<", it.is.key(it.cl, it.endKey))
		}
		it.it = q.Run(it.cl)
		it.n = 0
	}
	var ent indexRowEnt
	key, err := it.it.Next(&ent)
	if indexDebug {
		it.cl.Infof("For after %q; key = %#v, err = %v", it.after, key, err)
	}
	if err == datastore.Done {
		if it.n == 0 {
			return false
		}
		return it.Next()
	}
	if err != nil {
		it.cl.Warningf("Error iterating over index after %q: %v", it.after, err)
		return false
	}
	it.n++
	it.key = key.StringID()
	it.value = string(ent.Value)
	it.after = it.key
	return true
}

func (it *iter) Key() string   { return it.key }
func (it *iter) Value() string { return it.value }

// TODO(bradfit): optimize the string<->[]byte copies in this iterator, as done in the other
// sorted.KeyValue iterators.
func (it *iter) KeyBytes() []byte   { return []byte(it.key) }
func (it *iter) ValueBytes() []byte { return []byte(it.value) }

func indexFromConfig(ld blobserver.Loader, config jsonconfig.Obj) (storage blobserver.Storage, err error) {
	is := &indexStorage{}
	var (
		blobPrefix = config.RequiredString("blobSource")
		ns         = config.OptionalString("namespace", "")
	)
	if err := config.Validate(); err != nil {
		return nil, err
	}
	sto, err := ld.GetStorage(blobPrefix)
	if err != nil {
		return nil, err
	}
	is.ns, err = sanitizeNamespace(ns)
	if err != nil {
		return nil, err
	}

	ix, err := index.New(is)
	if err != nil {
		return nil, err
	}
	ix.BlobSource = sto
	ix.KeyFetcher = ix.BlobSource // TODO(bradfitz): global search? something else?
	return ix, nil
}
