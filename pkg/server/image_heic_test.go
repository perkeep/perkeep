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
	"image/jpeg"
	"io"
	"os"
	"testing"
	"testing/iotest"
)

func TestScaleImageHEICThumbnail(t *testing.T) {
	// ski-throne.heic is 3024x4032 (36 MB at 3 bytes per pixel) with
	// an embedded 312x416 thumbnail (389 KB).
	data, err := os.ReadFile("../../internal/images/testdata/ski-throne.heic")
	if err != nil {
		t.Fatal(err)
	}
	const capacity = 1 << 20
	ih, ref := jpegScaleTestFile(t, data, capacity, 2000, 256)

	embedded, primary := heicThumbEmbedded.Value(), heicThumbPrimary.Value()
	thumb, format, err := ih.ScaleImage(context.Background(), ref)
	if err != nil {
		t.Fatalf("ScaleImage: %v", err)
	}
	if format != "jpeg" {
		t.Errorf("format = %q; want jpeg", format)
	}
	conf, err := jpeg.DecodeConfig(bytes.NewReader(thumb))
	if err != nil {
		t.Fatal(err)
	}
	if conf.Width != 192 || conf.Height != 256 {
		t.Errorf("thumbnail is %dx%d; want 192x256", conf.Width, conf.Height)
	}
	if got := heicThumbEmbedded.Value() - embedded; got != 1 {
		t.Errorf("embedded thumbnails used = %d; want 1", got)
	}
	checkImageMemoryReleased(t, ih, capacity)

	// The thumbnail is too small for 500 pixels tall, so the primary
	// image is decoded, which needs more memory than allowed.
	ih.MaxHeight = 500
	if _, _, err := ih.ScaleImage(context.Background(), ref); err == nil {
		t.Error("ScaleImage at 500 pixels tall succeeded without memory for the primary image")
	}
	if got := heicThumbPrimary.Value() - primary; got != 1 {
		t.Errorf("primary images used = %d; want 1", got)
	}
	checkImageMemoryReleased(t, ih, capacity)
}

func TestHeadReaderAt(t *testing.T) {
	src := make([]byte, 200<<10)
	for i := range src {
		src[i] = byte(i * 7)
	}
	h := &headReaderAt{r: iotest.HalfReader(bytes.NewReader(src))}
	for _, rd := range []struct{ off, n int }{
		{10, 20}, {0, 5}, {70 << 10, 100}, {100, 70 << 10}, {150 << 10, 1},
	} {
		p := make([]byte, rd.n)
		n, err := h.ReadAt(p, int64(rd.off))
		if err != nil || n != rd.n || !bytes.Equal(p, src[rd.off:][:rd.n]) {
			t.Fatalf("ReadAt(%d bytes, %d) = %d, %v; want %[1]d bytes of src", rd.n, rd.off, n, err)
		}
	}
	if len(h.buf) >= len(src) {
		t.Errorf("buffered %d bytes; want less than all %d", len(h.buf), len(src))
	}
	all, err := io.ReadAll(h.All())
	if err != nil || !bytes.Equal(all, src) {
		t.Errorf("All read %d bytes, %v; want src", len(all), err)
	}

	h = &headReaderAt{r: bytes.NewReader(src[:100])}
	p := make([]byte, 50)
	if n, err := h.ReadAt(p, 80); n != 20 || err != io.EOF {
		t.Errorf("ReadAt past end = %d, %v; want 20, EOF", n, err)
	}
	if n, err := h.ReadAt(p, 200); n != 0 || err != io.EOF {
		t.Errorf("ReadAt beyond end = %d, %v; want 0, EOF", n, err)
	}
	if n, err := h.ReadAt(p, 0); n != 50 || err != nil || !bytes.Equal(p, src[:50]) {
		t.Errorf("ReadAt(50, 0) after EOF = %d, %v", n, err)
	}
	if all, err := io.ReadAll(h.All()); err != nil || !bytes.Equal(all, src[:100]) {
		t.Errorf("All read %d bytes, %v; want 100", len(all), err)
	}
}

func TestScaleImageHEICPrimaryFallback(t *testing.T) {
	data, err := os.ReadFile("../../internal/images/testdata/ski-throne.heic")
	if err != nil {
		t.Fatal(err)
	}
	ih, ref := jpegScaleTestFile(t, data, 128<<20, 2000, 500)
	thumb, _, err := ih.ScaleImage(context.Background(), ref)
	if err != nil {
		t.Fatalf("ScaleImage: %v", err)
	}
	conf, err := jpeg.DecodeConfig(bytes.NewReader(thumb))
	if err != nil {
		t.Fatal(err)
	}
	if conf.Width != 375 || conf.Height != 500 {
		t.Errorf("thumbnail is %dx%d; want 375x500", conf.Width, conf.Height)
	}
}
