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

package server

import (
	"bytes"
	"context"
	"image"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"go4.org/syncutil"
	"perkeep.org/internal/magic"
	"perkeep.org/pkg/blobserver/memory"
	"perkeep.org/pkg/index"
	"perkeep.org/pkg/index/indextest"
	"perkeep.org/pkg/schema/nodeattr"
	"perkeep.org/pkg/search"
	"perkeep.org/pkg/test"
)

func TestWebPIssue850(t *testing.T) {
	t.Setenv("CAMLI_REDO_INDEX_ON_RECEIVE", "false")
	ctx := context.Background()
	data, err := os.ReadFile("../../internal/images/testdata/hithere.webp")
	if err != nil {
		t.Fatal(err)
	}
	if got := magic.MIMEType(data); got != "image/webp" {
		t.Fatalf("MIMEType = %q; want image/webp", got)
	}
	idx := index.NewMemoryIndex()
	idx.Logger = test.NewLogger(t, "")
	id := indextest.NewIndexDeps(idx)
	id.Fataler = t
	defer id.Index.Close()
	file, _ := id.UploadFile("hithere.webp", string(data), time.Time{})
	pn := id.NewPermanode()
	id.SetAttribute(pn, nodeattr.CamliContent, file.String())
	h := search.NewHandler(id.Index, index.NewOwner(indextest.KeyID, id.SignerBlobRef))
	res, err := h.Describe(ctx, &search.DescribeRequest{BlobRef: pn, Depth: 2})
	if err != nil {
		t.Fatal(err)
	}
	db := res.Meta[file.String()]
	if db == nil || db.File == nil || db.File.MIMEType != "image/webp" ||
		db.Image == nil || db.Image.Width != 37 || db.Image.Height != 12 {
		t.Fatalf("describe did not expose WebP image metadata: %+v", db)
	}
	t.Logf("describe: image/webp, %dx%d", db.Image.Width, db.Image.Height)

	ih := &ImageHandler{
		Fetcher: id.BlobSource, Search: h,
		Cache: new(memory.Storage), ThumbMeta: NewThumbMeta(nil),
		MaxWidth: 20, MaxHeight: 20, ResizeSem: syncutil.NewSem(128 << 20),
	}
	for _, name := range []string{"cold", "cached", "detail"} {
		t.Run(name, func(t *testing.T) {
			if name == "detail" {
				ih.MaxWidth, ih.MaxHeight = 1000, 1000
			}
			hits := thumbCacheHitFull.Value()
			rec := httptest.NewRecorder()
			ih.ServeHTTP(rec, httptest.NewRequest("GET", "/ui/thumbnail/"+file.String()+"/hithere.webp", nil), file)
			if rec.Code != 200 {
				t.Fatalf("HTTP %d: %s", rec.Code, rec.Body.String())
			}
			if name == "cached" && !disableThumbCache && thumbCacheHitFull.Value() != hits+1 {
				t.Fatal("second thumbnail request did not use the cache")
			}
			im, format, err := image.Decode(bytes.NewReader(rec.Body.Bytes()))
			if err != nil {
				t.Fatal(err)
			}
			wantW, wantH := 20, 6
			if name == "detail" {
				wantW, wantH = 37, 12
			}
			if format != "jpeg" || im.Bounds().Dx() != wantW || im.Bounds().Dy() != wantH {
				t.Fatalf("thumbnail = %s %v; want JPEG %dx%d", format, im.Bounds(), wantW, wantH)
			}
			if got := rec.Header().Get("Content-Type"); got != "image/jpeg" {
				t.Errorf("Content-Type = %q; want image/jpeg", got)
			}
			t.Logf("HTTP %d, Content-Type %s, actual image/%s %dx%d",
				rec.Code, rec.Header().Get("Content-Type"), format, wantW, wantH)
		})
	}
	dh := &DownloadHandler{Fetcher: id.BlobSource, Search: h}
	rec := httptest.NewRecorder()
	dh.ServeFile(rec, httptest.NewRequest("GET", "/ui/download/"+file.String()+"/hithere.webp", nil), file)
	if rec.Code != 200 || !bytes.Equal(rec.Body.Bytes(), data) ||
		rec.Header().Get("Content-Type") != "image/webp" {
		t.Fatalf("original download = HTTP %d, MIME %s, %d bytes",
			rec.Code, rec.Header().Get("Content-Type"), rec.Body.Len())
	}

	// Simulate a pre-fix index: the schema was considered fully indexed,
	// but unsupported image decoding left no imagesize row.
	key := "imagesize|" + file.String()
	if err := id.Index.Storage().Delete(key); err != nil {
		t.Fatal(err)
	}
	id.UploadFile("hithere.webp", string(data), time.Time{})
	if _, err := id.Index.GetImageInfo(ctx, file); !os.IsNotExist(err) {
		t.Fatalf("ordinary reupload unexpectedly repaired old image metadata: %v", err)
	}
	res, err = h.Describe(ctx, &search.DescribeRequest{BlobRef: pn, Depth: 2})
	if err != nil {
		t.Fatal(err)
	}
	if db := res.Meta[file.String()]; db == nil || db.Image != nil {
		t.Fatalf("old index should describe the file without image dimensions: %+v", db)
	}
	t.Setenv("CAMLI_REDO_INDEX_ON_RECEIVE", "true")
	id.UploadFile("hithere.webp", string(data), time.Time{})
	info, err := id.Index.GetImageInfo(ctx, file)
	if err != nil || info.Width != 37 || info.Height != 12 {
		t.Fatalf("forced reindex = %+v, %v; want 37x12", info, err)
	}
	t.Log("old imagesize row: ordinary reupload leaves it missing; forced reindex restores 37x12")
}
