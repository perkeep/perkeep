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

package mongo_test

import (
	"errors"
	"sync"
	"testing"
	"time"

	"camlistore.org/pkg/index"
	"camlistore.org/pkg/index/indextest"
	"camlistore.org/pkg/sorted"
	"camlistore.org/pkg/sorted/kvtest"
	"camlistore.org/pkg/sorted/mongo"
	"camlistore.org/pkg/test"
)

var (
	once              sync.Once
	mongoNotAvailable bool
)

func checkMongoUp() {
	mongoNotAvailable = !mongo.Ping("localhost", 500*time.Millisecond)
}

func skipOrFailIfNoMongo(t *testing.T) {
	once.Do(checkMongoUp)
	if mongoNotAvailable {
		err := errors.New("Not running; start a mongoDB daemon on the standard port (27017). The \"keys\" collection in the \"camlitest\" database will be used.")
		test.DependencyErrorOrSkip(t)
		t.Fatalf("Mongo not available locally for testing: %v", err)
	}
}

func newSorted(t *testing.T) (kv sorted.KeyValue, cleanup func()) {
	skipOrFailIfNoMongo(t)

	// connect without credentials and wipe the database
	cfg := mongo.Config{
		Server:   "localhost",
		Database: "camlitest",
	}
	var err error
	kv, err = mongo.NewKeyValue(cfg)
	if err != nil {
		t.Fatal(err)
	}
	wiper, ok := kv.(sorted.Wiper)
	if !ok {
		panic("mongo KeyValue not a Wiper")
	}
	err = wiper.Wipe()
	if err != nil {
		t.Fatal(err)
	}
	return kv, func() {
		kv.Close()
	}
}

func TestSortedKV(t *testing.T) {
	kv, cleanup := newSorted(t)
	defer cleanup()
	kvtest.TestSorted(t, kv)
}

type mongoTester struct{}

func (mongoTester) test(t *testing.T, tfn func(*testing.T, func() *index.Index)) {
	skipOrFailIfNoMongo(t)
	defer test.TLog(t)()
	var cleanups []func()
	defer func() {
		for _, fn := range cleanups {
			fn()
		}
	}()
	initIndex := func() *index.Index {
		kv, cleanup := newSorted(t)
		cleanups = append(cleanups, cleanup)
		return index.New(kv)
	}
	tfn(t, initIndex)
}

func TestIndex_Mongo(t *testing.T) {
	mongoTester{}.test(t, indextest.Index)
}

func TestPathsOfSignerTarget_Mongo(t *testing.T) {
	mongoTester{}.test(t, indextest.PathsOfSignerTarget)
}

func TestFiles_Mongo(t *testing.T) {
	mongoTester{}.test(t, indextest.Files)
}

func TestEdgesTo_Mongo(t *testing.T) {
	mongoTester{}.test(t, indextest.EdgesTo)
}

func TestDelete_Mongo(t *testing.T) {
	mongoTester{}.test(t, indextest.Delete)
}
