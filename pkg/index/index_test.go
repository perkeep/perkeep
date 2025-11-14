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
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver"
	"perkeep.org/pkg/index"
	"perkeep.org/pkg/index/indextest"
	"perkeep.org/pkg/schema"
	"perkeep.org/pkg/sorted"
	"perkeep.org/pkg/test"
	"perkeep.org/pkg/types/camtypes"
)

var ctxbg = context.Background()

var (
	chunk1, chunk2, chunk3, fileBlob, staticSetBlob, dirBlob *test.Blob
	chunk1ref, chunk2ref, chunk3ref, fileBlobRef             blob.Ref
)

func init() {
	chunk1 = &test.Blob{Contents: "foo"}
	chunk2 = &test.Blob{Contents: "bar"}
	chunk3 = &test.Blob{Contents: "baz"}

	chunk1ref = chunk1.BlobRef()
	chunk2ref = chunk2.BlobRef()
	chunk3ref = chunk3.BlobRef()

	fileBlob = &test.Blob{Contents: fmt.Sprintf(`{"camliVersion": 1,
"camliType": "file",
"fileName": "stuff.txt",
"parts": [
  {"blobRef": "%s", "size": 3},
  {"blobRef": "%s", "size": 3},
  {"blobRef": "%s", "size": 3}
]}`, chunk1ref, chunk2ref, chunk3ref)}
	fileBlobRef = fileBlob.BlobRef()

	staticSetBlob = &test.Blob{Contents: fmt.Sprintf(`{"camliVersion": 1,
"camliType": "static-set",
"members": [
  "%s"
]}`, fileBlobRef)}

	dirBlob = &test.Blob{Contents: fmt.Sprintf(`{"camliVersion": 1,
"camliType": "directory",
"fileName": "someDir",
"entries": "%s"
}`, staticSetBlob.BlobRef())}
}

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
		"util_test.go",
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
			for k := range tests {
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
	return slices.Contains(notAnIndexer, name)
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
	c.Exp_mergeFileInfoRow("fileinfo|sha224-d78d192115bd8773d7b98b7b9812d1c9d137e8a930041e04a03b8428", value)
	fi := c.Exp_files(blob.MustParse("sha224-d78d192115bd8773d7b98b7b9812d1c9d137e8a930041e04a03b8428"))
	if !reflect.DeepEqual(want, fi) {
		t.Errorf("Got %+v; want %+v", fi, want)
	}
}

// When requiredSchemaVersion was at 4, i.e. wholeRef hadn't been introduced into fileInfo
func TestMergeFileInfoRow4(t *testing.T) {
	testMergeFileInfoRow(t, "")
}

func TestMergeFileInfoRow(t *testing.T) {
	testMergeFileInfoRow(t, "sha224-7032b0e01d39d3eac638f74512e19580212fc13bf846524d5eb6d1cb")
}

func TestInitNeededMaps(t *testing.T) {
	s := sorted.NewMemoryKeyValue()

	// Start unknowning that the data chunks are all gone:
	s.Set("schemaversion", fmt.Sprint(index.Exp_schemaVersion()))
	s.Set(index.Exp_missingKey(fileBlobRef, chunk1ref), "1")
	s.Set(index.Exp_missingKey(fileBlobRef, chunk2ref), "1")
	s.Set(index.Exp_missingKey(fileBlobRef, chunk3ref), "1")
	// Add fileBlob to the blobSource, so the out-of-order indexing can
	// succeed when the chunk3ref is finally added to the index. We technically
	// don't need to do that for this unit test, but that's one less log
	// message to deal with (the ooo indexing failing) if we do.
	bs := new(test.Fetcher)
	bs.AddBlob(fileBlob)
	ix, err := index.New(s)
	if err != nil {
		t.Fatal(err)
	}
	ix.InitBlobSource(bs)
	{
		ix.Lock()
		needs, neededBy, _ := ix.NeededMapsForTest()
		needsWant := map[blob.Ref][]blob.Ref{
			fileBlobRef: {chunk2ref, chunk1ref, chunk3ref},
		}
		if !reflect.DeepEqual(needs, needsWant) {
			t.Errorf("needs = %v; want \n%v", needs, needsWant)
		}
		neededByWant := map[blob.Ref][]blob.Ref{
			chunk1ref: {fileBlobRef},
			chunk2ref: {fileBlobRef},
			chunk3ref: {fileBlobRef},
		}
		if !reflect.DeepEqual(neededBy, neededByWant) {
			t.Errorf("neededBy = %v; \nwant %v", neededBy, neededByWant)
		}
		ix.Unlock()
	}

	ix.Exp_noteBlobIndexed(chunk2ref)

	{
		ix.Lock()
		needs, neededBy, ready := ix.NeededMapsForTest()
		needsWant := map[blob.Ref][]blob.Ref{
			fileBlobRef: {chunk1ref, chunk3ref},
		}
		neededByWant := map[blob.Ref][]blob.Ref{
			chunk1ref: {fileBlobRef},
			chunk3ref: {fileBlobRef},
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
		ix.Unlock()
	}

	ix.Exp_noteBlobIndexed(chunk1ref)

	{
		ix.Lock()
		needs, neededBy, ready := ix.NeededMapsForTest()
		needsWant := map[blob.Ref][]blob.Ref{
			fileBlobRef: {chunk3ref},
		}
		neededByWant := map[blob.Ref][]blob.Ref{
			chunk3ref: {fileBlobRef},
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
		ix.Unlock()
	}

	ix.Exp_noteBlobIndexed(chunk3ref)

	{
		ix.Lock()
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
		ix.Unlock()
	}
	// We also technically don't need to wait for the ooo indexing goroutine
	// to finish for this unit test, but it's cleaner.
	ix.Exp_AwaitAsyncIndexing(t)
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

type testSequence struct {
	add        []*test.Blob // chunks to add
	dependency blob.Ref     // for checking against a "missing" line
	dependee   blob.Ref     // for checking against a "missing" line
	wait       bool         // whether to wait for async reindexing
}

func testOutOfOrderIndexing(t *testing.T, sequence []testSequence) {
	tf := new(test.Fetcher)
	s := sorted.NewMemoryKeyValue()

	ix, err := index.New(s)
	if err != nil {
		t.Fatal(err)
	}
	ix.InitBlobSource(tf)

	add := func(b *test.Blob) {
		tf.AddBlob(b)
		if _, err := ix.ReceiveBlob(ctxbg, b.BlobRef(), b.Reader()); err != nil {
			t.Fatalf("ReceiveBlob(%v): %v", b.BlobRef(), err)
		}
	}

	for _, seq := range sequence {
		for _, b := range seq.add {
			add(b)
		}
		if seq.wait {
			ix.Exp_AwaitAsyncIndexing(t)
		}
		if seq.dependee.Valid() && seq.dependency.Valid() {
			{
				key := fmt.Sprintf("missing|%s|%s", seq.dependee, seq.dependency)
				if got, err := s.Get(key); got == "" || err != nil {
					t.Errorf("key %q missing (err: %v); want 1", key, err)
				}
			}
		}
	}

	foreachSorted(t, s, func(k, v string) {
		if strings.HasPrefix(k, "missing|") {
			t.Errorf("Shouldn't have missing key: %q", k)
		}
	})

}

func TestOutOfOrderIndexingFile(t *testing.T) {
	t.Logf("file ref = %v", fileBlobRef)
	t.Logf("missing data chunks = %v, %v, %v", chunk1ref, chunk2ref, chunk3ref)
	testOutOfOrderIndexing(t, []testSequence{
		{
			add:        []*test.Blob{fileBlob},
			wait:       false,
			dependee:   fileBlobRef,
			dependency: chunk1ref,
		},
		{
			add:        []*test.Blob{chunk1, chunk2},
			wait:       true,
			dependee:   fileBlobRef,
			dependency: chunk3ref,
		},
		{
			add:  []*test.Blob{chunk3},
			wait: true,
		},
	})
}

func TestOutOfOrderIndexingDirectory(t *testing.T) {
	testOutOfOrderIndexing(t, []testSequence{
		{
			add:        []*test.Blob{chunk1, chunk2, chunk3, fileBlob, dirBlob},
			wait:       true,
			dependee:   dirBlob.BlobRef(),
			dependency: staticSetBlob.BlobRef(),
		},
		{
			add:  []*test.Blob{staticSetBlob},
			wait: true,
		},
	})
}

func TestIndexingPermanodeBadSignature(t *testing.T) {
	s := sorted.NewMemoryKeyValue()
	idx, err := index.New(s)
	if err != nil {
		t.Fatal(err)
	}

	id := indextest.NewIndexDeps(idx)
	id.Fataler = t

	// Create a new permanode and break the signature
	goodPermanode := id.Sign(schema.NewPlannedPermanode("replaceme"))
	badPermanode := &test.Blob{
		Contents: strings.ReplaceAll(goodPermanode.Contents, "replaceme", "replaced"),
	}

	// We can upload it, but the indexing should fail.
	if _, err := id.BlobSource.ReceiveBlob(ctxbg, badPermanode.BlobRef(), badPermanode.Reader()); err != nil {
		t.Fatalf("public uploading signed blob to blob source, pre-indexing: %v, %v", badPermanode.BlobRef(), err)
	}
	if _, err = id.Index.ReceiveBlob(ctxbg, badPermanode.BlobRef(), badPermanode.Reader()); err == nil {
		t.Fatalf("Successfully indexed a permanode with a bad signature")
	} else if !strings.Contains(err.Error(), "signature verification failed") {
		t.Fatalf("Expected signature error, got: %v", err)
	}

	// Now upload the good one, everything is OK.
	if _, err := id.BlobSource.ReceiveBlob(ctxbg, goodPermanode.BlobRef(), goodPermanode.Reader()); err != nil {
		t.Fatalf("public uploading signed blob to blob source, pre-indexing: %v, %v", goodPermanode.BlobRef(), err)
	}
	if _, err = id.Index.ReceiveBlob(ctxbg, goodPermanode.BlobRef(), goodPermanode.Reader()); err != nil {
		t.Fatalf("Failed to index the good permanode: %v", err)
	}

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

	idx.Exp_AwaitAsyncIndexing(t)

	// Verify that populateClaim noted the missing public key blob:
	{
		key := fmt.Sprintf("missing|%s|%s", claimRef, id.SignerBlobRef)
		if got, err := s.Get(key); got != "" || err == nil {
			t.Errorf("row %q still exists", key)
		}
	}
}

func TestIndexingPermanodeMissingPubkey(t *testing.T) {
	s := sorted.NewMemoryKeyValue()
	idx, err := index.New(s)
	if err != nil {
		t.Fatal(err)
	}

	id := indextest.NewIndexDeps(idx)
	id.Fataler = t

	goodKeyFetcher := id.Index.KeyFetcher
	emptyFetcher := new(test.Fetcher)

	// Prevent the index from being able to find the public key:
	idx.KeyFetcher = emptyFetcher

	pn := id.NewPermanode()

	// Verify that populateClaim noted the missing public key blob:
	{
		key := fmt.Sprintf("missing|%s|%s", pn, id.SignerBlobRef)
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

	idx.Exp_AwaitAsyncIndexing(t)

	// Verify that populateClaim noted the now present public key blob:
	{
		key := fmt.Sprintf("missing|%s|%s", pn, id.SignerBlobRef)
		if got, err := s.Get(key); got != "" || err == nil {
			t.Errorf("row %q still exists", key)
		}
	}
}

func copyBlob(br blob.Ref, dst blobserver.BlobReceiver, src blob.Fetcher) error {
	rc, _, err := src.Fetch(ctxbg, br)
	if err != nil {
		return err
	}
	defer rc.Close()
	_, err = dst.ReceiveBlob(ctxbg, br, rc)
	return err
}

// tests that we add the missing wholeRef entries in FileInfo rows when going from
// a version 4 to a version 5 index.
func TestFixMissingWholeref(t *testing.T) {
	ctx := context.Background()
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
		if _, err := ix.ReceiveBlob(ctxbg, b.BlobRef(), b.Reader()); err != nil {
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
	fi, err := ix.GetFileInfo(ctx, fileBlobRef)
	if err != nil {
		t.Fatal(err)
	}
	if fi.WholeRef.String() != parts[3] {
		t.Fatalf("index fileInfo wholeref was not fixed: got %q, wanted %v", fi.WholeRef, parts[3])
	}
}
