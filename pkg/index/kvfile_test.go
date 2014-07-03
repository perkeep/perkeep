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
	"io/ioutil"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"camlistore.org/pkg/index"
	"camlistore.org/pkg/index/indextest"
	"camlistore.org/pkg/sorted"
	"camlistore.org/pkg/sorted/kvfile"
	"camlistore.org/pkg/sorted/kvtest"
	"camlistore.org/pkg/test"
)

func newKvfileSorted(t *testing.T) (kv sorted.KeyValue, cleanup func()) {
	td, err := ioutil.TempDir("", "kvfile-test")
	if err != nil {
		t.Fatal(err)
	}
	kv, err = kvfile.NewStorage(filepath.Join(td, "kvfile"))
	if err != nil {
		os.RemoveAll(td)
		t.Fatal(err)
	}
	return kv, func() {
		kv.Close()
		os.RemoveAll(td)
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
	var mu sync.Mutex // guards cleanups
	var cleanups []func()
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
		return index.MustNew(t, s)
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
