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

package index_test

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/jpeg"
	"math/rand/v2"
	"testing"
	"time"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/index"
	"perkeep.org/pkg/index/indextest"
	"perkeep.org/pkg/schema"
	"perkeep.org/pkg/test"
)

// newKnownContentsDeps returns IndexDeps of an index with a corpus.
func newKnownContentsDeps(t *testing.T) *indextest.IndexDeps {
	idx := index.NewMemoryIndex()
	idx.Logger = test.NewLogger(t, "")
	if _, err := idx.KeepInMemory(); err != nil {
		t.Fatal(err)
	}
	id := indextest.NewIndexDeps(idx)
	id.Fataler = t
	return id
}

// uploadRenamed indexes a file schema named name with the same parts as
// the file fileRef.
func uploadRenamed(t *testing.T, id *indextest.IndexDeps, fileRef blob.Ref, name string) blob.Ref {
	t.Helper()
	ctx := context.Background()
	rc, _, err := id.BlobSource.Fetch(ctx, fileRef)
	if err != nil {
		t.Fatal(err)
	}
	orig, err := schema.BlobFromReader(fileRef, rc)
	rc.Close()
	if err != nil {
		t.Fatal(err)
	}
	m := schema.NewFileMap(name).SetModTime(time.Unix(1800000000, 0))
	if err := m.PopulateParts(orig.PartsSize(), orig.ByteParts()); err != nil {
		t.Fatal(err)
	}
	fjson, err := m.JSON()
	if err != nil {
		t.Fatal(err)
	}
	fb := &test.Blob{Contents: fjson}
	id.BlobSource.AddBlob(fb)
	if _, err := id.Index.ReceiveBlob(ctx, fb.BlobRef(), fb.Reader()); err != nil {
		t.Fatalf("indexing %s: %v", name, err)
	}
	return fb.BlobRef()
}

// TestKnownContents checks that a file schema with the same parts as an
// indexed file is indexed without reading its contents.
func TestKnownContents(t *testing.T) {
	ctx := context.Background()
	id := newKnownContentsDeps(t)
	contents := make([]byte, 1<<20)
	rand.NewChaCha8([32]byte{}).Read(contents)
	a, whole := id.UploadFile("a.bin", string(contents), time.Unix(1700000000, 0))

	// Without its contents, b.bin can only be indexed without reading
	// them.
	if err := id.BlobSource.RemoveBlobs(ctx, []blob.Ref{whole}); err != nil {
		t.Fatal(err)
	}
	b := uploadRenamed(t, id, a, "b.bin")
	fi, err := id.Index.GetFileInfo(ctx, b)
	if err != nil {
		t.Fatal(err)
	}
	if fi.WholeRef != whole || fi.Size != int64(len(contents)) || fi.FileName != "b.bin" {
		t.Errorf("b.bin's file info = %+v; want its own name, a.bin's wholeRef %v, and size %d", fi, whole, len(contents))
	}
	if got := fi.ModTime; got == nil && fi.Time == nil {
		t.Errorf("b.bin has no times")
	}
}

// TestKnownContentsImage checks that a renamed image's own rows, such as
// its size, come from just its prefix.
func TestKnownContentsImage(t *testing.T) {
	ctx := context.Background()
	id := newKnownContentsDeps(t)
	img := image.NewRGBA(image.Rect(0, 0, 1200, 900))
	r := rand.New(rand.NewChaCha8([32]byte{1}))
	for i := range img.Pix {
		img.Pix[i] = uint8(r.IntN(256))
	}
	img.Set(0, 0, color.Black)
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 95}); err != nil {
		t.Fatal(err)
	}
	if buf.Len() <= 512<<10 {
		t.Fatalf("test JPEG is only %d bytes; want more than the image prefix", buf.Len())
	}
	a, whole := id.UploadFile("a.jpg", buf.String(), time.Unix(1700000000, 0))
	b := uploadRenamed(t, id, a, "b.jpg")

	fi, err := id.Index.GetFileInfo(ctx, b)
	if err != nil {
		t.Fatal(err)
	}
	if fi.WholeRef != whole || fi.MIMEType != "image/jpeg" {
		t.Errorf("b.jpg's file info = %+v; want image/jpeg with a.jpg's wholeRef", fi)
	}
	ii, err := id.Index.GetImageInfo(ctx, b)
	if err != nil {
		t.Fatal(err)
	}
	if ii.Width != 1200 || ii.Height != 900 {
		t.Errorf("b.jpg's image info = %+v; want 1200x900", ii)
	}
}
