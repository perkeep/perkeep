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
