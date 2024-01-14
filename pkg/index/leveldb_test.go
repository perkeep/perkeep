/*
Copyright 2016 The Perkeep Authors.

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
	"path/filepath"
	"testing"

	"perkeep.org/pkg/index/indextest"
	"perkeep.org/pkg/sorted"
	"perkeep.org/pkg/sorted/kvtest"
	"perkeep.org/pkg/sorted/leveldb"
)

func newLevelDBSorted(t *testing.T) sorted.KeyValue {
	td := t.TempDir()
	kv, err := leveldb.NewStorage(filepath.Join(td, "leveldb"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { kv.Close() })
	return kv
}

func TestSorted_LevelDB(t *testing.T) {
	kv := newLevelDBSorted(t)
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
	indexTest(t, newLevelDBSorted, indextest.Reindex)
}

func TestEnumStat_LevelDB(t *testing.T) {
	indexTest(t, newLevelDBSorted, indextest.EnumStat)
}
