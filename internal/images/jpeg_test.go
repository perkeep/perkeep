/*
Copyright 2026 The Perkeep Authors.

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
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/bradfitz/resizejpeg"
)

func encodedJPEG(t testing.TB, width, height int) []byte {
	t.Helper()
	src := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := range height {
		for x := range width {
			i := src.PixOffset(x, y)
			src.Pix[i+0] = uint8(3*x + 5*y)
			src.Pix[i+1] = uint8(7*x - 2*y)
			src.Pix[i+2] = uint8(x ^ y)
			src.Pix[i+3] = 255
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, src, nil); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestJPEGThumbnailParity(t *testing.T) {
	data := encodedJPEG(t, 513, 385)
	for _, tt := range []struct {
		name string
		opts *resizejpeg.ResizeOptions
	}{
		{"default", nil},
		{"lowCPU", &resizejpeg.ResizeOptions{
			Filter: resizejpeg.Bilinear, ThumbnailHeadroom: 2, Workers: 1,
			AllowFullDecode: true,
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			for _, bounds := range []image.Point{{31, 23}, {513, 385}} {
				target := bounds
				if bounds.X == 31 {
					target = image.Pt(30, 23)
				}
				opts := &DecodeOpts{
					MaxWidth: bounds.X, MaxHeight: bounds.Y,
					JPEGOptions: tt.opts,
				}
				got, conf, err := Decode(bytes.NewReader(data), opts)
				if err != nil {
					t.Fatal(err)
				}
				wantOpts := resizejpeg.ResizeOptions{AllowFullDecode: true}
				if tt.opts != nil {
					wantOpts = *tt.opts
				}
				want, err := resizejpeg.DecodeThumbnail(bytes.NewReader(data), target.X, target.Y, &wantOpts)
				if err != nil {
					t.Fatal(err)
				}
				if !equals(got, want) {
					t.Errorf("target %v: pixels differ from resizejpeg", target)
				}
				if conf.Format != "jpeg" || conf.Width != target.X || conf.Height != target.Y ||
					conf.Modified != (target.X != 513 || target.Y != 385) {
					t.Errorf("target %v: unexpected Config %+v", target, conf)
				}
			}
		})
	}
}

func TestJPEGMemoryAdmission(t *testing.T) {
	data := encodedJPEG(t, 129, 97)
	denied := errors.New("memory admission denied")
	for _, resize := range []bool{false, true} {
		for _, deny := range []bool{false, true} {
			for _, truncated := range []bool{false, true} {
				name := fmt.Sprintf("resize=%t/deny=%t/truncated=%t", resize, deny, truncated)
				t.Run(name, func(t *testing.T) {
					calls, releases := 0, 0
					jpegOpts := resizejpeg.ResizeOptions{
						MemAllocSemaphore: func(n int64) (func(), error) {
							calls++
							if n <= 0 {
								t.Errorf("nonpositive reservation: %d", n)
							}
							if deny {
								return nil, denied
							}
							return func() { releases++ }, nil
						},
					}
					opts := &DecodeOpts{JPEGOptions: &jpegOpts}
					if resize {
						opts.MaxWidth, opts.MaxHeight = 31, 23
					}
					input := data
					if truncated {
						// The header is intact, but entropy decoding fails
						// after the hook has admitted the allocation.
						input = data[:len(data)-64]
					}
					got, _, err := Decode(bytes.NewReader(input), opts)
					switch {
					case deny:
						if err != denied || got != nil {
							t.Fatalf("denied Decode = (%v, %v); want (nil, admission error)", got, err)
						}
					case truncated:
						if err == nil || got != nil {
							t.Fatalf("truncated Decode = (%v, %v); want (nil, error)", got, err)
						}
					default:
						if err != nil {
							t.Fatal(err)
						}
					}
					wantReleases := 1
					if deny {
						wantReleases = 0
					}
					if calls != 1 || releases != wantReleases {
						t.Errorf("hook called %d times, released %d; want 1, %d", calls, releases, wantReleases)
					}
				})
			}
		}
	}
}

func TestJPEGOptionsPreserved(t *testing.T) {
	data := encodedJPEG(t, 65, 49)
	jpegOpts := resizejpeg.ResizeOptions{
		Filter: resizejpeg.CatmullRom, ThumbnailHeadroom: 2, Workers: 1,
		AllowFullDecode: true, MaxSourcePixels: 65 * 49, MaxMemoryBytes: 16 << 20,
	}
	orig := jpegOpts
	opts := &DecodeOpts{JPEGOptions: &jpegOpts}
	for range 2 {
		if _, _, err := Decode(bytes.NewReader(data), opts); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(jpegOpts, orig) {
			t.Fatalf("JPEGOptions modified: got %+v, want %+v", jpegOpts, orig)
		}
	}
	// Supplied policies also apply when no resize was requested.
	for _, policy := range []resizejpeg.ResizeOptions{
		{MaxSourcePixels: 1},
		{MaxMemoryBytes: 1},
		{Workers: -1},
		{ThumbnailHeadroom: -1},
	} {
		opts.JPEGOptions = &policy
		got, _, err := Decode(bytes.NewReader(data), opts)
		if err == nil || got != nil {
			t.Errorf("policy %+v: got (%v, %v); want (nil, error)", policy, got, err)
		}
	}
}

func TestJPEGNilOptions(t *testing.T) {
	data := encodedJPEG(t, 65, 49)
	got, conf, err := Decode(bytes.NewReader(data), nil)
	if err != nil {
		t.Fatal(err)
	}
	want, err := jpeg.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if reflect.TypeOf(got) != reflect.TypeOf(want) || !equals(got, want) {
		t.Fatal("nil options did not preserve standard JPEG decoding")
	}
	if conf.Modified {
		t.Fatal("nil options marked an unmodified JPEG as modified")
	}
}

func TestJPEGNoUpscaleRotated(t *testing.T) {
	data := encodedJPEG(t, 65, 49)
	for _, rotation := range []int{-90, 90} {
		for _, bounds := range []image.Point{{}, {200, 200}, {49, 65}} {
			got, conf, err := Decode(bytes.NewReader(data), &DecodeOpts{
				Rotate: rotation, MaxWidth: bounds.X, MaxHeight: bounds.Y,
			})
			if err != nil {
				t.Fatal(err)
			}
			if got.Bounds() != image.Rect(0, 0, 49, 65) || conf.Width != 49 || conf.Height != 65 {
				t.Errorf("rotation %d bounds %v: got %v, config %+v", rotation, bounds, got.Bounds(), conf)
			}
		}
	}
}

func TestJPEGProgressive(t *testing.T) {
	// progressive.jpeg is video-001.q50.420.progressive.jpeg from
	// github.com/bradfitz/resizejpeg's Go image/jpeg-derived testdata,
	// distributed under the BSD license in progressive.jpeg.LICENSE.
	data, err := os.ReadFile(filepath.Join(datadir, "progressive.jpeg"))
	if err != nil {
		t.Fatal(err)
	}
	for _, resize := range []bool{false, true} {
		for _, tt := range []struct {
			name string
			opts *resizejpeg.ResizeOptions
			fail bool
		}{
			{"default", nil, false},
			{"allowed", &resizejpeg.ResizeOptions{AllowFullDecode: true}, false},
			{"disabled", &resizejpeg.ResizeOptions{}, true},
		} {
			t.Run(fmt.Sprintf("%s/resize=%t", tt.name, resize), func(t *testing.T) {
				opts := &DecodeOpts{JPEGOptions: tt.opts}
				if resize {
					opts.MaxWidth, opts.MaxHeight = 32, 32
				}
				got, conf, err := Decode(bytes.NewReader(data), opts)
				if tt.fail {
					var unsupported resizejpeg.UnsupportedError
					if !errors.As(err, &unsupported) || got != nil {
						t.Fatalf("disabled progressive = (%v, %v); want (nil, UnsupportedError)", got, err)
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				if got == nil || conf.Format != "jpeg" || conf.Modified != resize {
					t.Errorf("unexpected progressive result: %v, %+v", got, conf)
				}
			})
		}
	}
}

func TestJPEGOptionsIgnorePNG(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 65, 49))
	var data bytes.Buffer
	if err := png.Encode(&data, src); err != nil {
		t.Fatal(err)
	}
	got, conf, err := Decode(bytes.NewReader(data.Bytes()), &DecodeOpts{
		MaxWidth: 32, MaxHeight: 24,
		JPEGOptions: &resizejpeg.ResizeOptions{
			Workers: -1,
			MemAllocSemaphore: func(int64) (func(), error) {
				t.Fatal("JPEG memory hook called for PNG")
				return nil, nil
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := rescale(src, 31, 24)
	if !equals(got, want) || conf.Format != "png" || !conf.Modified {
		t.Fatalf("PNG behavior changed: bounds %v, config %+v", got.Bounds(), conf)
	}
}
