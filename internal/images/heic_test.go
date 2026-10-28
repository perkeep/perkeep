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

package images

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/gen2brain/heic"
)

// forEachHEICDecoder runs f as a subtest with each of the heic package's
// decoders: the system's libheif, if it can be loaded, and the embedded
// WebAssembly one.
func forEachHEICDecoder(t *testing.T, f func(t *testing.T)) {
	t.Run("libheif", func(t *testing.T) {
		if err := heic.Dynamic(); err != nil {
			t.Skipf("libheif not available: %v", err)
		}
		f(t)
	})
	t.Run("wasm", func(t *testing.T) {
		heic.ForceWasmMode = true
		defer func() { heic.ForceWasmMode = false }()
		f(t)
	})
}

// uiOpts returns DecodeOpts like those of a UI thumbnail request for
// height mh.
func uiOpts(mh int) *DecodeOpts {
	return &DecodeOpts{MaxWidth: 2000, MaxHeight: mh}
}

func readTestdata(t testing.TB, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func findHEICThumbnail(ra io.ReaderAt, opts *DecodeOpts) (*HEICThumbnail, error) {
	h, err := OpenHEIC(ra)
	if err != nil {
		return nil, err
	}
	return h.Thumbnail(opts)
}

// TestHEICGenericMajorBrand tests a HEIC file whose major brand is the
// generic "mif1", with HEVC brands only among its compatible brands, which
// the heic package's registered image formats don't match.
func TestHEICGenericMajorBrand(t *testing.T) {
	if testing.Short() {
		t.Skip("decodes a 12 megapixel image")
	}
	orig := readTestdata(t, "ski-throne.heic")
	src := bytes.Clone(orig)
	if string(src[4:12]) != "ftypheic" {
		t.Fatalf("ski-throne.heic starts %q", src[:12])
	}
	copy(src[8:12], "mif1")

	want, err := DecodeConfig(bytes.NewReader(orig))
	if err != nil {
		t.Fatal(err)
	}
	conf, err := DecodeConfig(bytes.NewReader(src))
	if err != nil {
		t.Fatalf("DecodeConfig: %v", err)
	}
	if conf.Format != "heic" || conf.Width != want.Width || conf.Height != want.Height {
		t.Errorf("DecodeConfig = %+v; want %+v", conf, want)
	}
	forEachHEICDecoder(t, func(t *testing.T) {
		im, conf, err := Decode(bytes.NewReader(src), uiOpts(256))
		if err != nil {
			t.Fatalf("Decode: %v", err)
		}
		if conf.Format != "heic" || im.Bounds().Dx() != 192 || im.Bounds().Dy() != 256 {
			t.Errorf("Decode = %v, config %+v; want 192x256 heic", im.Bounds(), conf)
		}
	})
}

func TestOpenHEIC(t *testing.T) {
	h, err := OpenHEIC(bytes.NewReader(readTestdata(t, "ski-throne.heic")))
	if err != nil {
		t.Fatal(err)
	}
	if h.Width != 3024 || h.Height != 4032 {
		t.Errorf("OpenHEIC dimensions = %dx%d; want 3024x4032", h.Width, h.Height)
	}
	if _, err := OpenHEIC(bytes.NewReader(readTestdata(t, "f1-exif.jpg"))); err == nil {
		t.Error("OpenHEIC of a JPEG succeeded; want error")
	}
}

// BenchmarkHEICThumbnail compares a 256 pixel tall thumbnail of a 12
// megapixel photo made from its embedded thumbnail and from its primary
// image.
func BenchmarkHEICThumbnail(b *testing.B) {
	src := readTestdata(b, "ski-throne.heic")
	opts := uiOpts(256)
	b.Run("embedded", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			th, err := findHEICThumbnail(bytes.NewReader(src), opts)
			if err != nil {
				b.Fatal(err)
			}
			if _, _, err := th.Decode(); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("primary", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, _, err := Decode(bytes.NewReader(src), opts); err != nil {
				b.Fatal(err)
			}
		}
	})
}

func TestHEICThumbnail(t *testing.T) {
	// ski-throne.heic is a 3024x4032 portrait photo whose 416x312
	// thumbnail item is rotated, like its primary image.
	src := readTestdata(t, "ski-throne.heic")
	forEachHEICDecoder(t, func(t *testing.T) {
		for _, tt := range []struct {
			mh         int
			wantW      int
			wantH      int
			wantNoThmb bool
		}{
			{mh: 416, wantW: 312, wantH: 416},
			{mh: 300, wantW: 312, wantH: 416}, // within maxThumbnailOversize
			{mh: 256, wantW: 192, wantH: 256}, // rescaled
			{mh: 64, wantW: 48, wantH: 64},
			{mh: 500, wantNoThmb: true},
		} {
			th, err := findHEICThumbnail(bytes.NewReader(src), uiOpts(tt.mh))
			if tt.wantNoThmb {
				if !errors.Is(err, ErrNoUsableThumbnail) {
					t.Errorf("mh=%d: findHEICThumbnail error = %v; want ErrNoUsableThumbnail", tt.mh, err)
				}
				continue
			}
			if err != nil {
				t.Fatalf("mh=%d: findHEICThumbnail: %v", tt.mh, err)
			}
			if th.Width != 312 || th.Height != 416 {
				t.Errorf("mh=%d: thumbnail is %dx%d; want 312x416", tt.mh, th.Width, th.Height)
			}
			im, conf, err := th.Decode()
			if err != nil {
				t.Fatalf("mh=%d: Decode: %v", tt.mh, err)
			}
			b := im.Bounds()
			if b.Dx() != tt.wantW || b.Dy() != tt.wantH || conf.Width != tt.wantW || conf.Height != tt.wantH {
				t.Errorf("mh=%d: decoded %v, config %dx%d; want %dx%d", tt.mh, b, conf.Width, conf.Height, tt.wantW, tt.wantH)
			}
			if conf.Format != "heic" || !conf.Modified {
				t.Errorf("mh=%d: config = %+v; want heic format, modified", tt.mh, conf)
			}
		}
	})
}

// TestHEICThumbnailMatchesPrimary checks that the thumbnail looks like
// the primary image scaled down, including its orientation.
func TestHEICThumbnailMatchesPrimary(t *testing.T) {
	if testing.Short() {
		t.Skip("decodes a 12 megapixel image")
	}
	src := readTestdata(t, "ski-throne.heic")
	th, err := findHEICThumbnail(bytes.NewReader(src), uiOpts(416))
	if err != nil {
		t.Fatal(err)
	}
	thumb, _, err := th.Decode()
	if err != nil {
		t.Fatal(err)
	}
	full, _, err := Decode(bytes.NewReader(src), uiOpts(416))
	if err != nil {
		t.Fatal(err)
	}
	if full.Bounds().Size() != thumb.Bounds().Size() {
		t.Fatalf("full decode is %v; thumbnail is %v", full.Bounds(), thumb.Bounds())
	}
	const n = 8 // compare n x n block averages
	var sum, rotSum float64
	rotated := rotate(rotate(thumb, 90), 90)
	for by := range n {
		for bx := range n {
			f := blockMean(full, bx, by, n)
			sum += colorDiff(f, blockMean(thumb, bx, by, n))
			rotSum += colorDiff(f, blockMean(rotated, bx, by, n))
		}
	}
	mean, rotMean := sum/(n*n), rotSum/(n*n)
	t.Logf("mean block difference %.1f; %.1f if rotated 180 degrees", mean, rotMean)
	if mean > 12 || mean*2 > rotMean {
		t.Errorf("thumbnail doesn't match the primary image: mean block difference %.1f; %.1f rotated", mean, rotMean)
	}
}

// blockMean returns the mean color of block (bx, by) of im, split into
// n x n blocks, with 8 bit channels.
func blockMean(im image.Image, bx, by, n int) [3]float64 {
	b := im.Bounds()
	x0, x1 := b.Min.X+bx*b.Dx()/n, b.Min.X+(bx+1)*b.Dx()/n
	y0, y1 := b.Min.Y+by*b.Dy()/n, b.Min.Y+(by+1)*b.Dy()/n
	var m [3]float64
	for y := y0; y < y1; y++ {
		for x := x0; x < x1; x++ {
			r, g, b, _ := im.At(x, y).RGBA()
			m[0] += float64(r >> 8)
			m[1] += float64(g >> 8)
			m[2] += float64(b >> 8)
		}
	}
	px := float64((x1 - x0) * (y1 - y0))
	for i := range m {
		m[i] /= px
	}
	return m
}

func colorDiff(a, b [3]float64) float64 {
	var d float64
	for i := range a {
		if a[i] > b[i] {
			d += a[i] - b[i]
		} else {
			d += b[i] - a[i]
		}
	}
	return d / 3
}

func TestHEICThumbnailUnsupportedOpts(t *testing.T) {
	src := readTestdata(t, "ski-throne.heic")
	for _, opts := range []*DecodeOpts{
		nil,
		{MaxWidth: 100},
		{MaxWidth: 100, MaxHeight: 100, Rotate: 90},
		{MaxWidth: 100, MaxHeight: 100, Flip: FlipHorizontal},
		{ScaleWidth: 0.1, ScaleHeight: 0.1},
		{MaxWidth: 5000, MaxHeight: 5000}, // no downscaling
	} {
		if _, err := findHEICThumbnail(bytes.NewReader(src), opts); !errors.Is(err, ErrNoUsableThumbnail) {
			t.Errorf("findHEICThumbnail(%+v) error = %v; want ErrNoUsableThumbnail", opts, err)
		}
	}
}

// TestHEICJPEGThumbnail tests a HEIC file with a JPEG-coded thumbnail
// item. The 320x198 thumbnail of river-truncated.heic lies beyond its
// truncation, so the test supplies one.
func TestHEICJPEGThumbnail(t *testing.T) {
	src := readTestdata(t, "river-truncated.heic")
	th, err := findHEICThumbnail(bytes.NewReader(src), uiOpts(198))
	if err != nil {
		t.Fatal(err)
	}
	if th.Width != 320 || th.Height != 198 {
		t.Fatalf("thumbnail is %dx%d; want 320x198", th.Width, th.Height)
	}
	if _, _, err := th.Decode(); err == nil {
		t.Fatal("Decode of truncated thumbnail succeeded; want error")
	}

	// Splice a JPEG into the thumbnail item's extent, padding it
	// to the declared length.
	const off, length = 7446, 25147
	src = src[:min(len(src), off)]
	in := image.NewRGBA(image.Rect(0, 0, 320, 198))
	for y := range 198 {
		for x := range 320 {
			in.Set(x, y, color.RGBA{uint8(x), uint8(y), 128, 255})
		}
	}
	var jb bytes.Buffer
	if err := jpeg.Encode(&jb, in, nil); err != nil {
		t.Fatal(err)
	}
	if jb.Len() > length {
		t.Fatalf("test JPEG is %d bytes; want at most %d", jb.Len(), length)
	}
	src = append(src, jb.Bytes()...)
	src = append(src, make([]byte, length-jb.Len())...)

	th, err = findHEICThumbnail(bytes.NewReader(src), uiOpts(198))
	if err != nil {
		t.Fatal(err)
	}
	im, conf, err := th.Decode()
	if err != nil {
		t.Fatal(err)
	}
	if b := im.Bounds(); b.Dx() != 320 || b.Dy() != 198 || conf.Width != 320 || conf.Height != 198 {
		t.Errorf("decoded %v, config %+v; want 320x198", b, conf)
	}
	near := func(v uint32, want int) bool { return int(v>>8) >= want-10 && int(v>>8) <= want+10 }
	if r, g, b, _ := im.At(200, 100).RGBA(); !near(r, 200) || !near(g, 100) || !near(b, 128) {
		t.Errorf("pixel (200, 100) = %v; want about {200, 100, 128}", im.At(200, 100))
	}
}

// heifBox returns an ISO BMFF box of type typ with the concatenated bodies.
func heifBox(typ string, body ...[]byte) []byte {
	b := bytes.Join(body, nil)
	return append(append(binary.BigEndian.AppendUint32(nil, uint32(8+len(b))), typ...), b...)
}

func u16(v uint16) []byte { return binary.BigEndian.AppendUint16(nil, v) }
func u32(v uint32) []byte { return binary.BigEndian.AppendUint32(nil, v) }

// jpegThumbHEIC returns a HEIF file whose 800x400 (as stored) "hvc1"
// primary image, which lacks data, has the JPEG thumb as a "jpeg"
// thumbnail item. Both items have the rotation irot, in quarter turns
// counter-clockwise.
func jpegThumbHEIC(thumb []byte, tw, th int, irot byte) []byte {
	full := []byte{0, 0, 0, 0} // FullBox version 0, flags 0
	ispe := func(w, h int) []byte { return heifBox("ispe", full, u32(uint32(w)), u32(uint32(h))) }
	meta := func(thumbOff uint32) []byte {
		return heifBox("meta", full,
			heifBox("hdlr", full, u32(0), []byte("pict"), make([]byte, 12), []byte{0}),
			heifBox("pitm", full, u16(1)),
			heifBox("iinf", full, u16(2),
				heifBox("infe", []byte{2, 0, 0, 0}, u16(1), u16(0), []byte("hvc1\x00")),
				heifBox("infe", []byte{2, 0, 0, 0}, u16(2), u16(0), []byte("jpeg\x00"))),
			heifBox("iref", full, heifBox("thmb", u16(2), u16(1), u16(1))),
			// 4 byte offsets and lengths, no base offsets.
			heifBox("iloc", full, []byte{0x44, 0}, u16(2),
				u16(1), u16(0), u16(0),
				u16(2), u16(0), u16(1), u32(thumbOff), u32(uint32(len(thumb)))),
			heifBox("iprp",
				heifBox("ipco", ispe(800, 400), heifBox("irot", []byte{irot}), ispe(tw, th)),
				heifBox("ipma", full, u32(2),
					u16(1), []byte{2, 1, 0x80 | 2},
					u16(2), []byte{2, 3, 0x80 | 2})))
	}
	ftyp := heifBox("ftyp", []byte("heic"), u32(0), []byte("mif1heic"))
	off := uint32(len(ftyp) + len(meta(0)) + 8)
	return bytes.Join([][]byte{ftyp, meta(off), heifBox("mdat", thumb)}, nil)
}

// TestHEICJPEGThumbnailRotation checks that a rotated JPEG thumbnail
// item is rotated counter-clockwise, as HEIF's irot specifies, by
// tracking a red marker in the corner of the stored image. If libheif
// can decode JPEG items, it also checks that libheif decodes the same.
func TestHEICJPEGThumbnailRotation(t *testing.T) {
	const tw, th = 80, 40
	in := image.NewRGBA(image.Rect(0, 0, tw, th))
	for y := range th {
		for x := range tw {
			c := color.RGBA{0, 0, 255, 255}
			if x < 16 && y < 16 {
				c = color.RGBA{255, 0, 0, 255}
			}
			in.Set(x, y, c)
		}
	}
	var jb bytes.Buffer
	if err := jpeg.Encode(&jb, in, &jpeg.Options{Quality: 95}); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		irot   byte
		w, h   int
		corner string // where the stored top-left marker should end up
	}{
		{0, 80, 40, "top-left"},
		{1, 40, 80, "bottom-left"},
		{2, 80, 40, "bottom-right"},
		{3, 40, 80, "top-right"},
	} {
		src := jpegThumbHEIC(jb.Bytes(), tw, th, tt.irot)
		thumb, err := findHEICThumbnail(bytes.NewReader(src), &DecodeOpts{MaxWidth: 60, MaxHeight: 60})
		if err != nil {
			t.Fatalf("irot %d: %v", tt.irot, err)
		}
		im, _, err := thumb.Decode()
		if err != nil {
			t.Fatalf("irot %d: Decode: %v", tt.irot, err)
		}
		b := im.Bounds()
		if b.Dx() != tt.w || b.Dy() != tt.h {
			t.Fatalf("irot %d: decoded %v; want %dx%d", tt.irot, b, tt.w, tt.h)
		}
		if got := redCorner(im); got != tt.corner {
			t.Errorf("irot %d: marker at %s; want %s", tt.irot, got, tt.corner)
		}

		forEachHEICDecoder(t, func(t *testing.T) {
			standalone, err := thumb.f.StandaloneHEIF(thumb.item)
			if err != nil {
				t.Fatal(err)
			}
			ref, err := heic.Decode(bytes.NewReader(standalone))
			if err != nil {
				t.Skipf("can't decode JPEG items: %v", err)
			}
			if ref.Bounds().Size() != b.Size() {
				t.Fatalf("irot %d: decoded to %v; want %v", tt.irot, ref.Bounds().Size(), b.Size())
			}
			if got := redCorner(ref); got != tt.corner {
				t.Errorf("irot %d: decoded with marker at %s; want %s", tt.irot, got, tt.corner)
			}
		})
	}
}

// redCorner returns which corner of im is red, or "none".
func redCorner(im image.Image) string {
	b := im.Bounds()
	for _, c := range []struct {
		name string
		x, y int
	}{
		{"top-left", b.Min.X + 4, b.Min.Y + 4},
		{"top-right", b.Max.X - 5, b.Min.Y + 4},
		{"bottom-left", b.Min.X + 4, b.Max.Y - 5},
		{"bottom-right", b.Max.X - 5, b.Max.Y - 5},
	} {
		if r, g, bl, _ := im.At(c.x, c.y).RGBA(); r>>8 > 200 && g>>8 < 60 && bl>>8 < 60 {
			return c.name
		}
	}
	return "none"
}
