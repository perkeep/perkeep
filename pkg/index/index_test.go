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
	"reflect"
	"strings"
	"testing"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/blobserver"
	"camlistore.org/pkg/index"
	"camlistore.org/pkg/index/indextest"
	"camlistore.org/pkg/sorted"
	"camlistore.org/pkg/test"
	"camlistore.org/pkg/types/camtypes"
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
	indextest.Index(t, index.NewMemoryIndex)
}

func TestPathsOfSignerTarget_Memory(t *testing.T) {
	indextest.PathsOfSignerTarget(t, index.NewMemoryIndex)
}

func TestFiles_Memory(t *testing.T) {
	indextest.Files(t, index.NewMemoryIndex)
}

func TestEdgesTo_Memory(t *testing.T) {
	indextest.EdgesTo(t, index.NewMemoryIndex)
}

func TestDelete_Memory(t *testing.T) {
	indextest.Delete(t, index.NewMemoryIndex)
}

var (
	// those test files are not specific to an indexer implementation
	// hence we do not want to check them.
	notAnIndexer = []string{
		"corpus_bench_test.go",
		"corpus_test.go",
		"export_test.go",
		"index_test.go",
		"keys_test.go",
	}
	// A map is used in hasAllRequiredTests to note which required
	// tests have been found in a package, by setting the corresponding
	// booleans to true. Those are the keys for this map.
	requiredTests = []string{"TestIndex_", "TestPathsOfSignerTarget_", "TestFiles_", "TestEdgesTo_"}
)

// This function checks that all the functions using the tests
// defined in indextest, namely:
// TestIndex_, TestPathOfSignerTarget_, TestFiles_
// do exist in the provided test file.
func hasAllRequiredTests(name string, t *testing.T) error {
	tests := make(map[string]bool)
	for _, v := range requiredTests {
		tests[v] = false
	}

	if !strings.HasSuffix(name, "_test.go") || skipFromList(name) {
		return nil
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, name, nil, 0)
	if err != nil {
		t.Fatalf("%v: %v", name, err)
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

	for k, v := range tests {
		if !v {
			return fmt.Errorf("%v not implemented in %v", k, name)
		}
	}
	return nil
}

// For each test file dedicated to an indexer implementation, this checks that
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
		if file.IsDir() || strings.HasPrefix(name, ".") {
			continue
		}
		if err := hasAllRequiredTests(name, t); err != nil {
			t.Error(err)
		}
	}
	// special case for sqlite as it is the only one left in its own package
	if err := hasAllRequiredTests(filepath.FromSlash("sqlite/sqlite_test.go"), t); err != nil {
		t.Error(err)
	}
}

func skipFromList(name string) bool {
	for _, v := range notAnIndexer {
		if name == v {
			return true
		}
	}
	return false
}

func testMergeFileInfoRow(t *testing.T, wholeRef string) {
	c := index.ExpNewCorpus()
	value := "100|something%2egif|image%2Fgif"
	want := camtypes.FileInfo{
		Size:     100,
		MIMEType: "image/gif",
		FileName: "something.gif",
	}
	if wholeRef != "" {
		value += "|" + wholeRef
		want.WholeRef = blob.MustParse(wholeRef)
	}
	c.Exp_mergeFileInfoRow("fileinfo|sha1-579f7f246bd420d486ddeb0dadbb256cfaf8bf6b", value)
	fi := c.Exp_files(blob.MustParse("sha1-579f7f246bd420d486ddeb0dadbb256cfaf8bf6b"))
	if !reflect.DeepEqual(want, fi) {
		t.Errorf("Got %+v; want %+v", fi, want)
	}
}

// When requiredSchemaVersion was at 4, i.e. wholeRef hadn't been introduced into fileInfo
func TestMergeFileInfoRow4(t *testing.T) {
	testMergeFileInfoRow(t, "")
}

func TestMergeFileInfoRow(t *testing.T) {
	testMergeFileInfoRow(t, "sha1-142b504945338158e0149d4ed25a41a522a28e88")
}

var (
	chunk1 = &test.Blob{Contents: "foo"}
	chunk2 = &test.Blob{Contents: "bar"}
	chunk3 = &test.Blob{Contents: "baz"}

	chunk1ref = chunk1.BlobRef()
	chunk2ref = chunk2.BlobRef()
	chunk3ref = chunk3.BlobRef()

	fileBlob = &test.Blob{fmt.Sprintf(`{"camliVersion": 1,
"camliType": "file",
"fileName": "stuff.txt",
"parts": [
  {"blobRef": "%s", "size": 3},
  {"blobRef": "%s", "size": 3},
  {"blobRef": "%s", "size": 3}
]}`, chunk1ref, chunk2ref, chunk3ref)}
	fileBlobRef = fileBlob.BlobRef()
)

func TestInitNeededMaps(t *testing.T) {
	s := sorted.NewMemoryKeyValue()

	// Start unknowning that the data chunks are all gone:
	s.Set("schemaversion", fmt.Sprint(index.Exp_schemaVersion()))
	s.Set(index.Exp_missingKey(fileBlobRef, chunk1ref), "1")
	s.Set(index.Exp_missingKey(fileBlobRef, chunk2ref), "1")
	s.Set(index.Exp_missingKey(fileBlobRef, chunk3ref), "1")
	ix, err := index.New(s)
	if err != nil {
		t.Fatal(err)
	}
	{
		needs, neededBy, _ := ix.NeededMapsForTest()
		needsWant := map[blob.Ref][]blob.Ref{
			fileBlobRef: []blob.Ref{chunk1ref, chunk2ref, chunk3ref},
		}
		neededByWant := map[blob.Ref][]blob.Ref{
			chunk1ref: []blob.Ref{fileBlobRef},
			chunk2ref: []blob.Ref{fileBlobRef},
			chunk3ref: []blob.Ref{fileBlobRef},
		}
		if !reflect.DeepEqual(needs, needsWant) {
			t.Errorf("needs = %v; want %v", needs, needsWant)
		}
		if !reflect.DeepEqual(neededBy, neededByWant) {
			t.Errorf("neededBy = %v; want %v", neededBy, neededByWant)
		}
	}

	ix.Exp_noteBlobIndexed(chunk2ref)

	{
		needs, neededBy, ready := ix.NeededMapsForTest()
		needsWant := map[blob.Ref][]blob.Ref{
			fileBlobRef: []blob.Ref{chunk1ref, chunk3ref},
		}
		neededByWant := map[blob.Ref][]blob.Ref{
			chunk1ref: []blob.Ref{fileBlobRef},
			chunk3ref: []blob.Ref{fileBlobRef},
		}
		if !reflect.DeepEqual(needs, needsWant) {
			t.Errorf("needs = %v; want %v", needs, needsWant)
		}
		if !reflect.DeepEqual(neededBy, neededByWant) {
			t.Errorf("neededBy = %v; want %v", neededBy, neededByWant)
		}
		if len(ready) != 0 {
			t.Errorf("ready = %v; want nothing", ready)
		}
	}

	ix.Exp_noteBlobIndexed(chunk1ref)

	{
		needs, neededBy, ready := ix.NeededMapsForTest()
		needsWant := map[blob.Ref][]blob.Ref{
			fileBlobRef: []blob.Ref{chunk3ref},
		}
		neededByWant := map[blob.Ref][]blob.Ref{
			chunk3ref: []blob.Ref{fileBlobRef},
		}
		if !reflect.DeepEqual(needs, needsWant) {
			t.Errorf("needs = %v; want %v", needs, needsWant)
		}
		if !reflect.DeepEqual(neededBy, neededByWant) {
			t.Errorf("neededBy = %v; want %v", neededBy, neededByWant)
		}
		if len(ready) != 0 {
			t.Errorf("ready = %v; want nothing", ready)
		}
	}

	ix.Exp_noteBlobIndexed(chunk3ref)

	{
		needs, neededBy, ready := ix.NeededMapsForTest()
		needsWant := map[blob.Ref][]blob.Ref{}
		neededByWant := map[blob.Ref][]blob.Ref{}
		if !reflect.DeepEqual(needs, needsWant) {
			t.Errorf("needs = %v; want %v", needs, needsWant)
		}
		if !reflect.DeepEqual(neededBy, neededByWant) {
			t.Errorf("neededBy = %v; want %v", neededBy, neededByWant)
		}
		if !ready[fileBlobRef] {
			t.Error("fileBlobRef not ready")
		}
	}
	dumpSorted(t, s)
}

func dumpSorted(t *testing.T, s sorted.KeyValue) {
	foreachSorted(t, s, func(k, v string) {
		t.Logf("index %q = %q", k, v)
	})
}

func foreachSorted(t *testing.T, s sorted.KeyValue, fn func(string, string)) {
	it := s.Find("", "")
	for it.Next() {
		fn(it.Key(), it.Value())
	}
	if err := it.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestOutOfOrderIndexing(t *testing.T) {
	tf := new(test.Fetcher)
	s := sorted.NewMemoryKeyValue()

	ix, err := index.New(s)
	if err != nil {
		t.Fatal(err)
	}
	ix.InitBlobSource(tf)

	t.Logf("file ref = %v", fileBlobRef)
	t.Logf("missing data chunks = %v, %v, %v", chunk1ref, chunk2ref, chunk3ref)

	add := func(b *test.Blob) {
		tf.AddBlob(b)
		if _, err := ix.ReceiveBlob(b.BlobRef(), b.Reader()); err != nil {
			t.Fatalf("ReceiveBlob(%v): %v", b.BlobRef(), err)
		}
	}

	add(fileBlob)

	{
		key := fmt.Sprintf("missing|%s|%s", fileBlobRef, chunk1ref)
		if got, err := s.Get(key); got == "" || err != nil {
			t.Errorf("key %q missing (err: %v); want 1", key, err)
		}
	}

	add(chunk1)
	add(chunk2)

	ix.Exp_AwaitReindexing(t)

	{
		key := fmt.Sprintf("missing|%s|%s", fileBlobRef, chunk3ref)
		if got, err := s.Get(key); got == "" || err != nil {
			t.Errorf("key %q missing (err: %v); want 1", key, err)
		}
	}

	add(chunk3)

	ix.Exp_AwaitReindexing(t)

	foreachSorted(t, s, func(k, v string) {
		if strings.HasPrefix(k, "missing|") {
			t.Errorf("Shouldn't have missing key: %q", k)
		}
	})
}

func TestIndexingClaimMissingPubkey(t *testing.T) {
	s := sorted.NewMemoryKeyValue()
	idx, err := index.New(s)
	if err != nil {
		t.Fatal(err)
	}

	id := indextest.NewIndexDeps(idx)
	id.Fataler = t

	goodKeyFetcher := id.Index.KeyFetcher
	emptyFetcher := new(test.Fetcher)

	pn := id.NewPermanode()

	// Prevent the index from being able to find the public key:
	idx.KeyFetcher = emptyFetcher

	// This previous failed to upload, since the signer's public key was
	// unavailable.
	claimRef := id.SetAttribute(pn, "tag", "foo")

	t.Logf(" Claim is %v", claimRef)
	t.Logf("Signer is %v", id.SignerBlobRef)

	// Verify that populateClaim noted the missing public key blob:
	{
		key := fmt.Sprintf("missing|%s|%s", claimRef, id.SignerBlobRef)
		if got, err := s.Get(key); got == "" || err != nil {
			t.Errorf("key %q missing (err: %v); want 1", key, err)
		}
	}

	// Now make it available again:
	idx.KeyFetcher = idx.Exp_BlobSource()

	if err := copyBlob(id.SignerBlobRef, idx.Exp_BlobSource().(*test.Fetcher), goodKeyFetcher); err != nil {
		t.Errorf("Error copying public key to BlobSource: %v", err)
	}
	if err := copyBlob(id.SignerBlobRef, idx, goodKeyFetcher); err != nil {
		t.Errorf("Error uploading public key to indexer: %v", err)
	}

	idx.Exp_AwaitReindexing(t)

	// Verify that populateClaim noted the missing public key blob:
	{
		key := fmt.Sprintf("missing|%s|%s", claimRef, id.SignerBlobRef)
		if got, err := s.Get(key); got != "" || err == nil {
			t.Errorf("row %q still exists", key)
		}
	}
}

func copyBlob(br blob.Ref, dst blobserver.BlobReceiver, src blob.Fetcher) error {
	rc, _, err := src.Fetch(br)
	if err != nil {
		return err
	}
	defer rc.Close()
	_, err = dst.ReceiveBlob(br, rc)
	return err
}

// tests that we add the missing wholeRef entries in FileInfo rows when going from
// a version 4 to a version 5 index.
func TestFixMissingWholeref(t *testing.T) {
	tf := new(test.Fetcher)
	s := sorted.NewMemoryKeyValue()

	ix, err := index.New(s)
	if err != nil {
		t.Fatal(err)
	}
	ix.InitBlobSource(tf)

	// populate with a file
	add := func(b *test.Blob) {
		tf.AddBlob(b)
		if _, err := ix.ReceiveBlob(b.BlobRef(), b.Reader()); err != nil {
			t.Fatalf("ReceiveBlob(%v): %v", b.BlobRef(), err)
		}
	}
	add(chunk1)
	add(chunk2)
	add(chunk3)
	add(fileBlob)

	// revert the row to the old form, by stripping the wholeRef suffix
	key := "fileinfo|" + fileBlobRef.String()
	val5, err := s.Get(key)
	if err != nil {
		t.Fatalf("could not get %v: %v", key, err)
	}
	parts := strings.SplitN(val5, "|", 4)
	val4 := strings.Join(parts[:3], "|")
	if err := s.Set(key, val4); err != nil {
		t.Fatalf("could not set (%v, %v): %v", key, val4, err)
	}

	// revert index version at 4 to trigger the fix
	if err := s.Set("schemaversion", "4"); err != nil {
		t.Fatal(err)
	}

	// init broken index
	ix, err = index.New(s)
	if err != index.Exp_ErrMissingWholeRef {
		t.Fatalf("wrong error upon index initialization: got %v, wanted %v", err, index.Exp_ErrMissingWholeRef)
	}
	// and fix it
	if err := ix.Exp_FixMissingWholeRef(tf); err != nil {
		t.Fatal(err)
	}

	// init fixed index
	ix, err = index.New(s)
	if err != nil {
		t.Fatal(err)
	}
	// and check that the value is now actually fixed
	fi, err := ix.GetFileInfo(fileBlobRef)
	if err != nil {
		t.Fatal(err)
	}
	if fi.WholeRef.String() != parts[3] {
		t.Fatalf("index fileInfo wholeref was not fixed: got %q, wanted %v", fi.WholeRef, parts[3])
	}
}
