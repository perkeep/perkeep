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
	"image"
	"image/color"
	"image/draw"
	"image/gif"
	"image/jpeg"
	"image/png"
	"net/http/httptest"
	"testing"
	"time"

	"go4.org/syncutil"
	"golang.org/x/image/tiff"
	"perkeep.org/pkg/blobserver/memory"
	"perkeep.org/pkg/index"
	"perkeep.org/pkg/index/indextest"
	"perkeep.org/pkg/search"
)

func TestThumbnailContentType(t *testing.T) {
	im := image.NewRGBA(image.Rect(0, 0, 40, 20))
	draw.Draw(im, im.Bounds(), image.NewUniform(color.RGBA{R: 255, A: 255}), image.Point{}, draw.Src)
	for _, tc := range []struct {
		format, wantMIME string
		encode           func(*bytes.Buffer) error
	}{
		{"jpeg", "image/jpeg", func(b *bytes.Buffer) error { return jpeg.Encode(b, im, nil) }},
		{"png", "image/png", func(b *bytes.Buffer) error { return png.Encode(b, im) }},
		{"gif", "image/jpeg", func(b *bytes.Buffer) error { return gif.Encode(b, im, nil) }},
		{"tiff", "image/jpeg", func(b *bytes.Buffer) error { return tiff.Encode(b, im, nil) }},
		{"svg", "image/svg+xml", func(b *bytes.Buffer) error {
			b.WriteString(`<svg xmlns="http://www.w3.org/2000/svg" width="40" height="20"><rect width="40" height="20" fill="red"/></svg>`)
			return nil
		}},
	} {
		t.Run(tc.format, func(t *testing.T) {
			var src bytes.Buffer
			if err := tc.encode(&src); err != nil {
				t.Fatal(err)
			}
			id := indextest.NewIndexDeps(index.NewMemoryIndex())
			id.Fataler = t
			defer id.Index.Close()
			file, _ := id.UploadFile("image."+tc.format, src.String(), time.Time{})
			h := search.NewHandler(id.Index, index.NewOwner(indextest.KeyID, id.SignerBlobRef))
			for _, cached := range []bool{false, true} {
				name := "uncached"
				if cached {
					name = "cached"
				}
				t.Run(name, func(t *testing.T) {
					ih := &ImageHandler{
						Fetcher: id.BlobSource, Search: h,
						MaxWidth: 20, MaxHeight: 20, ResizeSem: syncutil.NewSem(128 << 20),
					}
					if cached {
						ih.Cache = new(memory.Storage)
						ih.ThumbMeta = NewThumbMeta(nil)
					}
					for _, method := range []string{"HEAD", "GET", "GET", "HEAD"} {
						hits := thumbCacheHitFull.Value()
						rec := httptest.NewRecorder()
						ih.ServeHTTP(rec, httptest.NewRequest(method, "/thumbnail/"+file.String(), nil), file)
						if rec.Code != 200 {
							t.Fatalf("%s HTTP %d: %s", method, rec.Code, rec.Body.String())
						}
						if got := rec.Header().Get("Content-Type"); got != tc.wantMIME {
							t.Errorf("%s Content-Type = %q; want %q", method, got, tc.wantMIME)
						}
						// SVG is passed through, not rasterized or thumbnail-cached.
						if method == "GET" && cached && tc.format != "svg" && !disableThumbCache &&
							thumbCacheHitFull.Value() != hits+1 {
							t.Fatal("GET did not reuse the thumbnail cached by HEAD")
						}
						if method == "HEAD" {
							if rec.Body.Len() != 0 {
								t.Fatal("HEAD returned a response body")
							}
							continue
						}
						if tc.format == "svg" {
							if !bytes.Equal(rec.Body.Bytes(), src.Bytes()) {
								t.Fatal("SVG was not passed through unchanged")
							}
							continue
						}
						thumb, format, err := image.Decode(bytes.NewReader(rec.Body.Bytes()))
						if err != nil {
							t.Fatal(err)
						}
						if got := "image/" + format; got != tc.wantMIME {
							t.Errorf("body MIME = %q; want %q", got, tc.wantMIME)
						}
						if got := thumb.Bounds().Size(); got != image.Pt(20, 10) {
							t.Errorf("thumbnail size = %v; want 20x10", got)
						}
					}
				})
			}
		})
	}
}
