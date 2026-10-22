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
	"image/jpeg"
	"os"
	"runtime"
	"testing"
)

var optionsBenchmarkResult *image.RGBA

// BenchmarkDecodeThumbnailOptions measures only pure-Go pixels, with automatic
// workers (the same worker policy as nil options). The default fixture is the
// deterministic textured 12MP JPEG used by BenchmarkDecodeResize. An optional
// RESIZEJPEG_OPTIONS_JPEG path uses the original file unchanged, without a
// download or re-encode. Input/config/preflight work is outside the leaf timer.
//
// Use a separate process for each fixed CPU limit, including initial calibration:
//
//	GOMAXPROCS=2 CGO_ENABLED=0 go test ./internal/images/resizejpeg \
//	  -run '^$' -bench '^BenchmarkDecodeThumbnailOptions$' -benchmem \
//	  -cpu=2 -benchtime=300ms -count=5
//
// A 2x-headroom setting only quarters intermediate pixels versus 4x when its
// actual IDCT factor doubles; labels and the factor metric report that factor.
func BenchmarkDecodeThumbnailOptions(b *testing.B) {
	var data []byte
	name := "generated-4000x3000-420"
	if path := os.Getenv("RESIZEJPEG_OPTIONS_JPEG"); path != "" {
		var err error
		data, err = os.ReadFile(path)
		if err != nil {
			b.Fatal(err)
		}
		name = "original-JPEG"
	} else {
		data = resizeStreamEncode(b, resizeStreamBenchmarkPhoto())
	}
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		b.Fatal(err)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || uint64(cfg.Width)*uint64(cfg.Height) > 25_000_000 {
		b.Fatal("options benchmark source exceeds the 25-megapixel limit")
	}
	// Match the pixels-only production-path benchmark: fixed width and a
	// floored short edge, rather than the visual report's rounded long edge.
	const width = 256
	height := max(1, int(int64(cfg.Height)*width/int64(cfg.Width)))
	if width > cfg.Width || height > cfg.Height {
		b.Skip("input is smaller than the 256-pixel-wide target; no upsampling")
	}
	b.Run(fmt.Sprintf("%s-%dx%d-to-%dx%d-workers=auto", name, cfg.Width, cfg.Height, width, height), func(b *testing.B) {
		for _, filter := range []struct {
			name  string
			value Filter
		}{
			{"Lanczos3", Lanczos3},
			{"CatmullRom", CatmullRom},
			{"Bilinear", Bilinear},
		} {
			for _, headroom := range []int{1, 2, 4} {
				opts := &ResizeOptions{
					Filter: filter.value, ThumbnailHeadroom: headroom,
					AllowFullDecode: true, MaxSourcePixels: 25_000_000,
				}
				factor := chooseThumbnailFactorHeadroom(cfg.Width, cfg.Height, width, height, headroom)
				label := fmt.Sprintf("filter=%s/headroom=%dx/IDCT=1of%d", filter.name, headroom, factor)
				if headroom == 4 && filter.value == Lanczos3 {
					label += "-default"
				}
				b.Run(label, func(b *testing.B) {
					// Verify the selector against the actual decoder outside
					// the measured loop, including source limits/factor-8 caps.
					got, d, err := decodeThumbnail(bytes.NewReader(data), width, height, opts)
					if err != nil {
						b.Fatal(err)
					}
					if d.factor != factor || got.Bounds() != image.Rect(0, 0, width, height) {
						b.Fatal("preflight returned incorrect factor or dimensions")
					}
					b.ReportAllocs()
					b.SetBytes(int64(len(data)))
					b.ResetTimer()
					for i := 0; i < b.N; i++ {
						got, err := DecodeThumbnail(bytes.NewReader(data), width, height, opts)
						if err != nil {
							b.Fatal(err)
						}
						optionsBenchmarkResult = got
					}
					b.StopTimer()
					b.ReportMetric(float64(factor), "factor")
					b.ReportMetric(float64(runtime.GOMAXPROCS(0)), "gomaxprocs")
				})
			}
		}
	})
}
