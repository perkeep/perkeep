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
	"path/filepath"
	"testing"

	"perkeep.org/pkg/index"
	"perkeep.org/pkg/index/indextest"
	"perkeep.org/pkg/sorted"
	"perkeep.org/pkg/sorted/kvfile"
	"perkeep.org/pkg/sorted/kvtest"
	"perkeep.org/pkg/test"
)

func newKvfileSorted(t *testing.T) sorted.KeyValue {
	td := t.TempDir()
	kv, err := kvfile.NewStorage(filepath.Join(td, "kvfile"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { kv.Close() })
	return kv
}

func TestSorted_Kvfile(t *testing.T) {
	kv := newKvfileSorted(t)
	kvtest.TestSorted(t, kv)
}

func indexTest(t *testing.T,
	sortedGenfn func(t *testing.T) sorted.KeyValue,
	tfn func(*testing.T, func() *index.Index)) {
	defer test.TLog(t)()

	makeIndex := func() *index.Index {
		s := sortedGenfn(t)
		return indextest.MustNew(t, s)
	}
	tfn(t, makeIndex)
}

func TestIndex_Kvfile(t *testing.T) {
	indexTest(t, newKvfileSorted, indextest.Index)
}

func TestPathsOfSignerTarget_Kvfile(t *testing.T) {
	indexTest(t, newKvfileSorted, indextest.PathsOfSignerTarget)
}

func TestFiles_Kvfile(t *testing.T) {
	indexTest(t, newKvfileSorted, indextest.Files)
}

func TestEdgesTo_Kvfile(t *testing.T) {
	indexTest(t, newKvfileSorted, indextest.EdgesTo)
}

func TestDelete_Kvfile(t *testing.T) {
	indexTest(t, newKvfileSorted, indextest.Delete)
}

func TestReindex_Kvfile(t *testing.T) {
	indexTest(t, newKvfileSorted, indextest.Reindex)
}

func TestEnumStat_Kvfile(t *testing.T) {
	indexTest(t, newKvfileSorted, indextest.EnumStat)
}
