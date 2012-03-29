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
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"camlistore.org/pkg/index"
	"camlistore.org/pkg/index/indextest"
)

func TestReverseTimeString(t *testing.T) {
	in := "2011-11-27T01:23:45Z"
	got := index.ExpReverseTimeString(in)
	want := "rt7988-88-72T98:76:54Z"
	if got != want {
		t.Fatalf("reverseTimeString = %q, want %q", got, want)
	}
	back := index.ExpUnreverseTimeString(got)
	if back != in {
		t.Fatalf("unreverseTimeString = %q, want %q", back, in)
	}
}

func TestIndex_Memory(t *testing.T) {
	indextest.Index(t, index.ExpNewMemoryIndex)
}

func TestPathsOfSignerTarget_Memory(t *testing.T) {
	indextest.PathsOfSignerTarget(t, index.ExpNewMemoryIndex)
}

func TestFiles_Memory(t *testing.T) {
	indextest.Files(t, index.ExpNewMemoryIndex)
}

var (
	// those dirs are not packages implementing indexers,
	// hence we do not want to check them.
	excludedDirs = []string{"indextest", "testdata"}
	// A map is used in hasAllRequiredTests to note which required
	// tests have been found in a package, by setting the corresponding
	// booleans to true. Those are the keys for this map.
	requiredTests = []string{"TestIndex_", "TestPathsOfSignerTarget_", "TestFiles_"}
)

// This function checks that all the functions using the tests
// defined in indextest, namely:
// TestIndex_, TestPathOfSignerTarget_, TestFiles_
// do exist in the provided package.
func hasAllRequiredTests(path string, t *testing.T) error {
	tests := make(map[string]bool)
	for _, v := range requiredTests {
		tests[v] = false
	}
	dir, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	names, err := dir.Readdirnames(-1)
	if err != nil {
		t.Fatal(err)
	}
	defer dir.Close()

	for _, name := range names {
		if strings.HasPrefix(name, ".") || !strings.HasSuffix(name, "_test.go") {
			continue
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, filepath.Join(path, name), nil, 0)
		if err != nil {
			t.Fatalf("%v: %v", filepath.Join(path, name), err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.FuncDecl:
				name := x.Name.Name
				for k, _ := range tests {
					if strings.HasPrefix(name, k) {
						tests[k] = true
					}
				}
			}
			return true
		})
	}

	for k, v := range tests {
		if !v {
			return fmt.Errorf("%v not implemented in %v", k, path)
		}
	}
	return nil
}

// For each package implementing an indexer, this checks that
// all the required tests are present in its test suite.
func TestIndexerTestsCompleteness(t *testing.T) {
	cwd, err := os.Open(".")
	if err != nil {
		t.Fatal(err)
	}
	defer cwd.Close()
	files, err := cwd.Readdir(-1)
	if err != nil {
		t.Fatal(err)
	}

	for _, file := range files {
		name := file.Name()
		if !file.IsDir() || skipDir(name) {
			continue
		}
		if err := hasAllRequiredTests(name, t); err != nil {
			t.Error(err)
		}
	}
}

func skipDir(name string) bool {
	if strings.HasPrefix(name, "_") {
		return true
	}
	for _, v := range excludedDirs {
		if v == name {
			return true
		}
	}
	return false
}
