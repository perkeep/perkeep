//go:build jpegcompare

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
	"expvar"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	xdraw "golang.org/x/image/draw"

	"perkeep.org/internal/images/fastjpeg"
	"perkeep.org/internal/images/resizejpeg"
)

// This opt-in experiment compares the actual old production thumbnail path
// with pure-Go 4x-headroom thumbnails and full-IDCT streaming Lanczos3, not
// with a full-size stdlib JPEG decode. It covers eight paths.
// For example, run in a fresh process with a fixed Go execution limit:
//
//	GOMAXPROCS=2 go test -tags jpegcompare ./internal/images -run '^$' \
//	  -bench '^BenchmarkJPEGThumbnailComparison$' -benchmem -cpu=2 -count=5
//
// By default it reuses benchfastjpeg_test.go's deterministic, init-encoded
// 4000x4000 striped jpegBytes. RESIZEJPEG_COMPARE_JPEG=/path/original.jpg
// instead reads that JPEG unchanged; source names include original dimensions.
// File I/O, fixture encoding, header validation, and preflight are not timed.
// All paths ignore EXIF orientation. "jpeg-encoded" additionally measures
// jpeg.Encode(io.Discard, im, nil); "pixels" ends at the thumbnail image.
//
// The streaming and thumbnail calls pass nil options: progressive/non-
// interleaved inputs fail preflight instead of silently using a full-buffer
// fallback. The default min(GOMAXPROCS, 8) IDCT/filter worker goroutines are
// further capped by source MCU rows, with entropy decoding and ordered
// collection also doing work. Thumbnail decoding chooses a power-of-two IDCT
// factor retaining 4x headroom where the source permits, then uses phase-aware
// Lanczos3. It is not equivalent to full-IDCT filtering. GOMAXPROCS is an
// execution limit, not a total goroutine count or a CPU limit on the djpeg
// child process. Each measured leaf reports its actual GOMAXPROCS.
// Keep it fixed in a separate process for each CPU selection,
// including initial calibration; classic b.N loops keep timer control explicit.
//
// The native controls use the real djpeg subprocess, not the separate purego
// prototype. Native scaled IDCT + Lanczos3 is NOT equivalent in quality to
// streaming full IDCT. Native full IDCT + Lanczos3 is a full-decode reference
// quality family, not a pixel-identical Go reference (IDCT/chroma differ).
// Only this comparative harness requires native availability; both pure-Go
// APIs themselves are independent of djpeg and native libraries.
// benchmem measures Go allocations only, excluding djpeg's child-process heap;
// it is neither total process-tree memory nor peak resident memory.

const jpegCompareMaxSourcePixels = 25_000_000

// Same scale-aware, three-lobe kernel as resizejpeg/quality_test.go.
var jpegCompareLanczos3 = &xdraw.Kernel{
	Support: 3,
	At: func(x float64) float64 {
		if x == 0 {
			return 1
		}
		x *= math.Pi
		return math.Sin(x) * math.Sin(x/3) / (x * x / 3)
	},
}

type jpegComparePath struct {
	name   string
	decode func() (image.Image, error)
}

type jpegComparePlan struct {
	edge, width, height, factor int
	paths                       []jpegComparePath
}

func jpegCompareRequireDjpeg(tb testing.TB) {
	tb.Helper()
	if !fastjpeg.Available() {
		tb.Skip("JPEG thumbnail comparison NOT covered: fastjpeg/djpeg unavailable (check PATH and CAMLI_DISABLE_DJPEG)")
	}
}

func jpegCompareConfig(tb testing.TB, data []byte) image.Config {
	tb.Helper()
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		tb.Fatalf("comparison source header: %v", err)
	}
	if format != "jpeg" {
		tb.Fatalf("comparison source format = %q, want jpeg", format)
	}
	// Check the original header before any path can allocate decoded pixels.
	if cfg.Width <= 0 || cfg.Height <= 0 ||
		uint64(cfg.Width)*uint64(cfg.Height) > jpegCompareMaxSourcePixels {
		tb.Fatalf("comparison source %dx%d exceeds the %d-pixel budget or has invalid dimensions",
			cfg.Width, cfg.Height, jpegCompareMaxSourcePixels)
	}
	return cfg
}

func jpegCompareMakePlan(tb testing.TB, data []byte, cfg image.Config, edge int) jpegComparePlan {
	tb.Helper()
	w, h := ScaledDimensions(cfg.Width, cfg.Height, edge, edge)
	opts := &DecodeOpts{Rotate: 0, Flip: FlipDirection(0), MaxWidth: edge, MaxHeight: edge}
	sw, sh, needRescale := opts.rescaleDimensions(image.Rect(0, 0, cfg.Width, cfg.Height), false)
	factor := fastjpeg.Factor(cfg.Width, cfg.Height, w, h)
	if w <= 0 || h <= 0 || !needRescale || sw != w || sh != h || factor <= 1 {
		tb.Fatalf("source %dx%d -> edge %d gives %dx%d, production %dx%d/rescale=%v, factor=%d; comparison requires a positive downscale and fastjpeg factor > 1",
			cfg.Width, cfg.Height, edge, w, h, sw, sh, needRescale, factor)
	}

	// Every call decodes fresh input: production rescale can mutate the image.
	nativeLanczos := func(factor int) func() (image.Image, error) {
		return func() (image.Image, error) {
			im, err := fastjpeg.DecodeDownsample(bytes.NewReader(data), factor)
			if err != nil {
				return nil, err
			}
			dst := image.NewRGBA(image.Rect(0, 0, w, h))
			jpegCompareLanczos3.Scale(dst, dst.Bounds(), im, im.Bounds(), xdraw.Src, nil)
			return dst, nil
		}
	}
	return jpegComparePlan{
		edge: edge, width: w, height: h, factor: factor,
		paths: []jpegComparePath{
			{
				name: "legacy-production",
				decode: func() (image.Image, error) {
					im, _, err := Decode(bytes.NewReader(data), &DecodeOpts{
						Rotate: 0, Flip: FlipDirection(0), MaxWidth: edge, MaxHeight: edge,
					})
					return im, err
				},
			},
			{
				name: "streaming-core",
				decode: func() (image.Image, error) {
					return resizejpeg.DecodeResize(bytes.NewReader(data), w, h, nil)
				},
			},
			{
				name: "streaming-wrapper",
				decode: func() (image.Image, error) {
					r := bytes.NewReader(data)
					var prefix bytes.Buffer
					cfg, format, err := image.DecodeConfig(io.TeeReader(r, &prefix))
					if err != nil {
						return nil, err
					}
					if format != "jpeg" {
						return nil, fmt.Errorf("streaming wrapper: format %q, want jpeg", format)
					}
					w, h := ScaledDimensions(cfg.Width, cfg.Height, edge, edge)
					return resizejpeg.DecodeResize(io.MultiReader(&prefix, r), w, h, nil)
				},
			},
			{
				name: "thumbnail-core",
				decode: func() (image.Image, error) {
					return resizejpeg.DecodeThumbnail(bytes.NewReader(data), w, h, nil)
				},
			},
			{
				name: "thumbnail-wrapper",
				decode: func() (image.Image, error) {
					r := bytes.NewReader(data)
					var prefix bytes.Buffer
					cfg, format, err := image.DecodeConfig(io.TeeReader(r, &prefix))
					if err != nil {
						return nil, err
					}
					if format != "jpeg" {
						return nil, fmt.Errorf("thumbnail wrapper: format %q, want jpeg", format)
					}
					w, h := ScaledDimensions(cfg.Width, cfg.Height, edge, edge)
					return resizejpeg.DecodeThumbnail(io.MultiReader(&prefix, r), w, h, nil)
				},
			},
			{
				// Includes fastjpeg's own config/replay, but not Decode's
				// outer config/replay and production fallback buffering.
				name: "legacy-parts",
				decode: func() (image.Image, error) {
					im, err := fastjpeg.DecodeDownsample(bytes.NewReader(data), factor)
					if err != nil {
						return nil, err
					}
					return rescale(im, w, h), nil
				},
			},
			{name: "native-scaled-lanczos3", decode: nativeLanczos(factor)},
			{name: "native-full-lanczos3", decode: nativeLanczos(1)},
		},
	}
}

func jpegCompareCounter(tb testing.TB, name string) int64 {
	tb.Helper()
	v, ok := expvar.Get(name).(*expvar.Int)
	if !ok {
		tb.Fatalf("missing fastjpeg expvar.Int %q", name)
	}
	return v.Value()
}

func jpegComparePreflight(tb testing.TB, path jpegComparePath, w, h int) image.Image {
	tb.Helper()
	var successes, failures int64
	if path.name == "legacy-production" {
		successes = jpegCompareCounter(tb, "fastjpeg-djpeg-success")
		failures = jpegCompareCounter(tb, "fastjpeg-djpeg-failure")
	}
	im, err := path.decode()
	if err != nil {
		tb.Fatalf("%s preflight: %v (pure-Go full-buffer fallback is intentionally disabled)", path.name, err)
	}
	if path.name == "legacy-production" {
		gotSuccess := jpegCompareCounter(tb, "fastjpeg-djpeg-success")
		gotFailure := jpegCompareCounter(tb, "fastjpeg-djpeg-failure")
		if gotSuccess != successes+1 || gotFailure != failures {
			tb.Fatalf("legacy production did not use exactly one successful djpeg decode without fallback: success %d->%d, failure %d->%d",
				successes, gotSuccess, failures, gotFailure)
		}
	}
	if im == nil {
		tb.Fatalf("%s preflight: nil image", path.name)
	}
	if im.Bounds() != image.Rect(0, 0, w, h) {
		tb.Fatalf("%s preflight: got bounds %v, want %v", path.name, im.Bounds(), image.Rect(0, 0, w, h))
	}
	if err := jpeg.Encode(io.Discard, im, nil); err != nil {
		tb.Fatalf("%s preflight JPEG encoding: %v", path.name, err)
	}
	return im
}

func BenchmarkJPEGThumbnailComparison(b *testing.B) {
	b.StopTimer()
	jpegCompareRequireDjpeg(b)
	data, source := jpegBytes, "striped"
	if filename := os.Getenv("RESIZEJPEG_COMPARE_JPEG"); filename != "" {
		var err error
		data, err = os.ReadFile(filename)
		if err != nil {
			b.Fatal(err)
		}
		source = strings.TrimSuffix(filepath.Base(filename), filepath.Ext(filename))
	}
	cfg := jpegCompareConfig(b, data)
	var plans []jpegComparePlan
	for _, edge := range []int{128, 256, 512} {
		plans = append(plans, jpegCompareMakePlan(b, data, cfg, edge))
	}
	b.Run(fmt.Sprintf("%s-%dx%d", source, cfg.Width, cfg.Height), func(b *testing.B) {
		for _, plan := range plans {
			b.Run(fmt.Sprintf("edge-%d", plan.edge), func(b *testing.B) {
				b.Logf("target %dx%d; old production fastjpeg factor %d", plan.width, plan.height, plan.factor)
				for _, path := range plan.paths {
					b.Run(path.name, func(b *testing.B) {
						for _, encoded := range []bool{false, true} {
							name := "pixels"
							if encoded {
								name = "jpeg-encoded"
							}
							b.Run(name, func(b *testing.B) {
								b.StopTimer()
								jpegComparePreflight(b, path, plan.width, plan.height)
								b.ReportAllocs()
								b.SetBytes(int64(len(data)))
								b.ResetTimer()
								b.StartTimer()
								for i := 0; i < b.N; i++ {
									im, err := path.decode()
									if err != nil {
										b.Fatal(err)
									}
									if encoded {
										if err := jpeg.Encode(io.Discard, im, nil); err != nil {
											b.Fatal(err)
										}
									}
								}
								b.StopTimer()
								b.ReportMetric(float64(runtime.GOMAXPROCS(0)), "gomaxprocs")
							})
						}
					})
				}
			})
		}
	})
}

func TestJPEGThumbnailComparisonPaths(t *testing.T) {
	jpegCompareRequireDjpeg(t)
	// Odd dimensions exercise ceiling-divided native decoding followed by
	// floored aspect dimensions: edge 32 is 32x24, at factor 8.
	src := image.NewRGBA(image.Rect(0, 0, 257, 193))
	for y := 0; y < 193; y++ {
		for x := 0; x < 257; x++ {
			src.SetRGBA(x, y, color.RGBA{
				R: uint8(x*17 + y*3), G: uint8(x*5 + y*13), B: uint8(x ^ y), A: 255,
			})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, src, nil); err != nil {
		t.Fatal(err)
	}
	data := buf.Bytes()
	cfg := jpegCompareConfig(t, data)
	// Edge 16 also exercises rescale's ResampleInplace + HalveInplace branch;
	// edge 32 exercises its Resize branch.
	for _, edge := range []int{16, 32} {
		t.Run(fmt.Sprintf("edge-%d", edge), func(t *testing.T) {
			plan := jpegCompareMakePlan(t, data, cfg, edge)
			if plan.factor != 8 || plan.width != edge || plan.height != edge*3/4 {
				t.Fatalf("unexpected fixture plan: %dx%d factor %d", plan.width, plan.height, plan.factor)
			}
			if len(plan.paths) != 8 {
				t.Fatalf("comparison has %d paths, want 8", len(plan.paths))
			}
			outputs := make(map[string]image.Image)
			for _, path := range plan.paths {
				outputs[path.name] = jpegComparePreflight(t, path, plan.width, plan.height)
			}
			// These are the only equivalence claims: no equality assertion
			// between native, full-IDCT streaming, and reduced-IDCT thumbnail
			// quality families.
			for _, pair := range [][2]string{
				{"legacy-production", "legacy-parts"},
				{"streaming-core", "streaming-wrapper"},
				{"thumbnail-core", "thumbnail-wrapper"},
			} {
				a, b := outputs[pair[0]], outputs[pair[1]]
				for y := 0; y < plan.height; y++ {
					for x := 0; x < plan.width; x++ {
						ar, ag, ab, aa := a.At(x, y).RGBA()
						br, bg, bb, ba := b.At(x, y).RGBA()
						if ar != br || ag != bg || ab != bb || aa != ba {
							t.Fatalf("%s and %s differ at (%d,%d)", pair[0], pair[1], x, y)
						}
					}
				}
			}
		})
	}
}
