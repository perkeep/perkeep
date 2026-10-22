//go:build jpegquality

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
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"image/png"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	xdraw "golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"

	"perkeep.org/internal/images/fastjpeg"
)

// The manual report is opt-in and writes outside the source tree. The ordinary
// unit/fuzz tests do not download photos, call external tools, or generate HTML.
//
//go:embed quality.html
var qualityHTML []byte

// A three-lobe, scale-aware Lanczos reference using x/image/draw's separable
// kernel engine. It widens the filter on downsampling rather than sampling a
// fixed six source pixels regardless of scale.
var qualityLanczos3 = &xdraw.Kernel{
	Support: 3,
	At: func(x float64) float64 {
		if x == 0 {
			return 1
		}
		x *= math.Pi
		return math.Sin(x) * math.Sin(x/3) / (x * x / 3)
	},
}

type qualityPhoto struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	Source  string `json:"source"`
	Creator string `json:"creator"`
	License string `json:"license"`
	SHA256  string `json:"sha256,omitempty"`
}

type qualitySource struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Source         string `json:"source"`
	Creator        string `json:"creator"`
	License        string `json:"license"`
	OriginalWidth  int    `json:"original_width"`
	OriginalHeight int    `json:"original_height"`
	Width          int    `json:"width"`
	Height         int    `json:"height"`
	JPEG           string `json:"jpeg"`
	SHA256         string `json:"sha256"`
}

type qualityStats struct {
	MAE      float64  `json:"mae"`
	RMSE     float64  `json:"rmse"`
	PSNR     *float64 `json:"psnr"` // nil means identical, i.e. +infinity.
	MaxError int      `json:"max_error"`
}

type qualityVariant struct {
	Name    string       `json:"name"`
	Image   string       `json:"image"`
	Diff    string       `json:"diff"`
	Metrics qualityStats `json:"metrics"`
}

type qualityRow struct {
	ID        string           `json:"id"`
	SourceID  string           `json:"source_id"`
	Label     string           `json:"label"`
	Width     int              `json:"width"`
	Height    int              `json:"height"`
	Reference string           `json:"reference"`
	Variants  []qualityVariant `json:"variants"`
}

type qualityReport struct {
	Sources           []qualitySource `json:"sources"`
	Rows              []qualityRow    `json:"rows"`
	ProgressiveChecks int             `json:"progressive_checks"`
	Notes             []string        `json:"notes"`
}

type qualityCandidate struct {
	name string
	im   *image.RGBA
}

func qualityRGBA(src image.Image) *image.RGBA {
	dst := image.NewRGBA(image.Rect(0, 0, src.Bounds().Dx(), src.Bounds().Dy()))
	draw.Draw(dst, dst.Bounds(), src, src.Bounds().Min, draw.Src)
	return dst
}

func qualityResize(src image.Image, w, h int) *image.RGBA {
	return qualityResizeFilter(src, w, h, Lanczos3)
}

// Use x/image's independent separable kernel implementation for every filter,
// rather than the production resizer, when checking full-IDCT output pixels.
func qualityResizeFilter(src image.Image, w, h int, filter Filter) *image.RGBA {
	var kernel xdraw.Interpolator
	switch filter {
	case Lanczos3:
		kernel = qualityLanczos3
	case CatmullRom:
		kernel = xdraw.CatmullRom
	case Bilinear:
		kernel = xdraw.BiLinear
	default:
		panic("unknown quality reference filter")
	}
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	kernel.Scale(dst, dst.Bounds(), src, src.Bounds(), draw.Src, nil)
	return dst
}

func qualityFilterName(filter Filter) string {
	switch filter {
	case Lanczos3:
		return "Lanczos3"
	case CatmullRom:
		return "CatmullRom"
	case Bilinear:
		return "Bilinear"
	default:
		panic("unknown quality filter")
	}
}

func qualityThumbnailCandidate(t *testing.T, data []byte, sw, sh, w, h int, filter Filter, headroom int) qualityCandidate {
	t.Helper()
	thumbnail, d, err := decodeThumbnail(bytes.NewReader(data), w, h, &ResizeOptions{
		AllowFullDecode: true, MaxSourcePixels: 25_000_000,
		Filter: filter, ThumbnailHeadroom: headroom,
	})
	if err != nil {
		t.Fatal(err)
	}
	if headroom == 0 {
		headroom = 4
	}
	name := fmt.Sprintf("Pure Go %dx-headroom IDCT 1/%d + %s", headroom, d.factor, qualityFilterName(filter))
	if sw < headroom*w || sh < headroom*h {
		name += " (source-limited headroom)"
	}
	if d.progressive {
		name += " (progressive coefficient buffering)"
	} else if d.resize.fallback {
		name += " (buffered scan fallback)"
	}
	return qualityCandidate{name, thumbnail}
}

func qualityTarget(w, h, longEdge int) (int, int) {
	if w >= h {
		return longEdge, max(1, int(math.Round(float64(h)*float64(longEdge)/float64(w))))
	}
	return max(1, int(math.Round(float64(w)*float64(longEdge)/float64(h)))), longEdge
}

func qualityFactorIndex(images [4]*image.RGBA, w, h, headroom int) (index int, met bool) {
	for j, im := range images {
		if im.Rect.Dx() >= w*headroom && im.Rect.Dy() >= h*headroom {
			index, met = j, true
		}
	}
	// When even the full source cannot meet the goal, use full resolution and
	// report that the source limits headroom rather than claiming it was met.
	return index, met
}

func TestQualityFactorSelection(t *testing.T) {
	for _, tc := range []struct {
		sw, sh, w, h, headroom, index int
		met                           bool
	}{
		{2048, 1536, 256, 192, 1, 3, true},
		{2048, 1536, 256, 192, 2, 2, true},
		{1023, 768, 512, 384, 1, 1, true},
		{1023, 768, 512, 384, 2, 0, false},
		{1024, 1, 512, 1, 1, 1, true},
		{1024, 1, 512, 1, 2, 0, false},
		{1, 1, 512, 512, 1, 0, false},
	} {
		var images [4]*image.RGBA
		for j := range images {
			factor := 1 << j
			images[j] = &image.RGBA{Rect: image.Rect(0, 0, (tc.sw+factor-1)/factor, (tc.sh+factor-1)/factor)}
		}
		if index, met := qualityFactorIndex(images, tc.w, tc.h, tc.headroom); index != tc.index || met != tc.met {
			t.Fatalf("%+v: index=%d met=%v", tc, index, met)
		}
	}
}

func qualityMeasure(ref, candidate *image.RGBA) (qualityStats, *image.RGBA, error) {
	if ref.Bounds() != candidate.Bounds() || ref.Rect.Empty() {
		return qualityStats{}, nil, fmt.Errorf("incompatible comparison bounds")
	}
	diff := image.NewRGBA(ref.Bounds())
	var sum, squares uint64
	maxError := 0
	for y := 0; y < ref.Rect.Dy(); y++ {
		for x := 0; x < ref.Rect.Dx(); x++ {
			a, b, d := y*ref.Stride+4*x, y*candidate.Stride+4*x, y*diff.Stride+4*x
			for c := 0; c < 3; c++ {
				delta := int(ref.Pix[a+c]) - int(candidate.Pix[b+c])
				if delta < 0 {
					delta = -delta
				}
				sum += uint64(delta)
				squares += uint64(delta * delta)
				maxError = max(maxError, delta)
				diff.Pix[d+c] = byte(min(255, delta*8))
			}
			diff.Pix[d+3] = 255
		}
	}
	n := float64(ref.Rect.Dx() * ref.Rect.Dy() * 3)
	stats := qualityStats{MAE: float64(sum) / n, RMSE: math.Sqrt(float64(squares) / n), MaxError: maxError}
	if stats.RMSE != 0 {
		db := 20 * math.Log10(255/stats.RMSE)
		stats.PSNR = &db
	}
	return stats, diff, nil
}

func TestQualityMetrics(t *testing.T) {
	a := image.NewRGBA(image.Rect(0, 0, 2, 3))
	b := image.NewRGBA(a.Rect)
	draw.Draw(a, a.Rect, image.Black, image.Point{}, draw.Src)
	draw.Draw(b, b.Rect, image.White, image.Point{}, draw.Src)
	stats, _, err := qualityMeasure(a, a)
	if err != nil || stats.MAE != 0 || stats.RMSE != 0 || stats.PSNR != nil || stats.MaxError != 0 {
		t.Fatalf("identity: %+v, %v", stats, err)
	}
	stats, _, err = qualityMeasure(a, b)
	if err != nil || stats.MAE != 255 || stats.RMSE != 255 || stats.PSNR == nil || *stats.PSNR != 0 || stats.MaxError != 255 {
		t.Fatalf("black vs white: %+v, %v", stats, err)
	}
}

func TestQualityLanczosConstant(t *testing.T) {
	for _, tc := range []struct{ x, want float64 }{
		{0, 1}, {0.5, 6 / (math.Pi * math.Pi)}, {1, 0},
		{1.5, -4 / (3 * math.Pi * math.Pi)}, {2, 0},
	} {
		if got := qualityLanczos3.At(tc.x); math.Abs(got-tc.want) > 1e-14 {
			t.Fatalf("Lanczos3(%g) = %g, want %g", tc.x, got, tc.want)
		}
	}
	for _, level := range []byte{0, 17, 127, 255} {
		src := image.NewRGBA(image.Rect(0, 0, 64, 63))
		c := color.RGBA{level, level, level, 255}
		draw.Draw(src, src.Rect, image.NewUniform(c), image.Point{}, draw.Src)
		for _, size := range []image.Point{{1, 1}, {17, 9}, {128, 17}} {
			dst := qualityResize(src, size.X, size.Y)
			for y := 0; y < size.Y; y++ {
				for x := 0; x < size.X; x++ {
					if got := dst.RGBAAt(x, y); got != c {
						t.Fatalf("level %d size %v: got %v", level, size, got)
					}
				}
			}
		}
	}
}

func TestQualityLanczosAntialias(t *testing.T) {
	// One white pixel per eight source pixels. A fixed-width interpolator
	// aliases this pattern badly; a filter widened for a 32x reduction should
	// preserve its mean intensity away from the image boundaries.
	src := image.NewGray(image.Rect(0, 0, 512, 1))
	for x := 0; x < 512; x += 8 {
		src.Pix[x] = 255
	}
	dst := qualityResize(src, 16, 1)
	for x := 3; x < 13; x++ {
		if got := dst.RGBAAt(x, 0); got.R < 31 || got.R > 33 || got.R != got.G || got.R != got.B || got.A != 255 {
			t.Fatalf("downsampled mean at %d = %v, want approximately 32", x, got)
		}
	}
}

func qualityCreateOutput(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	// Reserve a fresh directory, never overwrite assets beneath an already
	// published report (even if a later comparison or write fails).
	return os.Mkdir(path, 0755)
}

func TestQualityFreshOutput(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "report")
	if err := qualityCreateOutput(dir); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "report.json")
	qualityWrite(t, path, []byte("previous report"))
	if err := qualityCreateOutput(dir); !os.IsExist(err) {
		t.Fatalf("reusing output: got %v, want existing-directory error", err)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != "previous report" {
		t.Fatalf("existing report changed: %q, %v", got, err)
	}
}

func TestQualityReport(t *testing.T) {
	out := os.Getenv("RESIZEJPEG_QUALITY_DIR")
	if out == "" {
		t.Skip("set RESIZEJPEG_QUALITY_DIR to generate the manual visual report")
	}
	if err := qualityCreateOutput(out); err != nil {
		t.Fatalf("output must be a fresh directory; choose an unused path: %v", err)
	}
	report := qualityReport{Notes: []string{
		fmt.Sprintf("Generator environment: %s, %s/%s. Dependency versions are pinned in the repository go.mod.", runtime.Version(), runtime.GOOS, runtime.GOARCH),
		"Reference: Go image/jpeg full decode, conversion to opaque 8-bit RGB, then scale-aware Lanczos3 in sRGB-coded values (not linear light).",
		"Photographs use the original source JPEG bytes without resizing or re-encoding. Generated patterns are 2048x1536 baseline JPEGs at quality 95, plus a 2049x1537 text/edges control. Every comparison starts from the same JPEG, not an uncompressed original.",
		"Fixed-factor candidates in direct rows compare reduced IDCT alone against full decode + Lanczos3 at identical output dimensions.",
		"Thumbnail rows compare the largest usable decode factor against a more conservative factor aiming for at least 2x target dimensions, followed by Lanczos3. Source-limited headroom is labeled; duplicate factors are omitted.",
		"Thumbnail rows also include a legacy full-decode + Lanczos3 control, unless that duplicates an existing candidate.",
		"All downsampling rows also test DecodeResize: full IDCT, clipped 8-bit RGB, and streaming Lanczos3, with automatic worker count. Progressive/non-interleaved inputs explicitly allow full-buffer fallback, labeled when used.",
		"Every row also tests pure-Go DecodeThumbnail: select the largest power-of-two IDCT factor (1, 2, 4, or 8) whose rounded-up dimensions retain min(source, 4x target) in both axes, then apply scale-aware Lanczos3. Power-of-two selection commonly leaves 4–8x headroom; the factor-8 cap can leave more. Source-limited headroom is labeled. This does less reconstruction/filtering work when a reduced factor is selected, but is not pixel-equivalent to full decode + Lanczos3.",
		"Thumbnail rows append four options comparisons after the original candidates: 2x-headroom Lanczos3 isolates the headroom change; 4x-headroom CatmullRom and Bilinear isolate the filter change; 2x-headroom Bilinear combines both. The unchanged default is 4x-headroom Lanczos3. Each label gives the actual IDCT factor: 2x headroom only quarters intermediate pixels relative to 4x when both axes can select twice the reduction factor; source limits and the factor-8 cap can prevent this.",
		"CatmullRom and Bilinear use scale-widened filters too, not fixed-width interpolation. Their errors here are measured against full-decode Lanczos3, so a different filter can differ even with IDCT factor 1. These images show quality trade-offs; use the separate options benchmark to measure CPU/allocation trade-offs.",
		"DecodeThumbnail uses automatic workers and phase-aware final filtering: its sample grid is based on original dimensions divided by the IDCT factor, not the rounded-up reduced dimensions, to avoid an additional odd-edge phase shift. Progressive/non-interleaved inputs explicitly permit buffering; progressive coefficient buffering is labeled separately from a buffered scan fallback.",
		"Progressive parity checks include both APIs with default Lanczos3, 2x-headroom Bilinear, and 4x-headroom CatmullRom. Each full-IDCT result is also checked against the corresponding independently implemented x/image filter reference (maximum one channel level); thumbnail results are not required to match full decoding.",
		"RGB MAE/RMSE/PSNR measure agreement with this reference, not absolute or perceptual quality. The difference images are absolute RGB differences amplified 8x.",
		"Outputs are lossless PNGs. Natural-size and enlarged inspection should be used; a browser-scaled fit view can hide differences.",
		"Fixed-factor reduced candidates sample on a factor-spaced grid. For odd source dimensions, their ordinary global resize from the rounded-up reduced dimensions uses a slightly different grid. Aligned and odd text/edge cases help expose that distinction; the phase-aware DecodeThumbnail candidate accounts for the original grid.",
	}}
	var photos []qualityPhoto
	if path := os.Getenv("RESIZEJPEG_QUALITY_PHOTOS"); path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(data, &photos); err != nil {
			t.Fatal(err)
		}
		// Resolve relative photo paths against the manifest's directory.
		for i := range photos {
			if !filepath.IsAbs(photos[i].Path) {
				photos[i].Path = filepath.Join(filepath.Dir(path), photos[i].Path)
			}
		}
	}
	if len(photos) == 0 {
		report.Notes = append(report.Notes, "No photo manifest supplied: this report contains synthetic inputs only.")
	}
	type input struct {
		source qualitySource
		image  image.Image
		data   []byte
	}
	var inputs []input
	for i, photo := range photos {
		data, err := os.ReadFile(photo.Path)
		if err != nil {
			t.Fatal(err)
		}
		cfg, err := jpeg.DecodeConfig(bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Width <= 0 || cfg.Height <= 0 || uint64(cfg.Width)*uint64(cfg.Height) > 25_000_000 {
			t.Fatalf("photo dimensions exceed the manual report's 25-megapixel limit")
		}
		hash := fmt.Sprintf("%x", sha256.Sum256(data))
		if photo.SHA256 != "" && hash != photo.SHA256 {
			t.Fatalf("photo %q SHA-256 changed: got %s", photo.Name, hash)
		}
		src := qualitySource{
			ID: fmt.Sprintf("photo-%d", i+1), Name: photo.Name,
			Source: photo.Source, Creator: photo.Creator, License: photo.License,
			OriginalWidth: cfg.Width, OriginalHeight: cfg.Height,
		}
		inputs = append(inputs, input{source: src, data: data})
	}
	for i, name := range []string{"Text, diagonals, and sharp edges", "Chirps and fine repeating patterns", "Saturated color edges", "Text/edges, odd dimensions"} {
		w, h, kind := 2048, 1536, i
		if i == 3 {
			w, h, kind = 2049, 1537, 0
		}
		src := qualitySource{
			ID: fmt.Sprintf("synthetic-%d", i+1), Name: name,
			Source: "Generated by quality_test.go", Creator: "Perkeep",
			License: "Apache-2.0", OriginalWidth: w, OriginalHeight: h,
		}
		inputs = append(inputs, input{source: src, image: qualityPattern(t, kind, w, h)})
	}
	jpegtran, err := exec.LookPath("jpegtran")
	if err != nil {
		report.Notes = append(report.Notes, "jpegtran unavailable: extra progressive conversion/parity checks were NOT run.")
	}
	useLegacy := fastjpeg.Available()
	if useLegacy {
		version, err := exec.Command("djpeg", "-version").CombinedOutput()
		if err != nil {
			t.Fatal(err)
		}
		report.Notes = append(report.Notes, "Legacy comparison: existing fastjpeg.DecodeDownsample with "+strings.TrimSpace(string(version))+". Its IDCT and chroma upsampling need not match the new decoder.")
	} else {
		report.Notes = append(report.Notes, "djpeg unavailable or disabled: legacy fastjpeg comparisons were NOT run.")
	}
	for _, in := range inputs {
		t.Run(in.source.ID, func(t *testing.T) {
			source := in.source
			data := in.data
			if data == nil {
				var encoded bytes.Buffer
				if err := jpeg.Encode(&encoded, in.image, &jpeg.Options{Quality: 95}); err != nil {
					t.Fatal(err)
				}
				data = encoded.Bytes()
			}
			full, err := jpeg.Decode(bytes.NewReader(data))
			if err != nil {
				t.Fatal(err)
			}
			source.Width, source.Height = full.Bounds().Dx(), full.Bounds().Dy()
			source.JPEG = source.ID + "-source.jpg"
			source.SHA256 = fmt.Sprintf("%x", sha256.Sum256(data))
			qualityWrite(t, filepath.Join(out, source.JPEG), data)
			report.Sources = append(report.Sources, source)
			if jpegtran != "" {
				report.ProgressiveChecks += qualityCheckProgressive(t, jpegtran, data)
			}
			fullRGB := qualityRGBA(full)
			var reduced [4]*image.RGBA
			var legacy [4]*image.RGBA
			for j, factor := range []int{1, 2, 4, 8} {
				im, err := Decode(bytes.NewReader(data), factor)
				if err != nil {
					t.Fatal(err)
				}
				reduced[j] = qualityRGBA(im)
				if factor == 1 && !bytes.Equal(fullRGB.Pix, reduced[j].Pix) {
					t.Fatal("full-resolution decode differs from standard library")
				}
				if useLegacy {
					im, err := fastjpeg.DecodeDownsample(bytes.NewReader(data), factor)
					if err != nil {
						t.Fatal(err)
					}
					legacy[j] = qualityRGBA(im)
				}
			}
			addRow := func(id, label string, w, h int, variants []qualityCandidate) {
				row := qualityRow{
					ID: source.ID + "-" + id, SourceID: source.ID, Label: label,
					Width: w, Height: h, Reference: source.ID + "-" + id + "-reference.png",
				}
				reference := qualityResize(fullRGB, w, h)
				qualityPNG(t, filepath.Join(out, row.Reference), reference)
				streamingIndex := len(variants)
				if w <= source.Width && h <= source.Height {
					streamed, d, err := decodeResize(bytes.NewReader(data), w, h, &ResizeOptions{
						AllowFullDecode: true, MaxSourcePixels: 25_000_000,
					})
					if err != nil {
						t.Fatal(err)
					}
					name := "Streaming full IDCT + Lanczos3"
					if !d.resize.streaming {
						name = "Full IDCT + Lanczos3 (full-buffer fallback)"
					}
					variants = append(variants, qualityCandidate{name, streamed})
					variants = append(variants, qualityThumbnailCandidate(t, data, source.Width, source.Height, w, h, Lanczos3, 0))
					// Append only to thumbnail rows. This preserves every existing
					// candidate index/hash and avoids large source-limited direct
					// rows duplicating the same factor-1 headroom comparison.
					if strings.HasPrefix(id, "thumbnail") {
						for _, option := range []struct {
							filter   Filter
							headroom int
						}{
							{Lanczos3, 2},
							{CatmullRom, 4},
							{Bilinear, 4},
							{Bilinear, 2},
						} {
							variants = append(variants, qualityThumbnailCandidate(t, data, source.Width, source.Height, w, h, option.filter, option.headroom))
						}
					}
				}
				for j, variant := range variants {
					stats, diff, err := qualityMeasure(reference, variant.im)
					if err != nil {
						t.Fatal(err)
					}
					if j == streamingIndex && stats.MaxError > 1 {
						t.Fatalf("%s differs from full-decode reference by more than one channel level: %+v", variant.name, stats)
					}
					base := fmt.Sprintf("%s-variant%d", row.ID, j+1)
					v := qualityVariant{Name: variant.name, Image: base + ".png", Diff: base + "-diff.png", Metrics: stats}
					qualityPNG(t, filepath.Join(out, v.Image), variant.im)
					qualityPNG(t, filepath.Join(out, v.Diff), diff)
					row.Variants = append(row.Variants, v)
					t.Logf("%s %s: MAE %.3f, RMSE %.3f, max %d", label, v.Name, stats.MAE, stats.RMSE, stats.MaxError)
				}
				report.Rows = append(report.Rows, row)
			}
			for j, factor := range []int{2, 4, 8} {
				im := reduced[j+1]
				variants := []qualityCandidate{{fmt.Sprintf("Reduced IDCT (factor %d)", factor), im}}
				if useLegacy {
					variants = append(variants, qualityCandidate{fmt.Sprintf("Legacy djpeg (factor %d)", factor), legacy[j+1]})
				}
				addRow(fmt.Sprintf("direct%d", factor), fmt.Sprintf("Direct 1/%d", factor), im.Rect.Dx(), im.Rect.Dy(),
					variants)
			}
			for _, target := range []int{512, 256, 128} {
				w, h := qualityTarget(source.Width, source.Height, target)
				var variants []qualityCandidate
				last := -1
				for _, headroom := range []int{1, 2} {
					index, met := qualityFactorIndex(reduced, w, h, headroom)
					if index == last {
						continue
					}
					last = index
					name := fmt.Sprintf("Aggressive factor %d + Lanczos3", 1<<index)
					if headroom == 2 {
						name = fmt.Sprintf("2x-headroom factor %d + Lanczos3", 1<<index)
						if !met {
							name = "Full decode + Lanczos3 (source-limited headroom)"
						}
					}
					variants = append(variants, qualityCandidate{name, qualityResize(reduced[index], w, h)})
					if useLegacy {
						name := fmt.Sprintf("Legacy djpeg factor %d + Lanczos3 (aggressive)", 1<<index)
						if headroom == 2 {
							name = fmt.Sprintf("Legacy djpeg factor %d + Lanczos3 (2x-headroom)", 1<<index)
							if !met {
								name = "Legacy djpeg full decode + Lanczos3 (source-limited headroom)"
							}
						}
						variants = append(variants, qualityCandidate{name, qualityResize(legacy[index], w, h)})
					}
				}
				if useLegacy && last != 0 {
					variants = append(variants, qualityCandidate{
						"Legacy djpeg full decode + Lanczos3 (control)", qualityResize(legacy[0], w, h),
					})
				}
				addRow(fmt.Sprintf("thumbnail%d", target), fmt.Sprintf("Thumbnail %dpx", target), w, h, variants)
			}
		})
	}
	if t.Failed() {
		return // Do not publish a partial report as a completed experiment.
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	qualityWrite(t, filepath.Join(out, "report.json"), data)
	qualityWrite(t, filepath.Join(out, "index.html"), qualityHTML)
	t.Logf("visual report: %s (%d sources, %d rows, %d progressive checks)", out, len(report.Sources), len(report.Rows), report.ProgressiveChecks)
}

func qualityCheckProgressive(t *testing.T, jpegtran string, data []byte) int {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, jpegtran, "-progressive")
	cmd.Stdin = bytes.NewReader(data)
	prog, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	for _, factor := range []int{1, 2, 4, 8} {
		a, err := Decode(bytes.NewReader(data), factor)
		if err != nil {
			t.Fatal(err)
		}
		b, err := Decode(bytes.NewReader(prog), factor)
		if err != nil {
			t.Fatal(err)
		}
		if !samePixels(a, b) {
			t.Fatalf("factor %d: original/progressive pixels differ", factor)
		}
	}
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	w, h := qualityTarget(cfg.Width, cfg.Height, min(256, max(cfg.Width, cfg.Height)))
	full, err := jpeg.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	fullRGB := qualityRGBA(full)
	checks := 4
	for _, option := range []struct {
		filter   Filter
		headroom int
	}{
		{Lanczos3, 0},
		{Bilinear, 2},
		{CatmullRom, 4},
	} {
		opts := &ResizeOptions{
			AllowFullDecode: true, MaxSourcePixels: 25_000_000,
			Filter: option.filter, ThumbnailHeadroom: option.headroom,
		}
		name := qualityFilterName(option.filter)
		a, err := DecodeResize(bytes.NewReader(data), w, h, opts)
		if err != nil {
			t.Fatal(err)
		}
		b, err := DecodeResize(bytes.NewReader(prog), w, h, opts)
		if err != nil {
			t.Fatal(err)
		}
		if !samePixels(a, b) {
			t.Fatalf("full IDCT + %s: original/progressive pixels differ", name)
		}
		reference := qualityResizeFilter(fullRGB, w, h, option.filter)
		stats, _, err := qualityMeasure(reference, a)
		if err != nil || stats.MaxError > 1 {
			t.Fatalf("full IDCT + %s differs from independent reference: %+v, %v", name, stats, err)
		}
		checks++
		a, err = DecodeThumbnail(bytes.NewReader(data), w, h, opts)
		if err != nil {
			t.Fatal(err)
		}
		b, err = DecodeThumbnail(bytes.NewReader(prog), w, h, opts)
		if err != nil {
			t.Fatal(err)
		}
		if !samePixels(a, b) {
			headroom := option.headroom
			if headroom == 0 {
				headroom = 4
			}
			t.Fatalf("%dx-headroom IDCT + %s: original/progressive pixels differ", headroom, name)
		}
		checks++
	}
	return checks
}

func qualityWrite(t testing.TB, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
}

func qualityPNG(t testing.TB, path string, im image.Image) {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, im); err != nil {
		t.Fatal(err)
	}
	qualityWrite(t, path, buf.Bytes())
}

func qualityPattern(t *testing.T, kind, w, h int) image.Image {
	t.Helper()
	if kind == 1 {
		im := image.NewGray(image.Rect(0, 0, w, h))
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				var v float64
				switch {
				case y < h/3:
					// Horizontal chirp, period gradually approaching 2 pixels.
					v = math.Sin(math.Pi * float64(x*x) / float64(2*w))
				case y < 2*h/3:
					period := []int{2, 3, 5, 7, 9, 13, 17, 31}[min(7, 8*x/w)]
					v = -1
					if (x/period+y/period)%2 == 0 {
						v = 1
					}
				default:
					dx, dy := float64(x-w/2), float64(y-5*h/6)
					v = math.Sin(math.Pi * (dx*dx + dy*dy) / float64(w))
				}
				im.Pix[y*im.Stride+x] = byte(math.Round(127.5 + 127.5*v))
			}
		}
		return im
	}
	im := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(im, im.Rect, image.White, image.Point{}, draw.Src)
	if kind == 2 {
		palette := []color.RGBA{{255, 0, 0, 255}, {0, 255, 0, 255}, {0, 0, 255, 255}, {255, 255, 0, 255}, {0, 255, 255, 255}, {255, 0, 255, 255}}
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				var c color.RGBA
				if y < h/2 {
					c = palette[min(len(palette)-1, x*len(palette)/w)]
				} else {
					c = palette[((x+y)/max(1, (y-h/2)/32+1))%len(palette)]
				}
				im.SetRGBA(x, y, c)
			}
		}
		return im
	}
	ft, err := opentype.Parse(goregular.TTF)
	if err != nil {
		t.Fatal(err)
	}
	y := 20
	for _, size := range []float64{18, 24, 36, 48, 72, 108, 160} {
		face, err := opentype.NewFace(ft, &opentype.FaceOptions{Size: size, DPI: 72})
		if err != nil {
			t.Fatal(err)
		}
		y += int(size) + 12
		d := font.Drawer{Dst: im, Src: image.Black, Face: face, Dot: fixed.P(24, y)}
		d.DrawString("JPEG 0123456789 — fine text & sharp edges")
		if err := face.Close(); err != nil {
			t.Fatal(err)
		}
	}
	for yy := 850; yy < h; yy++ {
		for x := 0; x < w; x++ {
			black := false
			switch {
			case x < w/3:
				black = x+(yy-850)/2 > w/4
			case x < 2*w/3:
				black = (x+yy)/9%2 == 0
			default:
				black = (x-w*2/3)%max(2, (yy-850)/16+2) == 0
			}
			if black {
				im.SetRGBA(x, yy, color.RGBA{A: 255})
			}
		}
	}
	return im
}
