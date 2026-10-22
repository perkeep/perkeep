//go:build (linux || darwin) && (amd64 || arm64) && !android && !ios

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

package fastjpeg

// This is deliberately a test-only prototype. See README.md for the findings
// and the remaining work before replacing the subprocess in production.

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"testing/iotest"

	"github.com/ebitengine/purego"

	"perkeep.org/internal/images/resize"
)

// The TurboJPEG API avoids libjpeg's version-dependent public structs and
// contains its setjmp/longjmp error handling entirely within C.
//
// Signatures and constants:
// https://github.com/libjpeg-turbo/libjpeg-turbo/blob/2.1.5/turbojpeg.h
// C int is int32, NOT Go int. C unsigned long is uintptr on these platforms,
// but not on Windows, which would need different bindings.
type turboJPEG struct {
	initDecompress   func() uintptr
	destroy          func(uintptr) int32
	decompressHeader func(uintptr, *byte, uintptr, *int32, *int32, *int32, *int32) int32
	decompress       func(uintptr, *byte, uintptr, *byte, int32, int32, int32, int32, int32) int32
	errorString      func(uintptr) string
}

func openTurboJPEG(t testing.TB) *turboJPEG {
	t.Helper()
	names := []string{"libturbojpeg.so.0"}
	if runtime.GOOS == "darwin" {
		names = []string{"libturbojpeg.0.dylib",
			"/opt/homebrew/lib/libturbojpeg.0.dylib",
			"/usr/local/lib/libturbojpeg.0.dylib"}
	}
	var lib uintptr
	var err error
	for _, name := range names {
		lib, err = purego.Dlopen(name, purego.RTLD_NOW|purego.RTLD_LOCAL)
		if err == nil {
			break
		}
	}
	if lib == 0 {
		t.Skipf("libturbojpeg unavailable: %v", err)
	}
	t.Cleanup(func() {
		if err := purego.Dlclose(lib); err != nil {
			t.Error(err)
		}
	})
	api := new(turboJPEG)
	for _, sym := range []struct {
		name string
		fn   any
	}{
		{"tjInitDecompress", &api.initDecompress},
		{"tjDestroy", &api.destroy},
		{"tjDecompressHeader3", &api.decompressHeader},
		{"tjDecompress2", &api.decompress},
		{"tjGetErrorStr2", &api.errorString},
	} {
		addr, err := purego.Dlsym(lib, sym.name)
		if err != nil {
			t.Fatalf("libturbojpeg symbol %s: %v", sym.name, err)
		}
		purego.RegisterFunc(sym.fn, addr)
	}
	return api
}

func (api *turboJPEG) decode(r io.Reader, factor int) (image.Image, error) {
	switch factor {
	case 1, 2, 4, 8:
	default:
		return nil, fmt.Errorf("fastjpeg: unsupported sample factor %d", factor)
	}
	fail := func(err error) (image.Image, error) {
		// images.Decode retries with the standard library for this error type.
		return nil, DjpegFailedError{Err: err}
	}
	data, err := io.ReadAll(r)
	if err != nil {
		return fail(err)
	}
	if len(data) == 0 {
		return fail(errors.New("fastjpeg: empty JPEG"))
	}
	// In libturbojpeg 2.x even errors associated with a nonzero handle can
	// use thread-local error text. Keep native operations and error retrieval
	// on the same OS thread.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	handle := api.initDecompress()
	if handle == 0 {
		return fail(errors.New("fastjpeg: " + api.errorString(0)))
	}

	// The decoder can retain the source pointer between calls. Pin it until
	// after tjDestroy, including on error paths. No Go pointers are stored in
	// these buffers. Each decode has its own handle, so calls may be concurrent.
	var pins runtime.Pinner
	defer pins.Unpin()
	pins.Pin(&data[0])
	defer api.destroy(handle)
	var hdr [4]int32 // width, height, subsampling, color space
	pins.Pin(&hdr[0])
	if api.decompressHeader(handle, &data[0], uintptr(len(data)),
		&hdr[0], &hdr[1], &hdr[2], &hdr[3]) != 0 {
		return fail(errors.New("fastjpeg: " + api.errorString(handle)))
	}
	if hdr[0] <= 0 || hdr[1] <= 0 {
		return fail(errors.New("fastjpeg: invalid JPEG dimensions"))
	}
	const (
		tjcsRGB   = 0
		tjcsYCbCr = 1
		tjcsGray  = 2
		tjpfGray  = 6
		tjpfRGBA  = 7
	)
	bpp, pf := 4, int32(tjpfRGBA)
	switch hdr[3] {
	case tjcsRGB, tjcsYCbCr:
	case tjcsGray:
		bpp, pf = 1, tjpfGray
	default:
		return fail(fmt.Errorf("fastjpeg: unsupported JPEG color space %d", hdr[3]))
	}
	w := (int(hdr[0]) + factor - 1) / factor
	h := (int(hdr[1]) + factor - 1) / factor
	// pitch is a C int; allocation length is a Go int. Check both before
	// handing a buffer to native code, which cannot check its actual length.
	if w > (1<<31-1)/bpp || h > int(^uint(0)>>1)/(w*bpp) {
		return fail(errors.New("fastjpeg: JPEG dimensions overflow"))
	}
	var im image.Image
	var pix []byte
	rect := image.Rect(0, 0, w, h)
	if pf == tjpfGray {
		gray := image.NewGray(rect)
		im, pix = gray, gray.Pix
	} else {
		rgba := image.NewRGBA(rect)
		im, pix = rgba, rgba.Pix
	}
	pins.Pin(&pix[0])
	// Use accurate DCT and fancy chroma upsampling, as djpeg does by
	// default. Do not attribute a quality-for-speed tradeoff to purego.
	// LIMITSCANS bounds pathological progressive images (not a full sandbox).
	const flags = 4096 | 32768 // TJFLAG_ACCURATEDCT | TJFLAG_LIMITSCANS
	if api.decompress(handle, &data[0], uintptr(len(data)), &pix[0],
		int32(w), int32(w*bpp), int32(h), pf, flags) != 0 {
		// Conservatively reject warnings, too. tjGetErrorCode's warning bit
		// alone doesn't establish that all output rows were written.
		return fail(errors.New("fastjpeg: " + api.errorString(handle)))
	}
	return im, nil
}

func TestPuregoDecode(t *testing.T) {
	api := openTurboJPEG(t)
	// Odd dimensions exercise ceiling, not truncating, division.
	for _, gray := range []bool{false, true} {
		name := "rgba"
		var src image.Image = image.NewRGBA(image.Rect(0, 0, 257, 129))
		if gray {
			name = "gray"
			im := image.NewGray(src.Bounds())
			for i := range im.Pix {
				im.Pix[i] = byte(i)
			}
			src = im
		} else {
			im := src.(*image.RGBA)
			for i := range im.Pix {
				im.Pix[i] = byte(i)
				if i%4 == 3 {
					im.Pix[i] = 255
				}
			}
		}
		var buf bytes.Buffer
		if err := jpeg.Encode(&buf, src, nil); err != nil {
			t.Fatal(err)
		}
		for _, factor := range []int{1, 2, 4, 8} {
			t.Run(fmt.Sprintf("%s/%d", name, factor), func(t *testing.T) {
				got, err := api.decode(bytes.NewReader(buf.Bytes()), factor)
				if err != nil {
					t.Fatal(err)
				}
				want := image.Rect(0, 0, (257+factor-1)/factor, (129+factor-1)/factor)
				if got.Bounds() != want || reflect.TypeOf(got) != reflect.TypeOf(src) {
					t.Fatalf("got %v %T; want %v %T", got.Bounds(), got, want, src)
				}
				if im, ok := got.(*image.RGBA); ok {
					for i := 3; i < len(im.Pix); i += 4 {
						if im.Pix[i] != 255 {
							t.Fatal("RGBA output is not opaque")
						}
					}
				}
				if Available() {
					ref, err := DecodeDownsample(bytes.NewReader(buf.Bytes()), factor)
					if err != nil {
						t.Fatal(err)
					}
					if !reflect.DeepEqual(got, ref) {
						t.Fatal("output differs from djpeg")
					}
				}
			})
		}
	}
}

func TestPuregoErrors(t *testing.T) {
	api := openTurboJPEG(t)
	for _, data := range [][]byte{nil, []byte("not JPEG"), {0xff, 0xd8, 0xff, 0xd9}} {
		_, err := api.decode(bytes.NewReader(data), 8)
		var failed DjpegFailedError
		if !errors.As(err, &failed) {
			t.Fatalf("got %v (%T), want DjpegFailedError", err, err)
		}
	}
	r := bytes.NewReader([]byte("not read"))
	if _, err := api.decode(r, 3); err == nil || r.Len() != len("not read") {
		t.Fatalf("invalid factor: err %v, reader has %d bytes", err, r.Len())
	}
	readErr := errors.New("read error")
	_, err := api.decode(iotest.ErrReader(readErr), 8)
	var failed DjpegFailedError
	if !errors.As(err, &failed) || !errors.Is(failed.Err, readErr) {
		t.Fatalf("got %v; want wrapped reader error", err)
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, image.NewGray(image.Rect(0, 0, 17, 19)), nil); err != nil {
		t.Fatal(err)
	}
	// Missing EOI produces a native warning after the header was accepted.
	_, err = api.decode(bytes.NewReader(buf.Bytes()[:buf.Len()-2]), 2)
	if !errors.As(err, &failed) {
		t.Fatalf("missing EOI: got %v, want fallback error", err)
	}
}

func TestPuregoSmallImages(t *testing.T) {
	api := openTurboJPEG(t)
	for _, size := range []image.Point{{1, 1}, {1, 17}, {17, 1}, {3, 3}, {7, 5}, {16, 16}} {
		src := image.NewGray(image.Rectangle{Max: size})
		for i := range src.Pix {
			src.Pix[i] = byte(i * 83)
		}
		var buf bytes.Buffer
		if err := jpeg.Encode(&buf, src, nil); err != nil {
			t.Fatal(err)
		}
		for _, factor := range []int{1, 2, 4, 8} {
			im, err := api.decode(bytes.NewReader(buf.Bytes()), factor)
			if err != nil {
				t.Fatalf("%v factor %d: %v", size, factor, err)
			}
			want := image.Rect(0, 0, (size.X+factor-1)/factor, (size.Y+factor-1)/factor)
			if im.Bounds() != want {
				t.Fatalf("%v factor %d: got %v, want %v", size, factor, im.Bounds(), want)
			}
			// tjDecompress2 chooses the largest fractional scale fitting
			// the requested bounds. On tiny images that may be larger than
			// 1/factor, with identical bounds but different pixel values.
			// Do not require pixel parity with djpeg here.
		}
	}
}

func TestPuregoFallbackReplay(t *testing.T) {
	api := openTurboJPEG(t)
	data, err := os.ReadFile(filepath.Join(runtime.GOROOT(), "src/image/testdata/video-001.cmyk.jpeg"))
	if err != nil {
		t.Fatal(err)
	}
	// Mirror images.go's fallback after the native decoder consumed its input.
	r := bytes.NewReader(data)
	var replay bytes.Buffer
	_, err = api.decode(io.TeeReader(r, &replay), 8)
	var failed DjpegFailedError
	if !errors.As(err, &failed) {
		t.Fatalf("got %v; want fallback error", err)
	}
	got, err := jpeg.Decode(io.MultiReader(&replay, r))
	if err != nil {
		t.Fatal(err)
	}
	want, err := jpeg.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatal("fallback did not reproduce stdlib decode")
	}
}

func TestPuregoCorpus(t *testing.T) {
	api := openTurboJPEG(t)
	paths, err := filepath.Glob(filepath.Join(runtime.GOROOT(), "src/image/testdata/*.jpeg"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("Go JPEG test corpus unavailable: %v", err)
	}
	for _, path := range paths {
		t.Run(filepath.Base(path), func(t *testing.T) {
			t.Parallel()
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			cfg, err := jpeg.DecodeConfig(bytes.NewReader(data))
			if err != nil {
				t.Fatal(err)
			}
			for _, factor := range []int{1, 2, 4, 8} {
				im, err := api.decode(bytes.NewReader(data), factor)
				switch filepath.Base(path) {
				case "video-001.cmyk.jpeg",
					"video-001.q50.121121.jpeg",
					"video-001.q50.211211.jpeg",
					"video-001.q50.221122.jpeg",
					"video-001.q50.222112.jpeg",
					"video-001.q50.410.jpeg",
					"video-001.q50.410.progressive.jpeg":
					// Explicitly exercise the stdlib fallback contract for
					// CMYK and unsupported chroma layouts.
					var failed DjpegFailedError
					if !errors.As(err, &failed) {
						t.Fatalf("factor %d: got %v, want fallback error", factor, err)
					}
					continue
				}
				if err != nil {
					t.Fatalf("factor %d: %v", factor, err)
				}
				want := image.Rect(0, 0, (cfg.Width+factor-1)/factor, (cfg.Height+factor-1)/factor)
				if im.Bounds() != want {
					t.Fatalf("factor %d: got %v, want %v", factor, im.Bounds(), want)
				}
			}
		})
	}
}

// BenchmarkJPEG compares decode-only and complete 128x128 thumbnail paths.
// It includes io.ReadAll, handle creation/destruction, and all Go allocations
// in the purego timings, but not dlopen/dlsym (a one-time startup cost).
func BenchmarkJPEG(b *testing.B) {
	api := openTurboJPEG(b)
	for _, size := range []int{256, 4000} {
		data := benchmarkJPEG(b, size)
		for _, factor := range []int{1, 2, 4, 8} {
			b.Run(fmt.Sprintf("%d/factor%d", size, factor), func(b *testing.B) {
				type decoder struct {
					name string
					fn   func(io.Reader) (image.Image, error)
				}
				decoders := []decoder{
					{"purego", func(r io.Reader) (image.Image, error) { return api.decode(r, factor) }},
				}
				if Available() {
					decoders = append(decoders, decoder{"djpeg", func(r io.Reader) (image.Image, error) {
						return DecodeDownsample(r, factor)
					}})
				}
				// Stdlib cannot decode at reduced resolution. Only run it
				// once per size, not once for each native scaling factor.
				if factor == 1 {
					decoders = append(decoders, decoder{"stdlib", jpeg.Decode})
				}
				for _, decoder := range decoders {
					for _, thumbnail := range []bool{false, true} {
						name := decoder.name + "/decode"
						if thumbnail {
							name = decoder.name + "/thumbnail"
						}
						b.Run(name, func(b *testing.B) {
							b.ReportAllocs()
							b.SetBytes(int64(len(data)))
							for b.Loop() {
								im, err := decoder.fn(bytes.NewReader(data))
								if err != nil {
									b.Fatal(err)
								}
								if thumbnail {
									im = resize.Resize(im, im.Bounds(), 128, 128)
									if err := jpeg.Encode(io.Discard, im, nil); err != nil {
										b.Fatal(err)
									}
								}
							}
						})
					}
				}
			})
		}
	}
}

func benchmarkJPEG(b *testing.B, size int) []byte {
	b.Helper()
	im := image.NewRGBA(image.Rect(0, 0, size, size))
	// Deterministic spatial variation, rather than a uniform fill that
	// unrealistically favors JPEG decompression. Match benchfastjpeg's
	// striped pattern, while also including a small image.
	for i := range im.Pix {
		im.Pix[i] = byte(i)
		if i%4 == 3 {
			im.Pix[i] = 255
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, im, nil); err != nil {
		b.Fatal(err)
	}
	b.Logf("%dx%d: %d JPEG bytes", size, size, buf.Len())
	return buf.Bytes()
}
