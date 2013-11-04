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
)

type tester struct{}

func (tester) test(t *testing.T, tfn func(*testing.T, func() *index.Index)) {
	var cleanup []func()
	defer func() {
		for _, fn := range cleanup {
			fn()
		}
	}()

	initIndex := func() *index.Index {
		td, err := ioutil.TempDir("", "kvfile-test")
		if err != nil {
			t.Fatal(err)
		}
		is, closer, err := kvfile.NewStorage(filepath.Join(td, "kvfile"))
		if err != nil {
			os.RemoveAll(td)
			t.Fatal(err)
		}
		cleanup = append(cleanup, func() {
			closer.Close()
			os.RemoveAll(td)
		})
		return index.New(is)
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

func TestIsDeleted_KV(t *testing.T) {
	tester{}.test(t, indextest.IsDeleted)
}

func TestDeletedAt_KV(t *testing.T) {
	tester{}.test(t, indextest.DeletedAt)
}
