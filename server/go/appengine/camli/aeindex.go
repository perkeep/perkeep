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
	"errors"

	"camlistore.org/pkg/blobserver"
	"camlistore.org/pkg/index"
	"camlistore.org/pkg/jsonconfig"

	"appengine"
	"appengine/datastore"
)

var (
	indexRowKind = "IndexRow"
)

// A row of the index.  Keyed by "<namespace>|<keyname>"
type indexRowEnt struct {
	value []byte
}

type indexStorage struct {
	ns string
}

func (is *indexStorage) key(c appengine.Context, key string) *datastore.Key {
	return datastore.NewKey(c, indexRowKind, is.ns + "|" + key, 0, nil)
}

func (is *indexStorage) BeginBatch() index.BatchMutation {
	panic("TODO: impl")
}

func (is *indexStorage) CommitBatch(bm index.BatchMutation) error {
	return errors.New("TODO: impl")
}

func (is *indexStorage) Get(key string) (string, error) {
	c := getContext()
	defer putContext(c)

	panic("TODO: impl")
}

func (is *indexStorage) Set(key, value string) error {
	c := getContext()
	defer putContext(c)

	panic("TODO: impl")
}

func (is *indexStorage) Delete(key string) error {
	c := getContext()
	defer putContext(c)

	panic("TODO: impl")
}

func (is *indexStorage) Find(key string) index.Iterator {
	panic("TODO: impl")
}

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

	ix := index.New(is)
	ix.BlobSource = sto
	ix.KeyFetcher = ix.BlobSource // TODO(bradfitz): global search? something else?
	return ix, nil
}
