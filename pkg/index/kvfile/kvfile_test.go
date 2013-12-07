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

package kvfile_test

import (
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"

	"camlistore.org/pkg/index"
	"camlistore.org/pkg/index/indextest"
	"camlistore.org/pkg/index/kvfile"
	"camlistore.org/pkg/sorted"
	"camlistore.org/pkg/sorted/kvtest"
	"camlistore.org/pkg/test"
)

func newSorted(t *testing.T) (kv sorted.KeyValue, cleanup func()) {
	td, err := ioutil.TempDir("", "kvfile-test")
	if err != nil {
		t.Fatal(err)
	}
	is, err := kvfile.NewStorage(filepath.Join(td, "kvfile"))
	if err != nil {
		os.RemoveAll(td)
		t.Fatal(err)
	}
	return is, func() {
		is.Close()
		os.RemoveAll(td)
	}
}

func TestSortedKV(t *testing.T) {
	kv, cleanup := newSorted(t)
	defer cleanup()
	kvtest.TestSorted(t, kv)
}

type tester struct{}

func (tester) test(t *testing.T, tfn func(*testing.T, func() *index.Index)) {
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

func TestIndex_KV(t *testing.T) {
	tester{}.test(t, indextest.Index)
}

func TestPathsOfSignerTarget_KV(t *testing.T) {
	tester{}.test(t, indextest.PathsOfSignerTarget)
}

func TestFiles_KV(t *testing.T) {
	tester{}.test(t, indextest.Files)
}

func TestEdgesTo_KV(t *testing.T) {
	tester{}.test(t, indextest.EdgesTo)
}

func TestDelete_KV(t *testing.T) {
	tester{}.test(t, indextest.Delete)
}
