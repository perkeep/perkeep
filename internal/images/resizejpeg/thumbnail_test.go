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
	"errors"
	"fmt"
	"image"
	"image/draw"
	"image/jpeg"
	"io"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"testing/iotest"
	"time"
)

// This deliberately dense, destination-driven oracle does not call the
// production Lanczos constructor, kernel, bounds, spans, or row filter. The
// reduced samples cover the nominal extent original/factor, not the rounded-up
// number of samples. In particular the last sample of an odd-sized JPEG must
// not shift all the output sample centers.
func thumbnailDenseReference(src image.Image, original image.Point, factor, width, height int) *image.RGBA {
	rgba := image.NewRGBA(image.Rect(0, 0, src.Bounds().Dx(), src.Bounds().Dy()))
	draw.Draw(rgba, rgba.Bounds(), src, src.Bounds().Min, draw.Src)
	type weightedSamples struct {
		first  int
		values []float64
	}
	weights := func(samples, original, outputs int) []weightedSamples {
		result := make([]weightedSamples, outputs)
		step := float64(original) / (float64(factor) * float64(outputs))
		scale := math.Max(1, step)
		for out := range result {
			center := (float64(out)+0.5)*step - 0.5
			first := max(0, int(math.Ceil(center-3*scale)))
			last := min(samples, int(math.Floor(center+3*scale))+1)
			row := make([]float64, last-first)
			sum := 0.0
			for in := range row {
				distance := math.Abs(float64(first+in)-center) / scale
				if distance >= 3 {
					continue
				}
				value := 1.0
				if distance != 0 {
					x := math.Pi * distance
					value = (math.Sin(x) / x) * (math.Sin(x/3) / (x / 3))
				}
				row[in] = value
				sum += value
			}
			for in := range row {
				row[in] /= sum
			}
			result[out] = weightedSamples{first, row}
		}
		return result
	}
	xWeights := weights(rgba.Rect.Dx(), original.X, width)
	yWeights := weights(rgba.Rect.Dy(), original.Y, height)
	// Retain the entire horizontal pass, unlike the production row-ring
	// algorithm. Do not clamp or quantize between the two passes.
	horizontal := make([]float64, rgba.Rect.Dy()*width*3)
	for y := 0; y < rgba.Rect.Dy(); y++ {
		for x, weights := range xWeights {
			for offset, weight := range weights.values {
				sx := weights.first + offset
				for c := 0; c < 3; c++ {
					horizontal[(y*width+x)*3+c] += float64(uint32(rgba.Pix[y*rgba.Stride+4*sx+c])*257) * weight / 65535
				}
			}
		}
	}
	dst := image.NewRGBA(image.Rect(0, 0, width, height))
	for y, weights := range yWeights {
		for x := 0; x < width; x++ {
			for c := 0; c < 3; c++ {
				value := 0.0
				for offset, weight := range weights.values {
					sy := weights.first + offset
					value += horizontal[(sy*width+x)*3+c] * weight
				}
				// x/image/draw rounds at 16 bits and takes the high byte.
				value = math.Max(0, math.Min(65535, value*65535+0.5))
				dst.Pix[y*dst.Stride+4*x+c] = uint8(uint16(value) >> 8)
			}
			dst.Pix[y*dst.Stride+4*x+3] = 255
		}
	}
	return dst
}

// Compute expected choices independently, including headroom limited by one
// original dimension. Use uint64 so the test itself cannot overflow on 386.
func thumbnailReferenceFactor(sw, sh, dw, dh int) int {
	for _, factor := range []int{8, 4, 2, 1} {
		w := (uint64(sw) + uint64(factor) - 1) / uint64(factor)
		h := (uint64(sh) + uint64(factor) - 1) / uint64(factor)
		if w >= min(uint64(sw), 4*uint64(dw)) && h >= min(uint64(sh), 4*uint64(dh)) {
			return factor
		}
	}
	return 1
}

func TestChooseThumbnailFactor(t *testing.T) {
	maxInt := int(^uint(0) >> 1)
	for _, tc := range []struct {
		source, target image.Point
		want           int
	}{
		{image.Pt(512, 384), image.Pt(32, 24), 4},
		{image.Pt(1024, 768), image.Pt(32, 24), 8},
		{image.Pt(512, 512), image.Pt(64, 64), 2},
		{image.Pt(256, 256), image.Pt(64, 64), 1},
		{image.Pt(513, 385), image.Pt(32, 24), 4},
		{image.Pt(512, 1), image.Pt(16, 1), 8},
		{image.Pt(1, 512), image.Pt(1, 16), 8},
		{image.Pt(512, 3), image.Pt(16, 1), 1},
		{image.Pt(17, 33), image.Pt(17, 1), 1},
		{image.Pt(65535, 65535), image.Pt(65535, 65535), 1},
		{image.Pt(maxInt, maxInt), image.Pt(1, 1), 8},
		{image.Pt(maxInt, maxInt), image.Pt(maxInt/16, maxInt/16), 4},
		{image.Pt(maxInt, maxInt), image.Pt(maxInt/8, maxInt/8), 2},
		{image.Pt(maxInt, maxInt), image.Pt(maxInt, maxInt), 1},
	} {
		if got := chooseThumbnailFactor(tc.source.X, tc.source.Y, tc.target.X, tc.target.Y); got != tc.want {
			t.Errorf("source %v target %v: factor %d, want %d", tc.source, tc.target, got, tc.want)
		}
	}
	// Exercise thresholds immediately on either side of a reduced sample
	// boundary, with both independent dimensions able to constrain the choice.
	for sw := 1; sw <= 65; sw++ {
		for _, sh := range []int{1, 2, 3, 7, 8, 9, 31, 64, 65} {
			for _, target := range []image.Point{{1, 1}, {min(4, sw), min(3, sh)}, {sw, sh}} {
				want := thumbnailReferenceFactor(sw, sh, target.X, target.Y)
				if got := chooseThumbnailFactor(sw, sh, target.X, target.Y); got != want {
					t.Fatalf("source %dx%d target %v: factor %d, want %d", sw, sh, target, got, want)
				}
			}
		}
	}
}

// The single-scan path must own only one ORIGINAL MCU row of reduced planes
// per worker. Do not reuse the full-IDCT helper: checking only its upper bounds
// would incorrectly permit unreduced RGBA and horizontal buffers here.
func thumbnailAssertBounded(t testing.TB, d *decoder, output *image.RGBA) resizeStreamMemory {
	t.Helper()
	resizeStreamAssertNoSourceBuffers(t, d)
	if d.resize == nil || d.resize.fallback {
		t.Fatal("single-scan thumbnail used full-buffer fallback")
	}
	return thumbnailAssertPipelineScratch(t, d, output)
}

func thumbnailAssertPipelineScratch(t testing.TB, d *decoder, output *image.RGBA) resizeStreamMemory {
	t.Helper()
	resizeStreamAssertJoined(t, d)
	r := d.resize
	if r == nil || !r.streaming || r.pipeline == nil || r.filter == nil {
		t.Fatal("thumbnail did not use the reduced worker pipeline")
	}
	if output != nil && (!r.complete || r.filter.dst != output) {
		t.Fatal("returned thumbnail is not the completed streaming destination")
	}
	bs := 8 / d.factor
	mxx := (d.width + 8*d.maxH - 1) / (8 * d.maxH)
	myy := (d.height + 8*d.maxV - 1) / (8 * d.maxV)
	sw, sh := (d.width+d.factor-1)/d.factor, (d.height+d.factor-1)/d.factor
	bandHeight := bs * d.maxV
	nw := min(r.opts.Workers, myy)
	nb := min(nw+1, myy)
	p := r.pipeline
	if len(p.workers) != nw || len(p.bands) != nb {
		t.Fatalf("workers/bands %d/%d, want %d/%d", len(p.workers), len(p.bands), nw, nb)
	}
	if cap(p.free) != nb || cap(p.jobs) > nw || cap(p.results) > nw {
		t.Fatal("unbounded thumbnail pipeline queue capacities")
	}
	memory := resizeStreamMemory{workers: nw, bands: nb}
	for i, b := range p.bands {
		if want := bandHeight * r.width * 3; len(b.rows) != want || cap(b.rows) != want {
			t.Fatalf("band %d horizontal rows len/cap %d/%d, want reduced %d", i, len(b.rows), cap(b.rows), want)
		}
		memory.horizontal += cap(b.rows) * 8
		for ci, coeffs := range b.coeffs {
			want := 0
			if ci < d.nComp {
				want = mxx * d.comp[ci].h * d.comp[ci].v
			}
			if len(coeffs) != want || cap(coeffs) != want {
				t.Fatalf("band %d component %d coefficient blocks len/cap %d/%d, want one original MCU row %d", i, ci, len(coeffs), cap(coeffs), want)
			}
			memory.coefficients += cap(coeffs) * 64 * 4
		}
	}
	planeLimit := mxx * bs * d.maxH * bandHeight
	for i, w := range p.workers {
		wd := &w.d
		if wd.width != d.width || wd.height != 8*d.maxV || wd.factor != d.factor {
			t.Fatalf("worker %d: original geometry %dx%d factor %d, want %dx%d factor %d", i, wd.width, wd.height, wd.factor, d.width, 8*d.maxV, d.factor)
		}
		if w.rgba == nil || w.rgba.Rect != image.Rect(0, 0, sw, bandHeight) || cap(w.rgba.Pix) != 4*sw*bandHeight {
			t.Fatalf("worker %d RGBA is not exactly one reduced MCU row", i)
		}
		memory.rgba += cap(w.rgba.Pix)
		var planes [][]byte
		if wd.img1 != nil {
			planes = append(planes, wd.img1.Pix)
		}
		if wd.img3 != nil {
			planes = append(planes, wd.img3.Y, wd.img3.Cb, wd.img3.Cr)
		}
		planes = append(planes, wd.blackPix)
		for _, plane := range planes {
			if cap(plane) > planeLimit {
				t.Fatalf("worker %d plane capacity %d exceeds one padded reduced MCU row %d", i, cap(plane), planeLimit)
			}
			memory.planes += cap(plane)
		}
		for ci := range wd.progCoeffs {
			if wd.progCoeffs[ci] != nil || wd.progNonzero[ci] != nil {
				t.Fatalf("worker %d retained image-wide coefficients", i)
			}
		}
	}
	f := r.filter
	if f.sw != sw || f.sh != sh || f.dst.Rect != image.Rect(0, 0, r.width, r.height) || cap(f.dst.Pix) != 4*r.width*r.height {
		t.Fatal("filter sample geometry or target storage is not reduced/exact")
	}
	if len(f.x) != r.width || len(f.y) != r.height || cap(f.sums) > 7*3*r.width {
		t.Fatal("filter retained source-height-proportional spans or floating-point rows")
	}
	if cap(f.xWeights) > 8*(sw+r.width) {
		t.Fatalf("horizontal weight capacity %d is not bounded by reduced source width", cap(f.xWeights))
	}
	memory.filterWeights = cap(f.xWeights) * 8
	return memory
}

// Opt-in progressive/separate scans have NO source-height-independent total
// memory guarantee. Still inspect their actual buffers: pixels must be reduced,
// retained progressive coefficients must be compact, and deferred progressive
// worker scratch must have the same reduced one-MCU-row bounds.
func thumbnailAssertAllowedBuffers(t testing.TB, d *decoder, output *image.RGBA) {
	t.Helper()
	r := d.resize
	if r == nil || !r.fallback || r.filter == nil || r.filter.dst != output {
		t.Fatal("unsupported scan did not use the explicitly permitted fallback")
	}
	bs := 8 / d.factor
	mxx := (d.width + 8*d.maxH - 1) / (8 * d.maxH)
	myy := (d.height + 8*d.maxV - 1) / (8 * d.maxV)
	if r.pipeline != nil {
		if d.img1 != nil || d.img3 != nil || d.blackPix != nil {
			t.Fatal("deferred coefficient pipeline allocated main source pixel planes")
		}
		thumbnailAssertPipelineScratch(t, d, output)
	} else {
		planeLimit := bs * bs * d.maxH * d.maxV * mxx * myy
		var planes [][]byte
		if d.img1 != nil {
			planes = append(planes, d.img1.Pix)
		}
		if d.img3 != nil {
			planes = append(planes, d.img3.Y, d.img3.Cb, d.img3.Cr)
		}
		planes = append(planes, d.blackPix)
		for _, plane := range planes {
			if cap(plane) > planeLimit {
				t.Fatalf("fallback source plane capacity %d exceeds reduced padded image %d", cap(plane), planeLimit)
			}
		}
	}
	sw, sh := (d.width+d.factor-1)/d.factor, (d.height+d.factor-1)/d.factor
	if r.filter.sw != sw || r.filter.sh != sh {
		t.Fatal("fallback filter uses unreduced source geometry")
	}
	for ci := range d.progCoeffs {
		if d.progCoeffs[ci] == nil {
			if d.progNonzero[ci] != nil {
				t.Fatalf("component %d has nonzero flags without retained coefficients", ci)
			}
			continue
		}
		blocks := mxx * myy * d.comp[ci].h * d.comp[ci].v
		want := blocks * bs * bs
		if len(d.progCoeffs[ci]) != want || cap(d.progCoeffs[ci]) != want {
			t.Fatalf("component %d coefficients len/cap %d/%d, want compact %d", ci, len(d.progCoeffs[ci]), cap(d.progCoeffs[ci]), want)
		}
		flags := 0
		if d.factor != 1 {
			flags = blocks
		}
		if len(d.progNonzero[ci]) != flags || cap(d.progNonzero[ci]) != flags {
			t.Fatalf("component %d nonzero flags len/cap %d/%d, want %d", ci, len(d.progNonzero[ci]), cap(d.progNonzero[ci]), flags)
		}
	}
}

func thumbnailReference(t testing.TB, data []byte, source, target image.Point) (*image.RGBA, int) {
	t.Helper()
	factor := thumbnailReferenceFactor(source.X, source.Y, target.X, target.Y)
	src, err := Decode(bytes.NewReader(data), factor)
	if err != nil {
		t.Fatal(err)
	}
	if source.X%factor == 0 && source.Y%factor == 0 {
		return resizeStreamReference(src, target.X, target.Y), factor
	}
	return thumbnailDenseReference(src, source, factor, target.X, target.Y), factor
}

func thumbnailCheckWorkers(t *testing.T, data, oracleData []byte, source, target image.Point, allowFull bool) {
	t.Helper()
	want, factor := thumbnailReference(t, oracleData, source, target)
	var serial *image.RGBA
	for _, workers := range []int{1, 2, 4} {
		t.Run(fmt.Sprintf("workers=%d", workers), func(t *testing.T) {
			got, d, err := decodeThumbnail(bytes.NewReader(data), target.X, target.Y, &ResizeOptions{Workers: workers, AllowFullDecode: allowFull})
			resizeStreamAssertJoined(t, d)
			if err != nil {
				t.Fatal(err)
			}
			if d.factor != factor {
				t.Fatalf("factor %d, want four-times-headroom factor %d", d.factor, factor)
			}
			if !allowFull {
				thumbnailAssertBounded(t, d, got)
			} else {
				thumbnailAssertAllowedBuffers(t, d, got)
			}
			// AllowFullDecode may use reduced source buffers or deferred
			// compact progressive coefficients. It is NOT a bounded-memory
			// promise; only the reduced-IDCT/filter result is required here.
			resizeStreamAssertNear(t, got, want)
			if workers == 1 {
				serial = got
			} else if serial != nil && !samePixels(got, serial) {
				t.Fatal("thumbnail pixels changed with worker count")
			}
		})
	}
}

func thumbnailAssertRejectedBeforeBuffers(t testing.TB, d *decoder) {
	t.Helper()
	resizeStreamAssertNoSourceBuffers(t, d)
	if d.resize == nil || d.resize.filter != nil || d.resize.pipeline != nil {
		t.Fatal("rejected input allocated resize filter or pipeline")
	}
}

func TestDecodeThumbnailCorpus(t *testing.T) {
	paths := corpus(t)
	if len(paths) != 30 {
		t.Fatalf("inherited corpus has %d fixtures, want all 30", len(paths))
	}
	for _, path := range paths {
		t.Run(filepath.Base(path), func(t *testing.T) {
			data := readJPEG(t, path)
			cfg, err := DecodeConfig(bytes.NewReader(data))
			if err != nil {
				t.Fatal(err)
			}
			source := image.Pt(cfg.Width, cfg.Height)
			sof, _, _ := resizeStreamSegment(t, data, 0xc0, 0xc1, 0xc2)
			progressive := sof == 0xc2
			oracleData := data
			if strings.HasSuffix(path, ".progressive.jpeg") {
				// Compare progressive traversal to the baseline JPEG, not
				// just to another traversal of the progressive input.
				oracleData = readJPEG(t, strings.TrimSuffix(path, ".progressive.jpeg")+".jpeg")
			}
			if progressive {
				_, _, sofEnd := resizeStreamSegment(t, data, 0xc2)
				r := &resizeStreamCountingReader{r: iotest.OneByteReader(bytes.NewReader(data))}
				got, d, err := decodeThumbnail(r, min(16, source.X), min(12, source.Y), nil)
				thumbnailAssertRejectedBeforeBuffers(t, d)
				var unsupported UnsupportedError
				if got != nil || !errors.As(err, &unsupported) || r.n > sofEnd {
					t.Fatalf("progressive default: image %v error %v after %d bytes, want UnsupportedError at SOF end %d", got, err, r.n, sofEnd)
				}
			}
			for _, target := range []image.Point{{min(16, source.X), min(12, source.Y)}, {min(128, source.X), min(97, source.Y)}} {
				t.Run(fmt.Sprintf("%dx%d", target.X, target.Y), func(t *testing.T) {
					thumbnailCheckWorkers(t, data, oracleData, source, target, progressive)
				})
			}
		})
	}
}

func TestDecodeThumbnailFactorsAndOddPhase(t *testing.T) {
	for _, tc := range []struct {
		source, target image.Point
		factor         int
	}{
		{image.Pt(512, 384), image.Pt(32, 24), 4},
		{image.Pt(1024, 768), image.Pt(32, 24), 8},
		{image.Pt(512, 512), image.Pt(64, 64), 2},
		{image.Pt(256, 256), image.Pt(64, 64), 1},
		{image.Pt(513, 385), image.Pt(32, 24), 4},
		{image.Pt(1025, 769), image.Pt(16, 12), 8},
		{image.Pt(513, 385), image.Pt(64, 48), 2},
	} {
		t.Run(fmt.Sprintf("%dx%d-to-%dx%d", tc.source.X, tc.source.Y, tc.target.X, tc.target.Y), func(t *testing.T) {
			if got := chooseThumbnailFactor(tc.source.X, tc.source.Y, tc.target.X, tc.target.Y); got != tc.factor {
				t.Fatalf("factor %d, want %d", got, tc.factor)
			}
			data := resizeStreamEncode(t, resizeStreamPattern(tc.source.X, tc.source.Y, false))
			thumbnailCheckWorkers(t, data, data, tc.source, tc.target, false)
		})
	}
	// Make the odd-size oracle's phase requirement observable, rather than
	// merely asserting two very similar references on a constant image.
	source, target := image.Pt(513, 385), image.Pt(32, 24)
	data := resizeStreamEncode(t, resizeStreamPattern(source.X, source.Y, false))
	reduced, err := Decode(bytes.NewReader(data), 4)
	if err != nil {
		t.Fatal(err)
	}
	phaseAware := thumbnailDenseReference(reduced, source, 4, target.X, target.Y)
	roundedExtent := resizeStreamReference(reduced, target.X, target.Y)
	largestDifference := 0
	for i, value := range phaseAware.Pix {
		difference := int(value) - int(roundedExtent.Pix[i])
		largestDifference = max(largestDifference, difference, -difference)
	}
	if largestDifference <= 1 {
		t.Fatal("odd-dimension fixture does not distinguish nominal from rounded-up extent")
	}
}

func TestDecodeThumbnailPreservesDecodeResizeContract(t *testing.T) {
	data := resizeStreamEncode(t, resizeStreamPattern(512, 384, false))
	src, err := jpeg.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	full, d, err := decodeResize(bytes.NewReader(data), 32, 24, &ResizeOptions{Workers: 2})
	if err != nil {
		t.Fatal(err)
	}
	if d.factor != 1 {
		t.Fatalf("DecodeResize adopted thumbnail factor %d; want full IDCT", d.factor)
	}
	resizeStreamAssertBounded(t, d, full)
	resizeStreamAssertNear(t, full, resizeStreamReference(src, 32, 24))
	thumbnail, err := DecodeThumbnail(bytes.NewReader(data), 32, 24, &ResizeOptions{Workers: 2})
	if err != nil {
		t.Fatal(err)
	}
	if samePixels(full, thumbnail) {
		t.Fatal("textured fixture does not distinguish the two quality contracts")
	}
}

func TestDecodeThumbnailTinyGrayAndCMYK(t *testing.T) {
	for _, source := range []image.Point{{1, 1}, {1, 17}, {17, 1}, {3, 5}, {7, 9}, {17, 33}, {257, 129}, {512, 1}, {1, 512}} {
		for _, gray := range []bool{false, true} {
			t.Run(fmt.Sprintf("%dx%d/gray=%v", source.X, source.Y, gray), func(t *testing.T) {
				data := resizeStreamEncode(t, resizeStreamPattern(source.X, source.Y, gray))
				for _, target := range []image.Point{{1, 1}, {min(16, source.X), min(12, source.Y)}, source} {
					t.Run(fmt.Sprintf("%dx%d", target.X, target.Y), func(t *testing.T) {
						thumbnailCheckWorkers(t, data, data, source, target, false)
					})
				}
			})
		}
	}
	for _, transform := range []byte{0, 2} {
		for _, subsampled := range []bool{false, true} {
			t.Run(fmt.Sprintf("CMYK-transform=%d/subsampled=%v", transform, subsampled), func(t *testing.T) {
				data := resizeStreamConstantCMYK(transform, subsampled)
				// 17x19 -> 1x1 selects factor 4, not just the old
				// full-IDCT CMYK pipeline exercised by larger targets.
				thumbnailCheckWorkers(t, data, data, image.Pt(17, 19), image.Pt(1, 1), false)
			})
		}
	}
}

func TestDecodeThumbnailMemorySourceHeight(t *testing.T) {
	measure := func(height, workers int) (resizeStreamMemory, *decoder) {
		t.Helper()
		data := resizeStreamEncode(t, resizeStreamPattern(512, height, false))
		got, d, err := decodeThumbnail(bytes.NewReader(data), 16, 16, &ResizeOptions{Workers: workers})
		if err != nil {
			t.Fatal(err)
		}
		if d.factor != 8 {
			t.Fatalf("factor %d, want 8", d.factor)
		}
		return thumbnailAssertBounded(t, d, got), d
	}
	for _, workers := range []int{1, 2, 4} {
		short, _ := measure(512, workers)
		tall, _ := measure(1024, workers)
		if short != tall {
			t.Fatalf("workers %d: source height increased retained scratch: short %+v, tall %+v", workers, short, tall)
		}
	}
	_, d := measure(512, 0)
	want := min(runtime.GOMAXPROCS(0), 8)
	if d.resize.opts.Workers != want || len(d.resize.pipeline.workers) != want {
		t.Fatalf("default workers configured/active %d/%d, want %d", d.resize.opts.Workers, len(d.resize.pipeline.workers), want)
	}
}

func TestDecodeThumbnailOptionsAndSourceBudget(t *testing.T) {
	for _, workers := range []int{-64, -1, 65, int(^uint(0) >> 1)} {
		r := new(resizeStreamNoRead)
		got, err := DecodeThumbnail(r, 1, 1, &ResizeOptions{Workers: workers, AllowFullDecode: true})
		if got != nil || err == nil || r.reads != 0 {
			t.Fatalf("workers %d: image %v error %v after %d reads", workers, got, err, r.reads)
		}
	}
	for _, target := range []image.Point{{0, 1}, {1, 0}, {-1, 1}, {1, -1}, {0, 0}, {65536, 1}, {1, 65536}, {int(^uint(0) >> 1), 1}} {
		r := new(resizeStreamNoRead)
		got, err := DecodeThumbnail(r, target.X, target.Y, nil)
		if got != nil || err == nil || r.reads != 0 {
			t.Fatalf("target %v: image %v error %v after %d reads", target, got, err, r.reads)
		}
	}
	data := resizeStreamEncode(t, resizeStreamPattern(17, 9, false))
	for _, target := range []image.Point{{18, 9}, {17, 10}, {18, 10}} {
		for _, fallback := range []bool{false, true} {
			got, d, err := decodeThumbnail(bytes.NewReader(data), target.X, target.Y, &ResizeOptions{AllowFullDecode: fallback})
			thumbnailAssertRejectedBeforeBuffers(t, d)
			if got != nil || err == nil {
				t.Fatalf("accepted upsampling target %v: %v", target, err)
			}
		}
	}
	for _, opts := range []*ResizeOptions{nil, {}, {Workers: 1}, {Workers: 64}, {AllowFullDecode: true}} {
		got, err := DecodeThumbnail(bytes.NewReader(data), 5, 3, opts)
		if err != nil {
			t.Fatal(err)
		}
		want, _ := thumbnailReference(t, data, image.Pt(17, 9), image.Pt(5, 3))
		resizeStreamAssertNear(t, got, want)
	}
	for _, path := range []string{"testdata/video-001.q50.420.jpeg", "testdata/video-001.progressive.jpeg"} {
		data := readJPEG(t, path)
		cfg, err := DecodeConfig(bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		pixels := uint64(cfg.Width) * uint64(cfg.Height)
		want, _ := thumbnailReference(t, data, image.Pt(cfg.Width, cfg.Height), image.Pt(16, 12))
		for _, limit := range []uint64{0, pixels, ^uint64(0)} {
			got, err := DecodeThumbnail(bytes.NewReader(data), 16, 12, &ResizeOptions{MaxSourcePixels: limit, AllowFullDecode: true})
			if err != nil {
				t.Fatal(err)
			}
			resizeStreamAssertNear(t, got, want)
		}
		_, sofStart, sofEnd := resizeStreamSegment(t, data, 0xc0, 0xc1, 0xc2)
		for _, allow := range []bool{false, true} {
			r := &resizeStreamCountingReader{r: iotest.OneByteReader(bytes.NewReader(data))}
			got, d, err := decodeThumbnail(r, 16, 12, &ResizeOptions{MaxSourcePixels: pixels - 1, AllowFullDecode: allow})
			thumbnailAssertRejectedBeforeBuffers(t, d)
			if got != nil || err == nil || r.n > sofEnd {
				t.Fatalf("source budget: image %v error %v after %d bytes, SOF ends %d", got, err, r.n, sofEnd)
			}
		}
		huge := bytes.Clone(data)
		for _, offset := range []int{5, 6, 7, 8} {
			huge[sofStart+offset] = 0xff
		}
		r := &resizeStreamCountingReader{r: iotest.OneByteReader(bytes.NewReader(huge))}
		got, d, err := decodeThumbnail(r, 1, 1, &ResizeOptions{MaxSourcePixels: 1_000_000, AllowFullDecode: true})
		thumbnailAssertRejectedBeforeBuffers(t, d)
		if got != nil || err == nil || r.n > sofEnd {
			t.Fatalf("65535x65535 source budget: image %v error %v after %d bytes", got, err, r.n)
		}
	}
}

func TestDecodeThumbnailSeparateScans(t *testing.T) {
	data := resizeStreamSeparateScans()
	_, _, sosEnd := resizeStreamSegment(t, data, 0xda)
	r := &resizeStreamCountingReader{r: iotest.OneByteReader(bytes.NewReader(data))}
	got, d, err := decodeThumbnail(r, 1, 1, nil)
	thumbnailAssertRejectedBeforeBuffers(t, d)
	var unsupported UnsupportedError
	if got != nil || !errors.As(err, &unsupported) || r.n > sosEnd {
		t.Fatalf("non-interleaved default: image %v error %v after %d bytes, SOS ends %d", got, err, r.n, sosEnd)
	}
	thumbnailCheckWorkers(t, data, data, image.Pt(8, 8), image.Pt(1, 1), true)
}

func TestDecodeThumbnailCannotReplayStream(t *testing.T) {
	color := readJPEG(t, "testdata/video-001.q50.420.jpeg")
	_, sosStart, sosEnd := resizeStreamSegment(t, color, 0xda)
	for _, tc := range []struct {
		name        string
		data, after []byte
	}{
		{"late-JFIF", readJPEG(t, "testdata/video-001.rgb.jpeg"), append([]byte{0xff, 0xe0, 0, 16}, []byte("JFIF\x00\x01\x01\x00\x00\x01\x00\x01\x00\x00")...)},
		{"late-Adobe", readJPEG(t, "testdata/video-001.cmyk.jpeg"), append([]byte{0xff, 0xee, 0, 14}, []byte("Adobe\x00\x64\x00\x00\x00\x00\x02")...)},
		{"second-SOS", color, color[sosStart:sosEnd]},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if !bytes.HasSuffix(tc.data, []byte{0xff, 0xd9}) {
				t.Fatal("fixture has no terminal EOI")
			}
			data := append(bytes.Clone(tc.data[:len(tc.data)-2]), tc.after...)
			data = append(data, 0xff, 0xd9)
			for _, allow := range []bool{false, true} {
				got, d, err := decodeThumbnail(bytes.NewReader(data), 16, 12, &ResizeOptions{Workers: 2, AllowFullDecode: allow})
				var unsupported UnsupportedError
				if got != nil || !errors.As(err, &unsupported) {
					t.Fatalf("AllowFullDecode=%v: image %v error %v, want UnsupportedError", allow, got, err)
				}
				thumbnailAssertBounded(t, d, nil)
			}
		})
	}
}

func TestDecodeThumbnailShortReadsAndErrors(t *testing.T) {
	for _, tc := range []struct {
		name string
		data []byte
		full bool
	}{
		{"odd", resizeStreamEncode(t, resizeStreamPattern(513, 385, false)), false},
		{"progressive", readJPEG(t, "testdata/video-001.progressive.jpeg"), true},
		{"separate-scans", resizeStreamSeparateScans(), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opts := &ResizeOptions{Workers: 4, AllowFullDecode: tc.full}
			cfg, err := DecodeConfig(bytes.NewReader(tc.data))
			if err != nil {
				t.Fatal(err)
			}
			target := image.Pt(min(16, cfg.Width), min(12, cfg.Height))
			want, err := DecodeThumbnail(bytes.NewReader(tc.data), target.X, target.Y, opts)
			if err != nil {
				t.Fatal(err)
			}
			for _, reader := range []io.Reader{iotest.OneByteReader(bytes.NewReader(tc.data)), iotest.HalfReader(bytes.NewReader(tc.data)), iotest.DataErrReader(bytes.NewReader(tc.data))} {
				got, d, err := decodeThumbnail(reader, target.X, target.Y, opts)
				resizeStreamAssertJoined(t, d)
				if err != nil || !samePixels(got, want) {
					t.Fatalf("short read changed result: %v", err)
				}
			}
			_, _, sosEnd := resizeStreamSegment(t, tc.data, 0xda)
			for _, cut := range []int{0, 1, 24, sosEnd, len(tc.data) / 2, len(tc.data) - 2} {
				for _, workers := range []int{1, 2, 4} {
					got, d, err := decodeThumbnail(bytes.NewReader(tc.data[:cut]), target.X, target.Y, &ResizeOptions{Workers: workers, AllowFullDecode: tc.full})
					resizeStreamAssertJoined(t, d)
					if got != nil || err == nil {
						t.Fatalf("cut %d workers %d accepted: %v", cut, workers, err)
					}
				}
			}
		})
	}
	for _, malformed := range [][]byte{[]byte("not a JPEG"), {0xff, 0xd8, 0xff, 0xdb, 0, 1}} {
		if got, err := DecodeThumbnail(bytes.NewReader(malformed), 1, 1, nil); got != nil || err == nil {
			t.Fatalf("accepted malformed input: %v", err)
		}
	}
	data := readJPEG(t, "testdata/video-001.q50.420.jpeg")
	_, start, _ := resizeStreamSegment(t, data, 0xc0)
	for _, offset := range []int{5, 7} {
		bad := bytes.Clone(data)
		bad[start+offset], bad[start+offset+1] = 0, 0
		if got, err := DecodeThumbnail(bytes.NewReader(bad), 1, 1, nil); got != nil || err == nil {
			t.Fatalf("accepted zero source dimension: %v", err)
		}
	}
	restart := bytes.Clone(readJPEG(t, "testdata/video-001.restart2.jpeg"))
	at := bytes.Index(restart, []byte{0xff, 0xd1})
	if at < 0 {
		t.Fatal("restart fixture has no RST1")
	}
	restart[at+1] = 0xd2
	got, d, err := decodeThumbnail(bytes.NewReader(restart), 16, 12, &ResizeOptions{Workers: 4})
	resizeStreamAssertJoined(t, d)
	if got != nil || err == nil {
		t.Fatalf("accepted wrong restart marker: %v", err)
	}
	readErr := errors.New("injected thumbnail reader failure")
	for _, reader := range []io.Reader{iotest.ErrReader(readErr), io.MultiReader(bytes.NewReader(data[:len(data)/2]), iotest.ErrReader(readErr))} {
		got, d, err := decodeThumbnail(reader, 16, 12, &ResizeOptions{Workers: 4})
		resizeStreamAssertJoined(t, d)
		if got != nil || !errors.Is(err, readErr) {
			t.Fatalf("reader failure: image %v error %v", got, err)
		}
	}
}

func TestDecodeThumbnailErrorCleanup(t *testing.T) {
	data := readJPEG(t, "testdata/video-001.q50.420.jpeg")
	done := make(chan error, 1)
	go func() {
		for i := 0; i < 24; i++ {
			opts := &ResizeOptions{Workers: []int{1, 2, 4}[i%3]}
			got, d, err := decodeThumbnail(bytes.NewReader(data[:len(data)/2]), 16, 12, opts)
			if got != nil || err == nil || d.resize.pipeline == nil || !d.resize.pipeline.joined {
				done <- fmt.Errorf("iteration %d: truncated decode failed cleanup: %v", i, err)
				return
			}
			got, d, err = decodeThumbnail(bytes.NewReader(data), 16, 12, opts)
			if got == nil || err != nil || d.resize.pipeline == nil || !d.resize.pipeline.joined {
				done <- fmt.Errorf("iteration %d: subsequent valid decode failed cleanup: %v", i, err)
				return
			}
		}
		done <- nil
	}()
	timer := time.NewTimer(20 * time.Second)
	defer timer.Stop()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-timer.C:
		t.Fatal("thumbnail error/success sequence blocked; possible unjoined worker")
	}
}

func FuzzDecodeThumbnail(f *testing.F) {
	for _, path := range corpus(f) {
		f.Add(readJPEG(f, path))
	}
	f.Add(resizeStreamSeparateScans())
	f.Add(resizeStreamConstantCMYK(2, true))
	f.Add(resizeStreamEncode(f, resizeStreamPattern(513, 385, false)))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 1<<20 {
			return
		}
		cfg, err := DecodeConfig(bytes.NewReader(data))
		if err != nil || cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width > 1024 || cfg.Height > 1024 || uint64(cfg.Width)*uint64(cfg.Height) > 1_000_000 {
			return
		}
		width, height := min(7, cfg.Width), min(5, cfg.Height)
		for _, allow := range []bool{false, true} {
			got, d, err := decodeThumbnail(bytes.NewReader(data), width, height, &ResizeOptions{Workers: 1 + len(data)%2, AllowFullDecode: allow, MaxSourcePixels: 1_000_000})
			resizeStreamAssertJoined(t, d)
			if d != nil && d.resize != nil && d.resize.streaming && !d.resize.fallback {
				thumbnailAssertBounded(t, d, got)
			}
			if err != nil {
				if got != nil {
					t.Fatal("error returned partial thumbnail")
				}
				continue
			}
			if got == nil || got.Bounds() != image.Rect(0, 0, width, height) {
				t.Fatal("successful thumbnail has incorrect bounds")
			}
			for y := 0; y < height; y++ {
				for x := 0; x < width; x++ {
					if got.RGBAAt(x, y).A != 255 {
						t.Fatal("successful thumbnail is not opaque")
					}
				}
			}
		}
	})
}

var thumbnailBenchmarkResult *image.RGBA

func thumbnailBenchmark(b *testing.B, data []byte) {
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		b.Fatal(err)
	}
	for _, edge := range []int{256, 128, 64} {
		target := image.Pt(edge, max(1, int(math.Round(float64(cfg.Height)*float64(edge)/float64(cfg.Width)))))
		if cfg.Height > cfg.Width {
			target = image.Pt(max(1, int(math.Round(float64(cfg.Width)*float64(edge)/float64(cfg.Height)))), edge)
		}
		b.Run(fmt.Sprintf("%dx%d", target.X, target.Y), func(b *testing.B) {
			if target.X > cfg.Width || target.Y > cfg.Height {
				b.Skip("input is smaller than target; no upsampling")
			}
			factor := thumbnailReferenceFactor(cfg.Width, cfg.Height, target.X, target.Y)
			for _, workers := range []int{1, 2, 4} {
				for _, thumbnail := range []bool{true, false} {
					name := "thumbnail"
					if !thumbnail {
						name = "full-DecodeResize"
					}
					b.Run(fmt.Sprintf("%s/workers=%d", name, workers), func(b *testing.B) {
						b.ReportAllocs()
						b.SetBytes(int64(len(data)))
						opts := &ResizeOptions{Workers: workers, AllowFullDecode: true}
						b.ResetTimer()
						for i := 0; i < b.N; i++ {
							var got *image.RGBA
							var err error
							if thumbnail {
								got, err = DecodeThumbnail(bytes.NewReader(data), target.X, target.Y, opts)
							} else {
								got, err = DecodeResize(bytes.NewReader(data), target.X, target.Y, opts)
							}
							if err != nil {
								b.Fatal(err)
							}
							thumbnailBenchmarkResult = got
						}
						b.StopTimer()
						b.ReportMetric(float64(runtime.GOMAXPROCS(0)), "gomaxprocs")
						if thumbnail {
							b.ReportMetric(float64(factor), "factor")
						} else {
							b.ReportMetric(1, "factor")
						}
					})
				}
			}
			b.Run(fmt.Sprintf("serial-reduced-factor%d+full-Lanczos3", factor), func(b *testing.B) {
				b.ReportAllocs()
				b.SetBytes(int64(len(data)))
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					src, err := Decode(bytes.NewReader(data), factor)
					if err != nil {
						b.Fatal(err)
					}
					if cfg.Width%factor == 0 && cfg.Height%factor == 0 {
						thumbnailBenchmarkResult = resizeStreamReference(src, target.X, target.Y)
					} else {
						thumbnailBenchmarkResult = thumbnailDenseReference(src, image.Pt(cfg.Width, cfg.Height), factor, target.X, target.Y)
					}
				}
				b.StopTimer()
				b.ReportMetric(float64(runtime.GOMAXPROCS(0)), "gomaxprocs")
				b.ReportMetric(float64(factor), "factor")
			})
		})
	}
}

// Generate/encode outside measured leaf loops. Worker counts are software
// concurrency, not core counts; use fixed GOMAXPROCS benchmark processes.
func BenchmarkDecodeThumbnail(b *testing.B) {
	data := resizeStreamEncode(b, resizeStreamBenchmarkPhoto())
	b.Run("generated-4000x3000-420", func(b *testing.B) {
		thumbnailBenchmark(b, data)
	})
}

func BenchmarkDecodeThumbnailRealJPEG(b *testing.B) {
	path := os.Getenv("RESIZEJPEG_BENCH_JPEG")
	if path == "" {
		b.Skip("set RESIZEJPEG_BENCH_JPEG to an existing JPEG photograph")
	}
	data := readJPEG(b, path)
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		b.Fatal(err)
	}
	b.Run(fmt.Sprintf("source-%dx%d", cfg.Width, cfg.Height), func(b *testing.B) {
		thumbnailBenchmark(b, data)
	})
}

var _ func(io.Reader, int, int, *ResizeOptions) (*image.RGBA, error) = DecodeThumbnail
