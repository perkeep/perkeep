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

package index_test

import (
	"testing"

	"camlistore.org/pkg/index/indextest"
	"camlistore.org/pkg/osutil"
	"camlistore.org/pkg/sorted"
	"camlistore.org/pkg/sorted/kvtest"
	_ "camlistore.org/pkg/sorted/mongo"
	"camlistore.org/pkg/test/dockertest"
	"go4.org/jsonconfig"
)

func newMongoSorted(t *testing.T) (kv sorted.KeyValue, cleanup func()) {
	dbname := "camlitest_" + osutil.Username()
	containerID, ip := dockertest.SetupMongoContainer(t)

	kv, err := sorted.NewKeyValue(jsonconfig.Obj{
		"type":     "mongo",
		"host":     ip,
		"database": dbname,
	})
	if err != nil {
		containerID.KillRemove(t)
		t.Fatal(err)
	}
	return kv, func() {
		kv.Close()
		containerID.KillRemove(t)
	}
}

func TestSorted_Mongo(t *testing.T) {
	kv, cleanup := newMongoSorted(t)
	defer cleanup()
	kvtest.TestSorted(t, kv)
}

func TestIndex_Mongo(t *testing.T) {
	indexTest(t, newMongoSorted, indextest.Index)
}

func TestPathsOfSignerTarget_Mongo(t *testing.T) {
	indexTest(t, newMongoSorted, indextest.PathsOfSignerTarget)
}

func TestFiles_Mongo(t *testing.T) {
	indexTest(t, newMongoSorted, indextest.Files)
}

func TestEdgesTo_Mongo(t *testing.T) {
	indexTest(t, newMongoSorted, indextest.EdgesTo)
}

func TestDelete_Mongo(t *testing.T) {
	indexTest(t, newMongoSorted, indextest.Delete)
}
