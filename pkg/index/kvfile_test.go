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
	"sync"
	"testing"

	"perkeep.org/pkg/index"
	"perkeep.org/pkg/index/indextest"
	"perkeep.org/pkg/sorted"
	"perkeep.org/pkg/sorted/kvfile"
	"perkeep.org/pkg/sorted/kvtest"
	"perkeep.org/pkg/test"
)

func newKvfileSorted(t *testing.T) (kv sorted.KeyValue, cleanup func()) {
	td := t.TempDir()
	kv, err := kvfile.NewStorage(filepath.Join(td, "kvfile"))
	if err != nil {
		t.Fatal(err)
	}
	return kv, func() {
		kv.Close()
	}
}

func TestSorted_Kvfile(t *testing.T) {
	kv, cleanup := newKvfileSorted(t)
	defer cleanup()
	kvtest.TestSorted(t, kv)
}

func indexTest(t *testing.T,
	sortedGenfn func(t *testing.T) (sorted.KeyValue, func()),
	tfn func(*testing.T, func() *index.Index)) {
	defer test.TLog(t)()
	var (
		mu       sync.Mutex // guards cleanups
		cleanups []func()
	)
	defer func() {
		mu.Lock() // never unlocked
		for _, fn := range cleanups {
			fn()
		}
	}()
	makeIndex := func() *index.Index {
		s, cleanup := sortedGenfn(t)
		mu.Lock()
		cleanups = append(cleanups, cleanup)
		mu.Unlock()
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

func TestShowReindexRace_Kvfile(t *testing.T) {
	indexTest(t, newKvfileSorted, indextest.ShowReindexRace)
}

func TestEnumStat_Kvfile(t *testing.T) {
	indexTest(t, newKvfileSorted, indextest.EnumStat)
}
