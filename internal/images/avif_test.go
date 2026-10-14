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
	"image"
	"image/color"
	"io"
	"os"
	"path/filepath"
	"testing"

	"perkeep.org/internal/magic"
)

func TestAVIF(t *testing.T) {
	// sample.avif is a losslessly encoded 32x16 gradient generated with
	// avif.Encode and Options{Lossless: true, Speed: 10}. Its pixels are
	// NRGBA{R: x*8, G: y*16, B: 64, A: 255}.
	data, err := os.ReadFile(filepath.Join(datadir, "sample.avif"))
	if err != nil {
		t.Fatal(err)
	}
	if got := magic.MIMEType(data); got != "image/avif" {
		t.Fatalf("MIMEType = %q; want image/avif", got)
	}

	// Exercise both seekable and streaming readers; AVIF decoders may read
	// ahead while finding the image configuration.
	for _, streaming := range []bool{false, true} {
		name := "seekable"
		if streaming {
			name = "streaming"
		}
		t.Run(name, func(t *testing.T) {
			reader := func() io.Reader {
				r := bytes.NewReader(data)
				if streaming {
					return struct{ io.Reader }{r}
				}
				return r
			}
			t.Run("image.DecodeConfig", func(t *testing.T) {
				conf, format, err := image.DecodeConfig(reader())
				if err != nil {
					t.Fatal(err)
				}
				if format != "avif" || conf.Width != 32 || conf.Height != 16 {
					t.Errorf("got %q %dx%d; want avif 32x16", format, conf.Width, conf.Height)
				}
			})
			t.Run("images.DecodeConfig", func(t *testing.T) {
				conf, err := DecodeConfig(reader())
				if err != nil {
					t.Fatal(err)
				}
				if conf.Format != "avif" || conf.Width != 32 || conf.Height != 16 {
					t.Errorf("got %+v; want avif 32x16", conf)
				}
			})
			for _, tt := range []struct {
				name   string
				opts   *DecodeOpts
				bounds image.Rectangle
			}{
				{"original", nil, image.Rect(0, 0, 32, 16)},
				{"unscaled", &DecodeOpts{MaxWidth: 64, MaxHeight: 64}, image.Rect(0, 0, 32, 16)},
				{"thumbnail", &DecodeOpts{MaxWidth: 8, MaxHeight: 8}, image.Rect(0, 0, 8, 4)},
			} {
				t.Run(tt.name, func(t *testing.T) {
					im, conf, err := Decode(reader(), tt.opts)
					if err != nil {
						t.Fatal(err)
					}
					if conf.Format != "avif" || im.Bounds() != tt.bounds {
						t.Errorf("got format %q, bounds %v; want avif, %v", conf.Format, im.Bounds(), tt.bounds)
					}
					if tt.name == "original" {
						for y := range 16 {
							for x := range 32 {
								want := color.NRGBA{R: uint8(x * 8), G: uint8(y * 16), B: 64, A: 255}
								if got := color.NRGBAModel.Convert(im.At(x, y)); got != want {
									t.Fatalf("pixel (%d, %d) = %v; want %v", x, y, got, want)
								}
							}
						}
					}
				})
			}
		})
	}
}
