/*
Copyright 2014 The Camlistore Authors

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

package blobpacked

import (
	"bytes"
	"encoding/json"
	"io/ioutil"
	"math/rand"
	"testing"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/blobserver"
	"camlistore.org/pkg/blobserver/storagetest"
	"camlistore.org/pkg/context"
	"camlistore.org/pkg/schema"
	"camlistore.org/pkg/sorted"
	"camlistore.org/pkg/test"
	"camlistore.org/third_party/go/pkg/archive/zip"
)

func TestStorage(t *testing.T) {
	storagetest.Test(t, func(t *testing.T) (sto blobserver.Storage, cleanup func()) {
		s := &storage{
			small: new(test.Fetcher),
			large: new(test.Fetcher),
			meta:  sorted.NewMemoryKeyValue(),
		}
		s.init()
		return s, func() {}
	})
}

func TestParseMetaRow(t *testing.T) {
	cases := []struct {
		in   string
		want meta
		err  bool
	}{
		{in: "123 s", want: meta{exists: true, size: 123}},
		{in: "123 sx", err: true},
		{in: "-123 s", err: true},
		{in: "", err: true},
		{in: "1 ", err: true},
		{in: " ", err: true},
		{in: "123 x", err: true},
		{in: "123 l", err: true},
		{in: "123 l sha1-f1d2d2f924e986ac86fdf7b36c94bcdf32beec15", err: true},
		{in: "123 l notaref 12", err: true},
		{in: "123 l sha1-f1d2d2f924e986ac86fdf7b36c94bcdf32beec15 42 extra", err: true},
		{in: "123 l sha1-f1d2d2f924e986ac86fdf7b36c94bcdf32beec15 42 ", err: true},
		{in: "123 l sha1-f1d2d2f924e986ac86fdf7b36c94bcdf32beec15 42", want: meta{
			exists:   true,
			size:     123,
			largeRef: blob.MustParse("sha1-f1d2d2f924e986ac86fdf7b36c94bcdf32beec15"),
			largeOff: 42,
		}},
	}
	for _, tt := range cases {
		got, err := parseMetaRow([]byte(tt.in))
		if (err != nil) != tt.err {
			t.Errorf("For %q error = %v; want-err? = %v", tt.in, err, tt.err)
			continue
		}
		if tt.err {
			continue
		}
		if got != tt.want {
			t.Errorf("For %q, parseMetaRow = %+v; want %+v", tt.in, got, tt.want)
		}
	}
}

func TestPack(t *testing.T) {
	small, large := new(test.Fetcher), new(test.Fetcher)
	sto := &storage{
		small: small,
		large: large,
		meta:  sorted.NewMemoryKeyValue(),
	}
	sto.init()

	const fileSize = 5 << 20
	fileContents := make([]byte, fileSize)
	for i := range fileContents {
		fileContents[i] = byte(rand.Int63())
	}
	const fileName = "foo.dat"
	ref, err := schema.WriteFileFromReader(sto, fileName, bytes.NewReader(fileContents))
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("wrote file %v", ref)
	t.Logf("items in small: %v", small.NumBlobs())
	t.Logf("items in large: %v", large.NumBlobs())
	if large.NumBlobs() != 1 {
		t.Fatalf("num large blobs = %d; want 1", large.NumBlobs())
	}

	var zipRef blob.Ref
	blobserver.EnumerateAll(context.New(), large, func(sb blob.SizedRef) error {
		zipRef = sb.Ref
		return nil
	})
	if !zipRef.Valid() {
		t.Fatal("didn't get zip ref from enumerate")
	}
	t.Logf("large ref = %v", zipRef)
	rc, _, err := large.Fetch(zipRef)
	if err != nil {
		t.Fatal(err)
	}
	zipBytes, err := ioutil.ReadAll(rc)
	rc.Close()
	zr, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
	if err != nil {
		t.Fatalf("Error reading resulting zip file: %v", err)
	}
	if len(zr.File) == 0 {
		t.Fatal("zip is empty")
	}
	nameSeen := map[string]bool{}
	for i, zf := range zr.File {
		if nameSeen[zf.Name] {
			t.Errorf("duplicate name %q seen", zf.Name)
		}
		nameSeen[zf.Name] = true
		t.Logf("zip[%d] size %d, %v", i, zf.UncompressedSize64, zf.Name)
	}
	mfr, err := zr.File[len(zr.File)-1].Open()
	if err != nil {
		t.Fatalf("Error opening manifest JSON: %v", err)
	}
	maniJSON, err := ioutil.ReadAll(mfr)
	if err != nil {
		t.Fatalf("Error reading manifest JSON: %v", err)
	}
	var mf zipManifest
	if err := json.Unmarshal(maniJSON, &mf); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	// Verify each chunk described in the manifest:
	for _, bo := range mf.Blobs {
		h := bo.Ref.Hash()
		h.Write(zipBytes[bo.Offset : bo.Offset+int64(bo.Size)])
		if !bo.Ref.HashMatches(h) {
			t.Errorf("blob %+v didn't describe the actual data in the zip", bo)
		}
	}
	t.Logf("Manifest: %s", maniJSON)

	// TODO: so many more tests:
	// -- first file is named "manifest.json" or like a blobref, in which case we should name it "data"
	// -- verify deleting from the source
	// -- verify we can reconstruct it all from the zip
	// -- verify the meta before & after
	// -- verify we can still get each blob. and enumerate.
	// -- overflowing the 16MB chunk size with huge initial chunks
	// -- zips spanning more than one 16MB zip
}
