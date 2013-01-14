/*
Copyright 2011 The Camlistore Authors.

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

package mongo

import (
	"errors"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"camlistore.org/pkg/blobserver"
	"camlistore.org/pkg/index"
	"camlistore.org/pkg/jsonconfig"

	"camlistore.org/third_party/labix.org/v2/mgo"
	"camlistore.org/third_party/labix.org/v2/mgo/bson"
)

// We explicitely separate the key and the value in a document,
// instead of simply storing as key:value, to avoid problems
// such as "." being an illegal char in a key name. Also because
// there is no way to do partial matching for key names (one can
// only check for their existence with bson.M{$exists: true}).
const (
	collectionName = "keys"
	mgoKey         = "k"
	mgoValue       = "v"
)

type MongoWrapper struct {
	Servers    string
	User       string
	Password   string
	Database   string
	Collection string
}

func (mgw *MongoWrapper) url() string {
	if mgw.User == "" || mgw.Password == "" {
		return mgw.Servers
	}
	return mgw.User + ":" + mgw.Password + "@" + mgw.Servers + "/" + mgw.Database
}

// Note that Ping won't work with old (1.2) mongo servers.
func (mgw *MongoWrapper) TestConnection(timeout time.Duration) bool {
	session, err := mgo.DialWithTimeout(mgw.url(), timeout)
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

func (mgw *MongoWrapper) getConnection() (*mgo.Session, error) {
	// TODO(mpl): do some "client caching" as in mysql, to avoid systematically dialing?
	session, err := mgo.Dial(mgw.url())
	if err != nil {
		return nil, err
	}
	session.SetMode(mgo.Monotonic, true)
	session.SetSafe(&mgo.Safe{})
	return session, nil
}

// TODO(mpl): I'm only calling getCollection at the beginning, and
// keeping the collection around and reusing it everywhere, instead
// of calling getCollection everytime, because that's the easiest.
// But I can easily change that. Gustavo says it does not make
// much difference either way.
// Brad, what do you think?
func (mgw *MongoWrapper) getCollection() (*mgo.Collection, error) {
	session, err := mgw.getConnection()
	if err != nil {
		return nil, err
	}
	session.SetSafe(&mgo.Safe{})
	session.SetMode(mgo.Strong, true)
	c := session.DB(mgw.Database).C(mgw.Collection)
	return c, nil
}

func init() {
	blobserver.RegisterStorageConstructor("mongodbindexer",
		blobserver.StorageConstructor(newMongoIndexFromConfig))
}

func newMongoIndex(mgw *MongoWrapper) (*index.Index, error) {
	db, err := mgw.getCollection()
	if err != nil {
		return nil, err
	}
	mongoStorage := &mongoKeys{db: db}
	return index.New(mongoStorage), nil
}

func newMongoIndexFromConfig(ld blobserver.Loader, config jsonconfig.Obj) (blobserver.Storage, error) {
	blobPrefix := config.RequiredString("blobSource")
	mgw := &MongoWrapper{
		Servers:    config.OptionalString("host", "localhost"),
		Database:   config.RequiredString("database"),
		User:       config.OptionalString("user", ""),
		Password:   config.OptionalString("password", ""),
		Collection: collectionName,
	}
	if err := config.Validate(); err != nil {
		return nil, err
	}
	sto, err := ld.GetStorage(blobPrefix)
	if err != nil {
		return nil, err
	}

	ix, err := newMongoIndex(mgw)
	if err != nil {
		return nil, err
	}
	ix.BlobSource = sto

	// Good enough, for now:
	ix.KeyFetcher = ix.BlobSource

	if wipe := os.Getenv("CAMLI_MONGO_WIPE"); wipe != "" {
		dowipe, err := strconv.ParseBool(wipe)
		if err != nil {
			return nil, err
		}
		if dowipe {
			err = ix.Storage().Delete("")
			if err != nil {
				return nil, err
			}
		}
	}

	return ix, err
}

// Implementation of index Iterator
type mongoStrIterator struct {
	res bson.M
	*mgo.Iter
}

func (s mongoStrIterator) Next() bool {
	return s.Iter.Next(&s.res)
}

func (s mongoStrIterator) Key() (key string) {
	key, ok := (s.res[mgoKey]).(string)
	if !ok {
		return ""
	}
	return key
}

func (s mongoStrIterator) Value() (value string) {
	value, ok := (s.res[mgoValue]).(string)
	if !ok {
		return ""
	}
	return value
}

func (s mongoStrIterator) Close() error {
	// TODO(mpl): think about anything more to be done here.
	return nil
}

// Implementation of index.Storage
type mongoKeys struct {
	mu sync.Mutex // guards db
	db *mgo.Collection
}

func (mk *mongoKeys) Get(key string) (string, error) {
	mk.mu.Lock()
	defer mk.mu.Unlock()
	res := bson.M{}
	q := mk.db.Find(&bson.M{mgoKey: key})
	err := q.One(&res)
	if err != nil {
		if err == mgo.ErrNotFound {
			return "", index.ErrNotFound
		} else {
			return "", err
		}
	}
	return res[mgoValue].(string), err
}

func (mk *mongoKeys) Find(key string) index.Iterator {
	mk.mu.Lock()
	defer mk.mu.Unlock()
	// TODO(mpl): escape other special chars, or maybe replace $regex with something
	// more suited if possible.
	cleanedKey := strings.Replace(key, "|", `\|`, -1)
	iter := mk.db.Find(&bson.M{mgoKey: &bson.M{"$regex": "^" + cleanedKey}}).Sort(mgoKey).Iter()
	return mongoStrIterator{res: bson.M{}, Iter: iter}
}

func (mk *mongoKeys) Set(key, value string) error {
	mk.mu.Lock()
	defer mk.mu.Unlock()
	_, err := mk.db.Upsert(&bson.M{mgoKey: key}, &bson.M{mgoKey: key, mgoValue: value})
	return err
}

// Delete removes the document with the matching key.
// If key is "", it removes all documents.
func (mk *mongoKeys) Delete(key string) error {
	mk.mu.Lock()
	defer mk.mu.Unlock()
	if key == "" {
		_, err := mk.db.RemoveAll(nil)
		return err
	}
	return mk.db.Remove(&bson.M{mgoKey: key})
}

func (mk *mongoKeys) BeginBatch() index.BatchMutation {
	return index.NewBatchMutation()
}

type batch interface {
	Mutations() []index.Mutation
}

func (mk *mongoKeys) CommitBatch(bm index.BatchMutation) error {
	b, ok := bm.(batch)
	if !ok {
		return errors.New("invalid batch type")
	}

	mk.mu.Lock()
	defer mk.mu.Unlock()
	for _, m := range b.Mutations() {
		if m.IsDelete() {
			if err := mk.db.Remove(bson.M{mgoKey: m.Key()}); err != nil {
				return err
			}
		} else {
			if _, err := mk.db.Upsert(&bson.M{mgoKey: m.Key()}, &bson.M{mgoKey: m.Key(), mgoValue: m.Value()}); err != nil {
				return err
			}
		}
	}
	return nil
}
