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

package archiver

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"math/rand"
	"reflect"
	"strings"
	"testing"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/schema"
	"camlistore.org/pkg/test"
)

func TestArchiver(t *testing.T) {
	src := new(test.Fetcher)
	blobHello := &test.Blob{Contents: "Hello"}
	blobWorld := &test.Blob{Contents: "World" + strings.Repeat("!", 1024)}

	golden := map[blob.Ref]string{
		blobHello.BlobRef(): blobHello.Contents,
		blobWorld.BlobRef(): blobWorld.Contents,
	}

	a := &Archiver{
		Source:                 src,
		DeleteSourceAfterStore: true,
	}

	src.AddBlob(blobHello)
	a.Store = func([]byte, []blob.SizedRef) error {
		return errors.New("Store shouldn't be called")
	}
	a.MinZipSize = 400 // empirically: the zip will be 416 bytes
	if err := a.RunOnce(); err != ErrSourceTooSmall {
		t.Fatalf("RunOnce with just Hello = %v; want ErrSourceTooSmall", err)
	}

	src.AddBlob(blobWorld)
	var zipData []byte
	var inZip []blob.SizedRef
	a.Store = func(zip []byte, brs []blob.SizedRef) error {
		zipData = zip
		inZip = brs
		return nil
	}
	if err := a.RunOnce(); err != nil {
		t.Fatalf("RunOnce with Hello and World = %v", err)
	}
	if zipData == nil {
		t.Error("no zip data stored")
	}
	if len(src.BlobrefStrings()) != 0 {
		t.Errorf("source still has blobs = %d; want none", len(src.BlobrefStrings()))
	}
	if len(inZip) != 2 {
		t.Errorf("expected 2 blobs reported as in zip to Store; got %v", inZip)
	}

	got := map[blob.Ref]string{}
	if err := foreachZipEntry(zipData, func(br blob.Ref, all []byte) {
		got[br] = string(all)
	}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(golden, got) {
		t.Errorf("zip contents didn't match. got: %v; want %v", got, golden)
	}
}

// Tests a bunch of rounds on a bunch of data.
func TestArchiverStress(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping in short mode")
	}
	src := new(test.Fetcher)
	fileRef, err := schema.WriteFileFromReader(src, "random", io.LimitReader(randReader{}, 10<<20))
	if err != nil {
		t.Fatal(err)
	}
	n0 := src.NumBlobs()
	t.Logf("Wrote %v in %d blobs", fileRef, n0)

	refs0 := src.BlobrefStrings()

	var zips [][]byte
	archived := map[blob.Ref]bool{}
	a := &Archiver{
		Source:                 src,
		MinZipSize:             1 << 20,
		DeleteSourceAfterStore: true,
		Store: func(zipd []byte, brs []blob.SizedRef) error {
			zips = append(zips, zipd)
			for _, sbr := range brs {
				if archived[sbr.Ref] {
					t.Errorf("duplicate archive of %v", sbr.Ref)
				}
				archived[sbr.Ref] = true
			}
			return nil
		},
	}
	for {
		err := a.RunOnce()
		if err == ErrSourceTooSmall {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
	}

	if len(archived) == 0 {
		t.Errorf("unexpected small number of archived blobs = %d", len(archived))
	}
	if len(zips) < 2 {
		t.Errorf("unexpected small number of zip files = %d", len(zips))
	}
	if n1 := src.NumBlobs() + len(archived); n0 != n1 {
		t.Errorf("original %d blobs != %d after + %d archived (%d)", n0, src.NumBlobs(), len(archived), n1)
	}

	// And restore:
	for _, zipd := range zips {
		if err := foreachZipEntry(zipd, func(br blob.Ref, contents []byte) {
			tb := &test.Blob{Contents: string(contents)}
			if tb.BlobRef() != br {
				t.Fatal("corrupt zip callback")
			}
			src.AddBlob(tb)
		}); err != nil {
			t.Fatal(err)
		}
	}

	refs1 := src.BlobrefStrings()
	if !reflect.DeepEqual(refs0, refs1) {
		t.Error("Restore error.")
	}
}

type randReader struct{}

func (randReader) Read(p []byte) (n int, err error) {
	for i := range p {
		p[i] = byte(rand.Intn(256))
	}
	return len(p), nil
}

func foreachZipEntry(zipData []byte, fn func(blob.Ref, []byte)) error {
	zipr, err := zip.NewReader(bytes.NewReader(zipData), int64(len(zipData)))
	if err != nil {
		return err
	}

	for _, f := range zipr.File {
		br, ok := blob.Parse(f.Name)
		if !ok {
			return fmt.Errorf("Bogus zip filename %q", f.Name)
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		all, err := ioutil.ReadAll(rc)
		rc.Close()
		if err != nil {
			return err
		}
		fn(br, all)
	}
	return nil
}
