/*
Copyright 2016 The Camlistore Authors.

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
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"
	"time"

	"camlistore.org/pkg/index/indextest"
	"camlistore.org/pkg/sorted"
	"camlistore.org/pkg/sorted/kvtest"
	"camlistore.org/pkg/sorted/leveldb"
)

func newLevelDBSorted(t *testing.T) (kv sorted.KeyValue, cleanup func()) {
	td, err := ioutil.TempDir("", "camli-index-leveldb")
	if err != nil {
		t.Fatal(err)
	}
	kv, err = leveldb.NewStorage(filepath.Join(td, "leveldb"))
	if err != nil {
		os.RemoveAll(td)
		t.Fatal(err)
	}
	return kv, func() {
		kv.Close()
		os.RemoveAll(td)
	}
}

func TestSorted_LevelDB(t *testing.T) {
	kv, cleanup := newLevelDBSorted(t)
	defer cleanup()
	kvtest.TestSorted(t, kv)
}

func TestIndex_LevelDB(t *testing.T) {
	indexTest(t, newLevelDBSorted, indextest.Index)
}

func TestPathsOfSignerTarget_LevelDB(t *testing.T) {
	indexTest(t, newLevelDBSorted, indextest.PathsOfSignerTarget)
}

func TestFiles_LevelDB(t *testing.T) {
	indexTest(t, newLevelDBSorted, indextest.Files)
}

func TestEdgesTo_LevelDB(t *testing.T) {
	indexTest(t, newLevelDBSorted, indextest.EdgesTo)
}

func TestDelete_LevelDB(t *testing.T) {
	indexTest(t, newLevelDBSorted, indextest.Delete)
}

func TestReindex_LevelDB(t *testing.T) {
	t.Skip("Disabled until issue #756 is fixed")
	t.Log("WARNING: as this test can get into an infinite loop, it will automatically terminate after a few seconds")
	tim := time.After(2 * time.Second)
	c := make(chan struct{}, 1)
	go func() {
		indexTest(t, newLevelDBSorted, indextest.Reindex)
		c <- struct{}{}
	}()
	select {
	case <-c:
		// all good
	case <-tim:
		// Because of at least (I suspect) issue #756, we not only
		// sometimes get a failing test here, but we also get into an
		// infinite loop retrying out-of-order indexing.Hence the Fatal
		// below as a temporary measure to interrupt that loop.
		t.Fatal("forced interruption of TestReindex_LevelDB infinite loop")
	}
}

func TestEnumStat_LevelDB(t *testing.T) {
	indexTest(t, newLevelDBSorted, indextest.EnumStat)
}
