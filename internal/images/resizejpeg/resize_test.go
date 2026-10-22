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

package resizejpeg

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"testing/iotest"
)

func corpus(t testing.TB) []string {
	t.Helper()
	paths, err := filepath.Glob("testdata/*.jpeg")
	if err != nil || len(paths) == 0 {
		t.Fatalf("JPEG test corpus: %v", err)
	}
	return paths
}

func readJPEG(t testing.TB, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func samePixels(a, b image.Image) bool {
	if a.Bounds() != b.Bounds() {
		return false
	}
	for y := 0; y < a.Bounds().Dy(); y++ {
		for x := 0; x < a.Bounds().Dx(); x++ {
			ar, ag, ab, aa := a.At(x, y).RGBA()
			br, bg, bb, ba := b.At(x, y).RGBA()
			if ar != br || ag != bg || ab != bb || aa != ba {
				return false
			}
		}
	}
	return true
}

func TestCorpusScales(t *testing.T) {
	for _, path := range corpus(t) {
		t.Run(filepath.Base(path), func(t *testing.T) {
			t.Parallel()
			data := readJPEG(t, path)
			want, err := jpeg.Decode(bytes.NewReader(data))
			if err != nil {
				t.Fatal(err)
			}
			cfg, err := DecodeConfig(bytes.NewReader(data))
			if err != nil || image.Rect(0, 0, cfg.Width, cfg.Height) != want.Bounds() {
				t.Fatalf("config %v, %v; image bounds %v", cfg, err, want.Bounds())
			}
			for _, factor := range []int{1, 2, 4, 8} {
				d := decoder{factor: factor}
				got, err := d.decode(bytes.NewReader(data), false)
				if err != nil {
					t.Fatalf("factor %d: %v", factor, err)
				}
				rect := image.Rect(0, 0, (cfg.Width+factor-1)/factor, (cfg.Height+factor-1)/factor)
				if got.Bounds() != rect || reflect.TypeOf(got) != reflect.TypeOf(want) {
					t.Fatalf("factor %d: got %v %T, want %v %T", factor, got.Bounds(), got, rect, want)
				}
				if factor == 1 && !samePixels(got, want) {
					t.Fatal("full resolution differs from image/jpeg")
				}
				// Check every internal pixel allocation, including padding
				// and intermediate CMYK/RGB planes, not just returned bounds.
				bs := 8 / factor
				mxx := (cfg.Width + 8*d.maxH - 1) / (8 * d.maxH)
				myy := (cfg.Height + 8*d.maxV - 1) / (8 * d.maxV)
				if im := d.img1; im != nil && len(im.Pix) > bs*bs*mxx*myy {
					t.Fatalf("factor %d: oversized grayscale buffer", factor)
				}
				if im := d.img3; im != nil {
					pixels := bs * bs * d.maxH * d.maxV * mxx * myy
					if len(im.Y) > pixels || len(im.Cb) > pixels || len(im.Cr) > pixels || len(d.blackPix) > pixels {
						t.Fatalf("factor %d: oversized color buffers", factor)
					}
				}
				for i := 0; i < d.nComp; i++ {
					if d.progCoeffs[i] == nil {
						continue
					}
					blocks := mxx * myy * d.comp[i].h * d.comp[i].v
					if len(d.progCoeffs[i]) != blocks*bs*bs {
						t.Fatalf("factor %d component %d: full-size progressive coefficients retained", factor, i)
					}
					if factor != 1 && len(d.progNonzero[i]) != blocks {
						t.Fatalf("factor %d component %d: incorrect nonzero mask allocation", factor, i)
					}
				}
			}
		})
	}
}

func TestProgressiveScales(t *testing.T) {
	for _, path := range corpus(t) {
		if !strings.HasSuffix(path, ".progressive.jpeg") {
			continue
		}
		t.Run(filepath.Base(path), func(t *testing.T) {
			t.Parallel()
			prog := readJPEG(t, path)
			seq := readJPEG(t, strings.TrimSuffix(path, ".progressive.jpeg")+".jpeg")
			for _, factor := range []int{1, 2, 4, 8} {
				a, err := Decode(bytes.NewReader(seq), factor)
				if err != nil {
					t.Fatal(err)
				}
				b, err := Decode(bytes.NewReader(prog), factor)
				if err != nil {
					t.Fatal(err)
				}
				if !samePixels(a, b) {
					t.Fatalf("factor %d: sequential and progressive pixels differ", factor)
				}
			}
		})
	}
}

func TestProgressiveRetainedCoefficients(t *testing.T) {
	// Verify that omitting high-frequency values does not change either the
	// retained coefficients or the parsing of subsequent refinement scans.
	for _, path := range corpus(t) {
		if !strings.Contains(path, ".progressive.") {
			continue
		}
		data := readJPEG(t, path)
		full := decoder{factor: 1}
		if _, err := full.decode(bytes.NewReader(data), false); err != nil {
			t.Fatal(err)
		}
		for _, factor := range []int{2, 4, 8} {
			small := decoder{factor: factor}
			if _, err := small.decode(bytes.NewReader(data), false); err != nil {
				t.Fatal(err)
			}
			bs := 8 / factor
			for c := 0; c < full.nComp; c++ {
				for index := 0; index < len(full.progCoeffs[c])/64; index++ {
					ref := full.progCoeffs[c][index*64 : (index+1)*64]
					var got block
					small.loadProgressiveBlock(&got, c, index)
					for u, v := range ref {
						if u/8 < bs && u%8 < bs && got[u] != v {
							t.Fatalf("%s factor %d component %d block %d coefficient %d: got %d, want %d",
								path, factor, c, index, u, got[u], v)
						}
						if (got[u] != 0) != (v != 0) {
							t.Fatalf("%s factor %d component %d block %d: nonzero mask differs", path, factor, c, index)
						}
					}
				}
			}
		}
	}
}

func TestProgressiveSparseMask(t *testing.T) {
	for _, factor := range []int{2, 4, 8} {
		bs := 8 / factor
		d := decoder{factor: factor}
		d.progCoeffs[0] = make([]int32, bs*bs)
		// All these high frequencies are discarded at every reduced scale.
		// Exercise both sides of a 32-bit word boundary and the top bit.
		mask := uint64(1)<<31 | uint64(1)<<32 | uint64(1)<<63
		// A stale DC bit must be overwritten by the actual retained zero.
		d.progNonzero[0] = []uint64{mask | 1}
		var b block
		d.loadProgressiveBlock(&b, 0, 0)
		if b[0] != 0 || b[31] != 1 || b[32] != 1 || b[63] != 1 {
			t.Fatalf("factor %d: incorrect sparse-mask expansion: %v", factor, b)
		}
		b[0] = -53
		d.saveProgressiveBlock(&b, 0, 0, mask)
		b = block{}
		d.loadProgressiveBlock(&b, 0, 0)
		if b[0] != -53 || b[31] != 1 || b[32] != 1 || b[63] != 1 {
			t.Fatalf("factor %d: lost coefficient or high mask bit", factor)
		}
	}
}

func TestQuantizationPrecision(t *testing.T) {
	for _, precision := range []int{0, 1} {
		var payload bytes.Buffer
		payload.WriteByte(byte(precision << 4))
		var quant block
		for zig := 0; zig < 64; zig++ {
			q := 1 + zig*3
			if precision == 1 {
				q += 257 // Nonuniform values that require 16-bit DQT.
				payload.WriteByte(byte(q >> 8))
			}
			payload.WriteByte(byte(q))
			quant[unzig[zig]] = int32(q)
		}
		for _, factor := range []int{1, 2, 4, 8} {
			d := decoder{factor: factor, nComp: 1, maxH: 1, maxV: 1, width: 8, height: 8}
			d.r = bytes.NewReader(payload.Bytes())
			if err := d.processDQT(payload.Len()); err != nil {
				t.Fatal(err)
			}
			if d.quant[0] != quant {
				t.Fatalf("precision %d: quantization table not in natural order", precision)
			}
			if err := d.makeImg(1, 1); err != nil {
				t.Fatal(err)
			}
			var b, dequantized block
			for i := range b {
				b[i] = int32(i%5) - 2
				dequantized[i] = b[i] * quant[i]
			}
			want := referenceScaledIDCT(dequantized, factor)
			if err := d.reconstructBlock(&b, 0, 0, 0); err != nil {
				t.Fatal(err)
			}
			bs := 8 / factor
			for y := 0; y < bs; y++ {
				for x := 0; x < bs; x++ {
					w := max(0, min(255, math.Floor(want[y*bs+x]+0.5)+128))
					v := float64(d.img1.Pix[y*d.img1.Stride+x])
					// The original 8x8 integer IDCT has lower precision;
					// one intensity level of error is permitted.
					if math.Abs(v-w) > 1 {
						t.Fatalf("precision %d factor %d (%d,%d): got %v, want %v",
							precision, factor, x, y, v, w)
					}
				}
			}
		}
	}
}

func TestSmallAndOddImages(t *testing.T) {
	for _, size := range []image.Point{{1, 1}, {1, 17}, {17, 1}, {3, 5}, {7, 9}, {8, 8}, {17, 33}, {257, 129}} {
		src := image.NewGray(image.Rectangle{Max: size})
		for y := 0; y < size.Y; y++ {
			for x := 0; x < size.X; x++ {
				// Each original 8x8 block is constant, so all scales should
				// retain its exact intensity, including incomplete edges.
				src.SetGray(x, y, color.Gray{Y: byte((x/8*43 + y/8*77) & 255)})
			}
		}
		var data bytes.Buffer
		if err := jpeg.Encode(&data, src, &jpeg.Options{Quality: 100}); err != nil {
			t.Fatal(err)
		}
		for _, factor := range []int{1, 2, 4, 8} {
			t.Run(fmt.Sprintf("%dx%d/%d", size.X, size.Y, factor), func(t *testing.T) {
				got, err := Decode(bytes.NewReader(data.Bytes()), factor)
				if err != nil {
					t.Fatal(err)
				}
				bounds := image.Rect(0, 0, (size.X+factor-1)/factor, (size.Y+factor-1)/factor)
				if got.Bounds() != bounds {
					t.Fatalf("got %v, want %v", got.Bounds(), bounds)
				}
				for y := 0; y < bounds.Dy(); y++ {
					for x := 0; x < bounds.Dx(); x++ {
						want := src.GrayAt(x*factor, y*factor)
						if got.At(x, y) != want {
							t.Fatalf("pixel (%d,%d): got %v, want %v", x, y, got.At(x, y), want)
						}
					}
				}
			})
		}
	}
}

func TestInvalidFactors(t *testing.T) {
	for _, factor := range []int{-8, -1, 0, 3, 5, 16} {
		r := bytes.NewReader([]byte("untouched"))
		_, err := Decode(r, factor)
		if _, ok := err.(UnsupportedError); !ok || r.Len() != len("untouched") {
			t.Fatalf("factor %d: err %v, remaining %d", factor, err, r.Len())
		}
	}
}

func TestAllocationSize(t *testing.T) {
	limit := uint64(^uint(0) >> 1)
	for _, size := range []uint64{0, 1, limit} {
		if err := checkAllocationSize(size); err != nil {
			t.Errorf("size %d: %v", size, err)
		}
	}
	if err := checkAllocationSize(limit + 1); err == nil {
		t.Fatal("accepted allocation that overflows int")
	}
}

func TestZeroDimensions(t *testing.T) {
	src := readJPEG(t, "testdata/video-001.jpeg")
	i := bytes.Index(src, []byte{0xff, sof0Marker})
	if i < 0 {
		t.Fatal("missing SOF marker in test image")
	}
	for _, offset := range []int{5, 7} { // Height and width within the SOF.
		data := bytes.Clone(src)
		data[i+offset], data[i+offset+1] = 0, 0
		if _, err := DecodeConfig(bytes.NewReader(data)); err == nil {
			t.Fatal("config accepted zero dimension")
		}
		for _, factor := range []int{1, 2, 4, 8} {
			if _, err := Decode(bytes.NewReader(data), factor); err == nil {
				t.Fatalf("factor %d: accepted zero dimension", factor)
			}
		}
	}
}

func TestStreamingAndErrors(t *testing.T) {
	data := readJPEG(t, "testdata/video-001.progressive.jpeg")
	for _, factor := range []int{1, 2, 4, 8} {
		want, err := Decode(bytes.NewReader(data), factor)
		if err != nil {
			t.Fatal(err)
		}
		got, err := Decode(iotest.OneByteReader(bytes.NewReader(data)), factor)
		if err != nil || !samePixels(got, want) {
			t.Fatalf("factor %d: streaming result differs: %v", factor, err)
		}
		got, err = Decode(iotest.DataErrReader(bytes.NewReader(data)), factor)
		if err != nil || !samePixels(got, want) {
			t.Fatalf("factor %d: data+EOF result differs: %v", factor, err)
		}
		for _, cut := range []int{0, 1, 24, len(data) / 2, len(data) - 2} {
			if _, err := Decode(bytes.NewReader(data[:cut]), factor); err == nil {
				t.Fatalf("factor %d cut %d: accepted truncated input", factor, cut)
			}
		}
		errRead := fmt.Errorf("reader failed")
		if _, err := Decode(iotest.ErrReader(errRead), factor); err != errRead {
			t.Fatalf("factor %d: got %v, want reader error", factor, err)
		}
	}
}

func TestImageRegistrationUnchanged(t *testing.T) {
	// Importing this package must not replace image/jpeg's default decoder.
	data := readJPEG(t, "testdata/video-001.jpeg")
	got, format, err := image.Decode(bytes.NewReader(data))
	want, wantErr := jpeg.Decode(bytes.NewReader(data))
	if err != nil || wantErr != nil || format != "jpeg" || !reflect.DeepEqual(got, want) {
		t.Fatalf("image.Decode changed: format %q, err %v", format, err)
	}
}

func FuzzDecode(f *testing.F) {
	for _, path := range corpus(f) {
		f.Add(readJPEG(f, path))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		cfg, err := jpeg.DecodeConfig(bytes.NewReader(data))
		if err != nil || cfg.Width > 1024 || cfg.Height > 1024 {
			return // Bound source dimensions, including progressive metadata.
		}
		for _, factor := range []int{1, 2, 4, 8} {
			got, err := Decode(bytes.NewReader(data), factor)
			if err != nil {
				continue
			}
			want := image.Rect(0, 0, (cfg.Width+factor-1)/factor, (cfg.Height+factor-1)/factor)
			if got.Bounds() != want {
				t.Fatalf("factor %d: got %v, want %v", factor, got.Bounds(), want)
			}
		}
	})
}

func BenchmarkDecode(b *testing.B) {
	// Match the existing fastjpeg benchmark's nonuniform 4000x4000 input.
	src := image.NewRGBA(image.Rect(0, 0, 4000, 4000))
	for i := range src.Pix {
		src.Pix[i] = byte(i)
		if i%4 == 3 {
			src.Pix[i] = 255
		}
	}
	var encoded bytes.Buffer
	if err := jpeg.Encode(&encoded, src, nil); err != nil {
		b.Fatal(err)
	}
	for _, input := range []struct {
		name string
		data []byte
	}{
		{"4000x4000", encoded.Bytes()},
		{"progressive", readJPEG(b, "testdata/video-001.progressive.jpeg")},
	} {
		b.Run(input.name, func(b *testing.B) {
			for _, factor := range []int{1, 2, 4, 8} {
				b.Run(fmt.Sprint(factor), func(b *testing.B) {
					b.ReportAllocs()
					for b.Loop() {
						if _, err := Decode(bytes.NewReader(input.data), factor); err != nil {
							b.Fatal(err)
						}
					}
				})
			}
			b.Run("stdlib", func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					if _, err := jpeg.Decode(bytes.NewReader(input.data)); err != nil {
						b.Fatal(err)
					}
				}
			})
		})
	}
}

// Assert the decoder's public API uses an io.Reader, not a seekable reader or
// a byte slice containing the whole compressed image.
var _ func(io.Reader, int) (image.Image, error) = Decode
