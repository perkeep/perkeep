/*
Copyright 2011 The Perkeep Authors

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

	"go4.org/jsonconfig"
	"perkeep.org/internal/osutil"
	"perkeep.org/pkg/index/indextest"
	"perkeep.org/pkg/sorted"
	"perkeep.org/pkg/sorted/kvtest"
	_ "perkeep.org/pkg/sorted/mongo"
	"perkeep.org/pkg/test/dockertest"
)

func newMongoSorted(t *testing.T) sorted.KeyValue {
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
	t.Cleanup(func() {
		kv.Close()
		containerID.KillRemove(t)
	})
	return kv
}

func TestSorted_Mongo(t *testing.T) {
	kv := newMongoSorted(t)
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
