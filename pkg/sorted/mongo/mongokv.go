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

// Package mongo provides an implementation of sorted.KeyValue
// using MongoDB.
package mongo

import (
	"bytes"
	"errors"
	"sync"
	"time"

	"camlistore.org/pkg/sorted"
	"go4.org/jsonconfig"

	"camlistore.org/third_party/labix.org/v2/mgo"
	"camlistore.org/third_party/labix.org/v2/mgo/bson"
)

// We explicitely separate the key and the value in a document,
// instead of simply storing as key:value, to avoid problems
// such as "." being an illegal char in a key name. Also because
// there is no way to do partial matching for key names (one can
// only check for their existence with bson.M{$exists: true}).
const (
	CollectionName = "keys" // MongoDB collection, equiv. to SQL table
	mgoKey         = "k"
	mgoValue       = "v"
)

func init() {
	sorted.RegisterKeyValue("mongo", newKeyValueFromJSONConfig)
}

func newKeyValueFromJSONConfig(cfg jsonconfig.Obj) (sorted.KeyValue, error) {
	ins := &instance{
		server:   cfg.OptionalString("host", "localhost"),
		database: cfg.RequiredString("database"),
		user:     cfg.OptionalString("user", ""),
		password: cfg.OptionalString("password", ""),
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	db, err := ins.getCollection()
	if err != nil {
		return nil, err
	}
	return &keyValue{db: db, session: ins.session}, nil
}

// Implementation of Iterator
type iter struct {
	res bson.M
	*mgo.Iter
	end []byte
}

func (it *iter) Next() bool {
	if !it.Iter.Next(&it.res) {
		return false
	}
	if len(it.end) > 0 && bytes.Compare(it.KeyBytes(), it.end) >= 0 {
		return false
	}
	return true
}

func (it *iter) Key() string {
	key, ok := (it.res[mgoKey]).(string)
	if !ok {
		return ""
	}
	return key
}

func (it *iter) KeyBytes() []byte {
	// TODO(bradfitz,mpl): this is less efficient than the string way. we should
	// do better here, somehow, like all the other KeyValue iterators.
	// For now:
	return []byte(it.Key())
}

func (it *iter) Value() string {
	value, ok := (it.res[mgoValue]).(string)
	if !ok {
		return ""
	}
	return value
}

func (it *iter) ValueBytes() []byte {
	// TODO(bradfitz,mpl): this is less efficient than the string way. we should
	// do better here, somehow, like all the other KeyValue iterators.
	// For now:
	return []byte(it.Value())
}

func (it *iter) Close() error {
	return it.Iter.Close()
}

// Implementation of KeyValue
type keyValue struct {
	session *mgo.Session // so we can close it
	mu      sync.Mutex   // guards db
	db      *mgo.Collection
}

func (kv *keyValue) Get(key string) (string, error) {
	kv.mu.Lock()
	defer kv.mu.Unlock()
	res := bson.M{}
	q := kv.db.Find(&bson.M{mgoKey: key})
	err := q.One(&res)
	if err != nil {
		if err == mgo.ErrNotFound {
			return "", sorted.ErrNotFound
		} else {
			return "", err
		}
	}
	return res[mgoValue].(string), err
}

func (kv *keyValue) Find(start, end string) sorted.Iterator {
	kv.mu.Lock()
	defer kv.mu.Unlock()
	it := kv.db.Find(&bson.M{mgoKey: &bson.M{"$gte": start}}).Sort(mgoKey).Iter()
	return &iter{res: bson.M{}, Iter: it, end: []byte(end)}
}

func (kv *keyValue) Set(key, value string) error {
	if err := sorted.CheckSizes(key, value); err != nil {
		return err
	}
	kv.mu.Lock()
	defer kv.mu.Unlock()
	_, err := kv.db.Upsert(&bson.M{mgoKey: key}, &bson.M{mgoKey: key, mgoValue: value})
	return err
}

// Delete removes the document with the matching key.
func (kv *keyValue) Delete(key string) error {
	kv.mu.Lock()
	defer kv.mu.Unlock()
	err := kv.db.Remove(&bson.M{mgoKey: key})
	if err == mgo.ErrNotFound {
		return nil
	}
	return err
}

// Wipe removes all documents from the collection.
func (kv *keyValue) Wipe() error {
	kv.mu.Lock()
	defer kv.mu.Unlock()
	_, err := kv.db.RemoveAll(nil)
	return err
}

type batch interface {
	Mutations() []sorted.Mutation
}

func (kv *keyValue) BeginBatch() sorted.BatchMutation {
	return sorted.NewBatchMutation()
}

func (kv *keyValue) CommitBatch(bm sorted.BatchMutation) error {
	b, ok := bm.(batch)
	if !ok {
		return errors.New("invalid batch type")
	}

	kv.mu.Lock()
	defer kv.mu.Unlock()
	for _, m := range b.Mutations() {
		if m.IsDelete() {
			if err := kv.db.Remove(bson.M{mgoKey: m.Key()}); err != nil {
				return err
			}
		} else {
			if err := sorted.CheckSizes(m.Key(), m.Value()); err != nil {
				return err
			}
			if _, err := kv.db.Upsert(&bson.M{mgoKey: m.Key()}, &bson.M{mgoKey: m.Key(), mgoValue: m.Value()}); err != nil {
				return err
			}
		}
	}
	return nil
}

func (kv *keyValue) Close() error {
	kv.session.Close()
	return nil
}

// Ping tests if MongoDB on host can be dialed.
func Ping(host string, timeout time.Duration) bool {
	return (&instance{server: host}).ping(timeout)
}

// instance helps with the low level details about
// the connection to MongoDB.
type instance struct {
	server   string
	database string
	user     string
	password string
	session  *mgo.Session
}

func (ins *instance) url() string {
	if ins.user == "" || ins.password == "" {
		return ins.server
	}
	return ins.user + ":" + ins.password + "@" + ins.server + "/" + ins.database
}

// ping won't work with old (1.2) mongo servers.
func (ins *instance) ping(timeout time.Duration) bool {
	session, err := mgo.DialWithTimeout(ins.url(), timeout)
	if err != nil {
		return false
	}
	defer session.Close()
	session.SetSyncTimeout(timeout)
	if err = session.Ping(); err != nil {
		return false
	}
	return true
}

func (ins *instance) getConnection() (*mgo.Session, error) {
	if ins.session != nil {
		return ins.session, nil
	}
	// TODO(mpl): do some "client caching" as in mysql, to avoid systematically dialing?
	session, err := mgo.Dial(ins.url())
	if err != nil {
		return nil, err
	}
	session.SetMode(mgo.Monotonic, true)
	session.SetSafe(&mgo.Safe{}) // so we get an ErrNotFound error when deleting an absent key
	ins.session = session
	return session, nil
}

// TODO(mpl): I'm only calling getCollection at the beginning, and
// keeping the collection around and reusing it everywhere, instead
// of calling getCollection everytime, because that's the easiest.
// But I can easily change that. Gustavo says it does not make
// much difference either way.
// Brad, what do you think?
func (ins *instance) getCollection() (*mgo.Collection, error) {
	session, err := ins.getConnection()
	if err != nil {
		return nil, err
	}
	session.SetSafe(&mgo.Safe{})
	session.SetMode(mgo.Strong, true)
	c := session.DB(ins.database).C(CollectionName)
	return c, nil
}
