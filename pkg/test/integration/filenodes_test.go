/*
Copyright 2026 The Perkeep Authors

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

package integration

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/search"
	"perkeep.org/pkg/test"
)

// TestFileNodes checks that "pk-put file -filenodes" makes a permanode
// for each file, and the same one when run again, for files small enough
// not to be hashed whole by package upload, and bigger ones.
func TestFileNodes(t *testing.T) {
	w := test.GetWorld(t)

	dir := t.TempDir()
	for _, size := range []int{100, 300 << 10} {
		name := filepath.Join(dir, "file")
		if err := os.WriteFile(name, []byte(strings.Repeat("x", size-1)+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		mtime := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
		if err := os.Chtimes(name, mtime, mtime); err != nil {
			t.Fatal(err)
		}
		// It prints the permanode's ref, its claim's, and then the file
		// schema blob's.
		out := runCmd(t, w, "pk-put", "file", "-filenodes", name)
		refs := strings.Fields(out)
		if len(refs) != 3 {
			t.Fatalf("pk-put file output %q; want 3 blobrefs", out)
		}
		node, fileRef := blob.MustParse(refs[0]), blob.MustParse(refs[2])
		checkFileNode(t, w, fileRef, node)
		// A planned permanode depends only on the contents' hash.
		if want := wantFileNodes[size]; node.String() != want {
			t.Errorf("%d-byte file: permanode %v; want %v", size, node, want)
		}

		// Again, from the stat cache.
		out = runCmd(t, w, "pk-put", "file", "-filenodes", name)
		if refs := strings.Fields(out); len(refs) == 0 || refs[len(refs)-1] != fileRef.String() {
			t.Errorf("pk-put file again printed %q; want it to end with %v", out, fileRef)
		}
		checkFileNode(t, w, fileRef, node)
	}
}

// checkFileNode checks that node is the only permanode of the file of
// schema blob fileRef.
func checkFileNode(t *testing.T, w *test.World, fileRef, node blob.Ref) {
	t.Helper()
	sr := runCmd(t, w, "pk", "search", "attr:camliContent:"+fileRef.String())
	var res search.SearchResult
	if err := json.Unmarshal([]byte(sr), &res); err != nil {
		t.Fatalf("decoding pk search output %q: %v", sr, err)
	}
	if len(res.Blobs) != 1 || res.Blobs[0].Blob != node {
		t.Errorf("search for permanodes of %v found %s; want just %v", fileRef, sr, node)
	}
}

// wantFileNodes are the permanodes of TestFileNodes' files, by size, as
// pk-put made them before it uploaded files with package upload.
var wantFileNodes = map[int]string{
	100:       "sha224-754f842422b4f3a974f90c0c6c3976beeb194b47e0fb37f2fe6af737",
	300 << 10: "sha224-d75b1c67b9a81650afef61781574350d33e66c890fb3db398e21b6ca",
}
