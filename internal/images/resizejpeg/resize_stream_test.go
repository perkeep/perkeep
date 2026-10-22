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
	"image/color"
	"image/draw"
	"image/jpeg"
	"io"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"testing/iotest"
	"time"

	xdraw "golang.org/x/image/draw"
)

// Keep the integration oracle independent of the production filter. In
// particular, convert the full decode to opaque 8-bit RGBA before filtering:
// filtering YCbCr or 16-bit color directly is a different reference.
var resizeStreamReferenceKernel = &xdraw.Kernel{
	Support: 3,
	At: func(x float64) float64 {
		if x == 0 {
			return 1
		}
		x *= math.Pi
		return math.Sin(x) * math.Sin(x/3) / (x * x / 3)
	},
}

func resizeStreamReference(src image.Image, width, height int) *image.RGBA {
	rgba := image.NewRGBA(image.Rect(0, 0, src.Bounds().Dx(), src.Bounds().Dy()))
	draw.Draw(rgba, rgba.Bounds(), src, src.Bounds().Min, draw.Src)
	dst := image.NewRGBA(image.Rect(0, 0, width, height))
	resizeStreamReferenceKernel.Scale(dst, dst.Bounds(), rgba, rgba.Bounds(), draw.Src, nil)
	return dst
}

func resizeStreamAssertNear(t testing.TB, got, want *image.RGBA) {
	t.Helper()
	if got == nil || got.Bounds() != want.Bounds() {
		t.Fatalf("got %v, want bounds %v", got, want.Bounds())
	}
	for y := 0; y < want.Bounds().Dy(); y++ {
		for x := 0; x < want.Bounds().Dx(); x++ {
			g, w := got.RGBAAt(x, y), want.RGBAAt(x, y)
			if g.A != 255 || w.A != 255 {
				t.Fatalf("pixel (%d,%d): alpha got %d, reference %d; want opaque", x, y, g.A, w.A)
			}
			for channel, pair := range [][2]uint8{{g.R, w.R}, {g.G, w.G}, {g.B, w.B}} {
				if d := int(pair[0]) - int(pair[1]); d < -1 || d > 1 {
					t.Fatalf("pixel (%d,%d) channel %d: got %v, reference %v (tolerance 1)", x, y, channel, g, w)
				}
			}
		}
	}
}

func resizeStreamAssertNoSourceBuffers(t testing.TB, d *decoder) {
	t.Helper()
	if d == nil {
		t.Fatal("missing decoder for buffer inspection")
	}
	if d.img1 != nil || d.img3 != nil || d.blackPix != nil {
		t.Fatal("main decoder allocated source pixel planes")
	}
	for ci := range d.progCoeffs {
		if d.progCoeffs[ci] != nil || d.progNonzero[ci] != nil {
			t.Fatalf("main decoder retained image-wide component %d coefficients", ci)
		}
	}
}

func resizeStreamAssertJoined(t testing.TB, d *decoder) {
	t.Helper()
	if d == nil || d.resize == nil || d.resize.pipeline == nil {
		return // Failure before a pipeline was created, or explicit fallback.
	}
	p := d.resize.pipeline
	if !p.joined {
		t.Fatal("decoder returned before workers and collector joined")
	}
	select {
	case <-p.done:
	default:
		t.Fatal("collector is still running after decoder return")
	}
}

type resizeStreamMemory struct {
	workers, bands              int
	coefficients, horizontal    int
	planes, rgba, filterWeights int
}

// Inspect actual retained capacities, not merely visible image bounds or
// cumulative allocation statistics. A worker owns at most one source MCU row;
// the serial entropy decoder owns no source image planes at all.
func resizeStreamAssertBounded(t testing.TB, d *decoder, output *image.RGBA) resizeStreamMemory {
	t.Helper()
	resizeStreamAssertNoSourceBuffers(t, d)
	resizeStreamAssertJoined(t, d)
	r := d.resize
	if r == nil || !r.streaming || r.fallback || r.pipeline == nil || r.filter == nil {
		t.Fatal("single-scan input did not use the bounded streaming pipeline")
	}
	if output != nil && (!r.complete || r.filter.dst != output) {
		t.Fatal("returned image is not the completed streaming destination")
	}
	mxx := (d.width + 8*d.maxH - 1) / (8 * d.maxH)
	myy := (d.height + 8*d.maxV - 1) / (8 * d.maxV)
	bandHeight := 8 * d.maxV
	nw := min(r.opts.Workers, myy)
	nb := min(nw+1, myy)
	p := r.pipeline
	if len(p.workers) != nw || len(p.bands) != nb {
		t.Fatalf("workers/bands %d/%d, want %d/%d", len(p.workers), len(p.bands), nw, nb)
	}
	if cap(p.free) != nb || cap(p.jobs) > nw || cap(p.results) > nw {
		t.Fatalf("unbounded queue capacities: free=%d jobs=%d results=%d", cap(p.free), cap(p.jobs), cap(p.results))
	}
	memory := resizeStreamMemory{workers: nw, bands: nb}
	for i, b := range p.bands {
		if want := bandHeight * r.width * 3; len(b.rows) != want || cap(b.rows) != want {
			t.Fatalf("band %d: horizontal rows len/cap %d/%d, want %d", i, len(b.rows), cap(b.rows), want)
		}
		memory.horizontal += cap(b.rows) * 8
		for ci, coeffs := range b.coeffs {
			want := 0
			if ci < d.nComp {
				want = mxx * d.comp[ci].h * d.comp[ci].v
			}
			if len(coeffs) != want || cap(coeffs) != want {
				t.Fatalf("band %d component %d: coefficient blocks len/cap %d/%d, want %d",
					i, ci, len(coeffs), cap(coeffs), want)
			}
			memory.coefficients += cap(coeffs) * 64 * 4
		}
	}
	planeLimit := mxx * 8 * d.maxH * bandHeight
	for i, w := range p.workers {
		wd := &w.d
		if wd.width != d.width || wd.height != bandHeight || wd.factor != 1 {
			t.Fatalf("worker %d owns a decoder larger than one full-IDCT MCU row", i)
		}
		if w.rgba == nil || w.rgba.Bounds() != image.Rect(0, 0, d.width, bandHeight) ||
			cap(w.rgba.Pix) != 4*d.width*bandHeight {
			t.Fatalf("worker %d RGBA buffer is not one MCU row", i)
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
				t.Fatalf("worker %d plane capacity %d exceeds one padded MCU row %d", i, cap(plane), planeLimit)
			}
			memory.planes += cap(plane)
		}
		for ci := range wd.progCoeffs {
			if wd.progCoeffs[ci] != nil || wd.progNonzero[ci] != nil {
				t.Fatalf("worker %d retained progressive image-wide coefficients", i)
			}
		}
	}
	f := r.filter
	if f.dst.Bounds() != image.Rect(0, 0, r.width, r.height) || cap(f.dst.Pix) != 4*r.width*r.height {
		t.Fatal("filter destination is not exactly target-sized")
	}
	if len(f.x) != r.width || len(f.y) != r.height || cap(f.sums) > 7*3*r.width {
		t.Fatal("filter retained source-height-proportional spans or RGB rows")
	}
	if cap(f.xWeights) > 7*(d.width+r.width) {
		t.Fatalf("horizontal weight capacity %d is not bounded by source width", cap(f.xWeights))
	}
	memory.filterWeights = cap(f.xWeights) * 8
	return memory
}

// Return header segment offsets without depending on production parsing or on
// filenames: video-001.separate.dc.progression.jpeg is itself progressive.
// The returned interval includes the marker and length bytes.
func resizeStreamSegment(t testing.TB, data []byte, wanted ...byte) (marker byte, start, end int) {
	t.Helper()
	if len(data) < 2 || data[0] != 0xff || data[1] != 0xd8 {
		t.Fatal("test JPEG has no SOI")
	}
	for pos := 2; pos+4 <= len(data); {
		if data[pos] != 0xff {
			t.Fatalf("invalid test JPEG header at %d", pos)
		}
		start = pos
		for pos < len(data) && data[pos] == 0xff {
			pos++
		}
		if pos+3 > len(data) {
			break
		}
		marker = data[pos]
		n := int(data[pos+1])<<8 | int(data[pos+2])
		end = pos + 1 + n
		if n < 2 || end > len(data) {
			t.Fatalf("invalid test JPEG segment %#x at %d", marker, start)
		}
		for _, w := range wanted {
			if marker == w {
				return marker, start, end
			}
		}
		if marker == 0xda {
			break
		}
		pos = end
	}
	t.Fatalf("test JPEG has no header marker %x", wanted)
	return 0, 0, 0
}

func resizeStreamTargets(width, height int) []image.Point {
	var targets []image.Point
	add := func(p image.Point) {
		for _, old := range targets {
			if old == p {
				return
			}
		}
		targets = append(targets, p)
	}
	for _, factor := range []int{1, 2, 4, 8} {
		add(image.Pt((width+factor-1)/factor, (height+factor-1)/factor))
	}
	// Deliberately do not preserve aspect ratio: dimensions are explicit.
	add(image.Pt(min(128, width), min(97, height)))
	return targets
}

func resizeStreamCheckWorkers(t *testing.T, data []byte, src image.Image, target image.Point, fallback bool) {
	t.Helper()
	want := resizeStreamReference(src, target.X, target.Y)
	var serial *image.RGBA
	for _, workers := range []int{1, 2, 4} {
		t.Run(fmt.Sprintf("workers=%d", workers), func(t *testing.T) {
			got, d, err := decodeResize(bytes.NewReader(data), target.X, target.Y, &ResizeOptions{
				Workers: workers, AllowFullDecode: fallback,
			})
			resizeStreamAssertJoined(t, d)
			if err != nil {
				t.Fatal(err)
			}
			if fallback {
				if !d.resize.fallback || d.resize.streaming || d.resize.pipeline != nil {
					t.Fatal("unsupported scan did not use the explicitly permitted fallback")
				}
			} else {
				resizeStreamAssertBounded(t, d, got)
			}
			resizeStreamAssertNear(t, got, want)
			if workers == 1 {
				serial = got
			} else if !samePixels(got, serial) {
				t.Fatal("worker count changed output pixels")
			}
		})
		if serial == nil {
			return
		}
	}
}

func TestDecodeResizeCorpus(t *testing.T) {
	for _, path := range corpus(t) {
		t.Run(filepath.Base(path), func(t *testing.T) {
			data := readJPEG(t, path)
			src, err := jpeg.Decode(bytes.NewReader(data))
			if err != nil {
				t.Fatal(err)
			}
			sof, _, _ := resizeStreamSegment(t, data, 0xc0, 0xc1, 0xc2)
			progressive := sof == 0xc2
			if progressive {
				_, _, sosEnd := resizeStreamSegment(t, data, 0xda)
				r := &resizeStreamCountingReader{r: iotest.OneByteReader(bytes.NewReader(data))}
				got, d, err := decodeResize(r, 128, 97, nil)
				resizeStreamAssertNoSourceBuffers(t, d)
				if d.resize.filter != nil || d.resize.pipeline != nil {
					t.Fatal("progressive default allocated a resize image or pipeline")
				}
				var unsupported UnsupportedError
				if got != nil || !errors.As(err, &unsupported) {
					t.Fatalf("progressive default: got %T, error %v; want nil, UnsupportedError", got, err)
				}
				if r.n > sosEnd {
					t.Fatalf("progressive rejection consumed entropy: read %d bytes, SOS ends at %d", r.n, sosEnd)
				}
			}
			for _, target := range resizeStreamTargets(src.Bounds().Dx(), src.Bounds().Dy()) {
				t.Run(fmt.Sprintf("%dx%d", target.X, target.Y), func(t *testing.T) {
					// Every single-scan format, including CMYK, RGB, flex
					// subsampling, grayscale, and restart intervals, must use
					// the default bounded path, not opt in to full decoding.
					resizeStreamCheckWorkers(t, data, src, target, progressive)
				})
			}
			target := image.Pt(min(128, src.Bounds().Dx()), min(97, src.Bounds().Dy()))
			want := resizeStreamReference(src, target.X, target.Y)
			for _, opts := range []*ResizeOptions{nil, {}, {AllowFullDecode: true}} {
				if progressive && (opts == nil || !opts.AllowFullDecode) {
					continue
				}
				got, err := DecodeResize(bytes.NewReader(data), target.X, target.Y, opts)
				if err != nil {
					t.Fatalf("default/zero options: %v", err)
				}
				resizeStreamAssertNear(t, got, want)
			}
		})
	}
}

func resizeStreamEncode(t testing.TB, src image.Image) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, src, &jpeg.Options{Quality: 95}); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func resizeStreamPattern(width, height int, gray bool) image.Image {
	if gray {
		src := image.NewGray(image.Rect(0, 0, width, height))
		for y := 0; y < height; y++ {
			for x := 0; x < width; x++ {
				src.SetGray(x, y, color.Gray{Y: byte(x*43 + y*77 + x*y*3)})
			}
		}
		return src
	}
	src := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			src.SetRGBA(x, y, color.RGBA{
				R: byte(x*43 + y*13), G: byte(x*5 + y*71), B: byte(x*y*7 + x*19), A: 255,
			})
		}
	}
	return src
}

func TestDecodeResizeSmallAndOdd(t *testing.T) {
	for _, size := range []image.Point{{1, 1}, {1, 17}, {17, 1}, {3, 5}, {7, 9}, {8, 8}, {17, 33}, {257, 129}} {
		for _, gray := range []bool{false, true} {
			t.Run(fmt.Sprintf("%dx%d/gray=%v", size.X, size.Y, gray), func(t *testing.T) {
				data := resizeStreamEncode(t, resizeStreamPattern(size.X, size.Y, gray))
				src, err := jpeg.Decode(bytes.NewReader(data))
				if err != nil {
					t.Fatal(err)
				}
				for _, target := range resizeStreamTargets(size.X, size.Y) {
					t.Run(fmt.Sprintf("%dx%d", target.X, target.Y), func(t *testing.T) {
						resizeStreamCheckWorkers(t, data, src, target, false)
					})
				}
			})
		}
	}
}

func TestDecodeResizeDenseColor420(t *testing.T) {
	// Preserve the quality experiment's saturated fine-diagonal stress case:
	// full IDCT, clipping, source chroma expansion, and Lanczos must occur in
	// the reference order. Reduced IDCT plus resizing is not interchangeable.
	const width, height = 2049, 1537
	palette := [...]color.RGBA{
		{255, 0, 0, 255}, {0, 255, 0, 255}, {0, 0, 255, 255},
		{255, 255, 0, 255}, {0, 255, 255, 255}, {255, 0, 255, 255},
	}
	pattern := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			index := min(len(palette)-1, x*len(palette)/width)
			if y >= height/2 {
				index = ((x + y) / max(1, (y-height/2)/32+1)) % len(palette)
			}
			pattern.SetRGBA(x, y, palette[index])
		}
	}
	data := resizeStreamEncode(t, pattern)
	src, err := jpeg.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if ycbcr, ok := src.(*image.YCbCr); !ok || ycbcr.SubsampleRatio != image.YCbCrSubsampleRatio420 {
		t.Fatalf("stress input is %T, want baseline 4:2:0", src)
	}
	for _, target := range []image.Point{{256, 192}, {128, 96}, {257, 193}} {
		t.Run(fmt.Sprintf("%dx%d", target.X, target.Y), func(t *testing.T) {
			resizeStreamCheckWorkers(t, data, src, target, false)
		})
	}
}

func TestDecodeResizeMemorySourceShape(t *testing.T) {
	measure := func(width, height, workers int) (resizeStreamMemory, *decoder) {
		t.Helper()
		data := resizeStreamEncode(t, resizeStreamPattern(width, height, false))
		got, d, err := decodeResize(bytes.NewReader(data), 31, 17, &ResizeOptions{Workers: workers})
		if err != nil {
			t.Fatal(err)
		}
		return resizeStreamAssertBounded(t, d, got), d
	}
	// Both heights have at least three MCU rows, so neither the worker
	// count nor the fixed-size band pool is reduced for a short source.
	short, _ := measure(129, 129, 2)
	tall, _ := measure(129, 1025, 2)
	if short != tall {
		t.Fatalf("source height increased retained scratch: short %+v, tall %+v", short, tall)
	}
	wide, _ := measure(513, 129, 2)
	// Coefficients and worker planes scale with padded MCU columns; worker
	// RGBA scales with visible source width. Horizontal rows scale with
	// target width only. No term may grow as source width*source height.
	narrowCols, wideCols := (129+15)/16, (513+15)/16
	if wide.coefficients*narrowCols != short.coefficients*wideCols ||
		wide.planes*narrowCols != short.planes*wideCols ||
		wide.rgba*129 != short.rgba*513 ||
		wide.horizontal != short.horizontal ||
		wide.workers != short.workers || wide.bands != short.bands {
		t.Fatalf("unexpected source-width scaling: narrow %+v, wide %+v", short, wide)
	}
	_, d := measure(129, 257, 0)
	if want := min(runtime.GOMAXPROCS(0), 8); d.resize.opts.Workers != want || len(d.resize.pipeline.workers) != want {
		t.Fatalf("automatic workers: configured %d, active %d; want min(GOMAXPROCS,8)=%d",
			d.resize.opts.Workers, len(d.resize.pipeline.workers), want)
	}
}

type resizeStreamCountingReader struct {
	r io.Reader
	n int
}

func (r *resizeStreamCountingReader) Read(p []byte) (int, error) {
	n, err := r.r.Read(p)
	r.n += n
	return n, err
}

type resizeStreamNoRead struct{ reads int }

func (r *resizeStreamNoRead) Read([]byte) (int, error) {
	r.reads++
	return 0, errors.New("invalid options must not read")
}

func TestDecodeResizeInvalidOptionsBeforeRead(t *testing.T) {
	for _, workers := range []int{-64, -1, 65, int(^uint(0) >> 1)} {
		t.Run(fmt.Sprintf("workers=%d", workers), func(t *testing.T) {
			r := new(resizeStreamNoRead)
			got, err := DecodeResize(r, 1, 1, &ResizeOptions{Workers: workers, AllowFullDecode: true})
			if got != nil || err == nil || r.reads != 0 {
				t.Fatalf("got %v, %v after %d reads; want nil, error before Read", got, err, r.reads)
			}
		})
	}
	for _, target := range []image.Point{{0, 1}, {1, 0}, {-1, 1}, {1, -1}, {0, 0}} {
		t.Run(fmt.Sprintf("size=%dx%d", target.X, target.Y), func(t *testing.T) {
			r := new(resizeStreamNoRead)
			got, err := DecodeResize(r, target.X, target.Y, nil)
			if got != nil || err == nil || r.reads != 0 {
				t.Fatalf("got %v, %v after %d reads; want nil, error before Read", got, err, r.reads)
			}
		})
	}
}

func TestDecodeResizeNoUpsampling(t *testing.T) {
	data := resizeStreamEncode(t, resizeStreamPattern(17, 9, false))
	for _, target := range []image.Point{{18, 9}, {17, 10}, {18, 10}, {int(^uint(0) >> 1), 1}} {
		for _, fallback := range []bool{false, true} {
			got, err := DecodeResize(bytes.NewReader(data), target.X, target.Y, &ResizeOptions{AllowFullDecode: fallback})
			if got != nil || err == nil {
				t.Fatalf("target %v, fallback %v: got %v, %v; want nil, error", target, fallback, got, err)
			}
		}
	}
	// Both public endpoints of the worker range are valid, even if the image
	// contains too little work to use all of the requested workers.
	for _, workers := range []int{0, 1, 64} {
		if _, err := DecodeResize(bytes.NewReader(data), 17, 9, &ResizeOptions{Workers: workers}); err != nil {
			t.Fatalf("valid workers=%d: %v", workers, err)
		}
	}
}

func TestDecodeResizeSourcePixelLimit(t *testing.T) {
	for _, path := range []string{"testdata/video-001.q50.420.jpeg", "testdata/video-001.progressive.jpeg"} {
		t.Run(filepath.Base(path), func(t *testing.T) {
			data := readJPEG(t, path)
			src, err := jpeg.Decode(bytes.NewReader(data))
			if err != nil {
				t.Fatal(err)
			}
			pixels := uint64(src.Bounds().Dx()) * uint64(src.Bounds().Dy())
			want := resizeStreamReference(src, 37, 29)
			for _, limit := range []uint64{0, pixels, ^uint64(0)} {
				got, err := DecodeResize(bytes.NewReader(data), 37, 29, &ResizeOptions{
					MaxSourcePixels: limit, AllowFullDecode: true,
				})
				if err != nil {
					t.Fatalf("limit %d: %v", limit, err)
				}
				resizeStreamAssertNear(t, got, want)
			}
			_, _, sofEnd := resizeStreamSegment(t, data, 0xc0, 0xc1, 0xc2)
			for _, fallback := range []bool{false, true} {
				// A one-byte reader prevents decoder read-ahead from
				// obscuring the requirement to reject at the SOF.
				r := &resizeStreamCountingReader{r: iotest.OneByteReader(bytes.NewReader(data))}
				got, d, err := decodeResize(r, 37, 29, &ResizeOptions{
					MaxSourcePixels: pixels - 1, AllowFullDecode: fallback,
				})
				resizeStreamAssertNoSourceBuffers(t, d)
				if d.resize.filter != nil || d.resize.pipeline != nil {
					t.Fatal("source pixel limit was checked after allocating resize buffers")
				}
				if got != nil || err == nil || r.n > sofEnd {
					t.Fatalf("limit %d, fallback %v: got %v, %v after %d bytes; SOF ends at %d",
						pixels-1, fallback, got, err, r.n, sofEnd)
				}
			}
			// Exercise the product at the JPEG dimension limit without ever
			// allocating source-sized image or coefficient storage.
			huge := bytes.Clone(data)
			_, sofStart, _ := resizeStreamSegment(t, huge, 0xc0, 0xc1, 0xc2)
			for _, offset := range []int{5, 6, 7, 8} {
				huge[sofStart+offset] = 0xff
			}
			r := &resizeStreamCountingReader{r: iotest.OneByteReader(bytes.NewReader(huge))}
			got, d, err := decodeResize(r, 1, 1, &ResizeOptions{MaxSourcePixels: 1 << 20, AllowFullDecode: true})
			resizeStreamAssertNoSourceBuffers(t, d)
			if d.resize.filter != nil || d.resize.pipeline != nil {
				t.Fatal("huge source limit was checked after allocating resize buffers")
			}
			if got != nil || err == nil || r.n > sofEnd {
				t.Fatalf("65535x65535 limit: got %v, %v after %d bytes; SOF ends at %d", got, err, r.n, sofEnd)
			}
		})
	}
}

// A valid, constant 8x8 sequential JPEG with one scan per component. The two
// one-bit Huffman codes encode DC category zero and AC EOB. Unlike changing
// an existing SOS header, this remains valid through all three scans and
// therefore also exercises the explicit full-decode fallback.
func resizeStreamSeparateScans() []byte {
	data := []byte{0xff, 0xd8}
	segment := func(marker byte, payload []byte) {
		n := len(payload) + 2
		data = append(data, 0xff, marker, byte(n>>8), byte(n))
		data = append(data, payload...)
	}
	quant := make([]byte, 65)
	for i := 1; i < len(quant); i++ {
		quant[i] = 1
	}
	segment(0xdb, quant)
	segment(0xc0, []byte{8, 0, 8, 0, 8, 3, 1, 0x11, 0, 2, 0x11, 0, 3, 0x11, 0})
	var huffman []byte
	for _, selector := range []byte{0x00, 0x10} {
		huffman = append(huffman, selector, 1)
		huffman = append(huffman, make([]byte, 15)...)
		huffman = append(huffman, 0)
	}
	segment(0xc4, huffman)
	for component := byte(1); component <= 3; component++ {
		segment(0xda, []byte{1, component, 0, 0, 63, 0})
		data = append(data, 0x3f) // DC=0, EOB, six padding bits.
	}
	return append(data, 0xff, 0xd9)
}

func TestDecodeResizeSeparateScans(t *testing.T) {
	data := resizeStreamSeparateScans()
	src, err := jpeg.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("generated non-interleaved reference: %v", err)
	}
	_, _, sosEnd := resizeStreamSegment(t, data, 0xda)
	r := &resizeStreamCountingReader{r: iotest.OneByteReader(bytes.NewReader(data))}
	got, d, err := decodeResize(r, 5, 3, nil)
	resizeStreamAssertNoSourceBuffers(t, d)
	if d.resize.filter != nil || d.resize.pipeline != nil {
		t.Fatal("partial first scan allocated a resize image or pipeline")
	}
	var unsupported UnsupportedError
	if got != nil || !errors.As(err, &unsupported) {
		t.Fatalf("default non-interleaved decode: got %v, %v; want nil, UnsupportedError", got, err)
	}
	if r.n > sosEnd {
		t.Fatalf("non-interleaved rejection consumed entropy: read %d bytes, SOS ends at %d", r.n, sosEnd)
	}
	resizeStreamCheckWorkers(t, data, src, image.Pt(5, 3), true)
}

func resizeStreamConstantCMYK(transform byte, subsampled bool) []byte {
	data := []byte{0xff, 0xd8}
	segment := func(marker byte, payload []byte) {
		n := len(payload) + 2
		data = append(data, 0xff, marker, byte(n>>8), byte(n))
		data = append(data, payload...)
	}
	segment(0xee, append([]byte("Adobe\x00\x64\x00\x00\x00\x00"), transform))
	quant := make([]byte, 65)
	for i := 1; i < len(quant); i++ {
		quant[i] = 1
	}
	segment(0xdb, quant)
	factors := [4]byte{0x11, 0x11, 0x11, 0x11}
	maxH, maxV, blocksPerMCU := 1, 1, 4
	if subsampled {
		factors[0], factors[3] = 0x22, 0x22
		maxH, maxV, blocksPerMCU = 2, 2, 10
	}
	sof := []byte{8, 0, 19, 0, 17, 4}
	sos := []byte{4}
	for i, id := range []byte{'C', 'M', 'Y', 'K'} {
		sof = append(sof, id, factors[i], 0)
		sos = append(sos, id, 0)
	}
	segment(0xc0, sof)
	var huffman []byte
	for _, selector := range []byte{0x00, 0x10} {
		huffman = append(huffman, selector, 1)
		huffman = append(huffman, make([]byte, 15)...)
		huffman = append(huffman, 0)
	}
	segment(0xc4, huffman)
	segment(0xda, append(sos, 0, 63, 0))
	// Each block is DC=0, EOB (00). Only final partial-byte padding uses
	// one bits; no external encoder or checked-in binary fixture is needed.
	blocks := ((17 + 8*maxH - 1) / (8 * maxH)) * ((19 + 8*maxV - 1) / (8 * maxV)) * blocksPerMCU
	bits := 2 * blocks
	entropy := make([]byte, (bits+7)/8)
	if bits%8 != 0 {
		entropy[len(entropy)-1] = byte(1<<(8-bits%8) - 1)
	}
	data = append(data, entropy...)
	return append(data, 0xff, 0xd9)
}

func TestDecodeResizeCMYKAndYCCK(t *testing.T) {
	for _, transform := range []byte{0, 2} {
		for _, subsampled := range []bool{false, true} {
			t.Run(fmt.Sprintf("transform=%d/subsampled=%v", transform, subsampled), func(t *testing.T) {
				data := resizeStreamConstantCMYK(transform, subsampled)
				src, err := jpeg.Decode(bytes.NewReader(data))
				if err != nil {
					t.Fatalf("generated CMYK/YCCK reference: %v", err)
				}
				if _, ok := src.(*image.CMYK); !ok {
					t.Fatalf("reference is %T, want CMYK", src)
				}
				for _, target := range []image.Point{{17, 19}, {9, 10}, {5, 3}} {
					t.Run(fmt.Sprintf("%dx%d", target.X, target.Y), func(t *testing.T) {
						resizeStreamCheckWorkers(t, data, src, target, false)
					})
				}
			})
		}
	}
}

func TestDecodeResizeCannotReplayStream(t *testing.T) {
	rgb := readJPEG(t, "testdata/video-001.rgb.jpeg")
	cmyk := readJPEG(t, "testdata/video-001.cmyk.jpeg")
	color := readJPEG(t, "testdata/video-001.q50.420.jpeg")
	_, sosStart, sosEnd := resizeStreamSegment(t, color, 0xda)
	for _, test := range []struct {
		name  string
		data  []byte
		after []byte
	}{
		// JFIF changes an RGB frame's interpretation to YCbCr.
		{"late-JFIF", rgb, append([]byte{0xff, 0xe0, 0, 16}, []byte("JFIF\x00\x01\x01\x00\x00\x01\x00\x01\x00\x00")...)},
		// Adobe transform 2 changes CMYK to YCCK after samples are gone.
		{"late-Adobe", cmyk, append([]byte{0xff, 0xee, 0, 14}, []byte("Adobe\x00\x64\x00\x00\x00\x00\x02")...)},
		{"second-SOS", color, color[sosStart:sosEnd]},
	} {
		t.Run(test.name, func(t *testing.T) {
			if !bytes.HasSuffix(test.data, []byte{0xff, 0xd9}) {
				t.Fatal("test JPEG has no terminal EOI")
			}
			data := append(bytes.Clone(test.data[:len(test.data)-2]), test.after...)
			data = append(data, 0xff, 0xd9)
			for _, fallback := range []bool{false, true} {
				got, d, err := decodeResize(bytes.NewReader(data), 37, 29, &ResizeOptions{
					Workers: 2, AllowFullDecode: fallback,
				})
				var unsupported UnsupportedError
				if got != nil || !errors.As(err, &unsupported) {
					t.Fatalf("AllowFullDecode=%v: got %v, %v; want nil, UnsupportedError", fallback, got, err)
				}
				// These are late errors after a real, completed streaming
				// scan; opting in cannot replay the discarded input.
				resizeStreamAssertBounded(t, d, nil)
			}
		})
	}
}

func TestDecodeResizeShortReads(t *testing.T) {
	for _, input := range []struct {
		name     string
		data     []byte
		fallback bool
	}{
		{"tiny-gray", resizeStreamEncode(t, resizeStreamPattern(1, 1, true)), false},
		{"tiny-color", resizeStreamEncode(t, resizeStreamPattern(1, 1, false)), false},
		{"odd-color", resizeStreamEncode(t, resizeStreamPattern(33, 17, false)), false},
		{"progressive", readJPEG(t, "testdata/video-001.progressive.jpeg"), true},
		{"separate-scans", resizeStreamSeparateScans(), true},
	} {
		t.Run(input.name, func(t *testing.T) {
			src, err := jpeg.Decode(bytes.NewReader(input.data))
			if err != nil {
				t.Fatal(err)
			}
			w, h := min(17, src.Bounds().Dx()), min(9, src.Bounds().Dy())
			opts := &ResizeOptions{Workers: 4, AllowFullDecode: input.fallback}
			want, err := DecodeResize(bytes.NewReader(input.data), w, h, opts)
			if err != nil {
				t.Fatal(err)
			}
			for _, reader := range []struct {
				name string
				r    io.Reader
			}{
				{"one-byte", iotest.OneByteReader(bytes.NewReader(input.data))},
				{"half-read", iotest.HalfReader(bytes.NewReader(input.data))},
				{"data-and-EOF", iotest.DataErrReader(bytes.NewReader(input.data))},
			} {
				t.Run(reader.name, func(t *testing.T) {
					got, err := DecodeResize(reader.r, w, h, opts)
					if err != nil {
						t.Fatal(err)
					}
					if !samePixels(got, want) {
						t.Fatal("short reads changed visible pixels")
					}
				})
			}
		})
	}
}

func TestDecodeResizeMalformedAndTruncated(t *testing.T) {
	for _, path := range []string{"testdata/video-001.q50.420.jpeg", "testdata/video-001.progressive.jpeg"} {
		data := readJPEG(t, path)
		_, _, sosEnd := resizeStreamSegment(t, data, 0xda)
		for _, cut := range []int{0, 1, 24, sosEnd, len(data) / 2, len(data) - 2} {
			t.Run(fmt.Sprintf("%s/cut=%d", filepath.Base(path), cut), func(t *testing.T) {
				for _, workers := range []int{1, 2, 4} {
					got, d, err := decodeResize(bytes.NewReader(data[:cut]), 37, 29, &ResizeOptions{
						Workers: workers, AllowFullDecode: true,
					})
					resizeStreamAssertJoined(t, d)
					if got != nil || err == nil {
						t.Fatalf("workers %d accepted truncation: got %v, %v", workers, got, err)
					}
				}
			})
		}
	}
	for _, malformed := range [][]byte{[]byte("not a JPEG"), {0xff, 0xd8, 0xff, 0xdb, 0, 1}} {
		if got, err := DecodeResize(bytes.NewReader(malformed), 1, 1, nil); got != nil || err == nil {
			t.Fatalf("accepted malformed data %x: got %v, %v", malformed, got, err)
		}
	}
	data := readJPEG(t, "testdata/video-001.q50.420.jpeg")
	_, start, _ := resizeStreamSegment(t, data, 0xc0)
	for _, offset := range []int{5, 7} {
		bad := bytes.Clone(data)
		bad[start+offset], bad[start+offset+1] = 0, 0
		if got, err := DecodeResize(bytes.NewReader(bad), 1, 1, nil); got != nil || err == nil {
			t.Fatalf("accepted zero source dimension: got %v, %v", got, err)
		}
	}
	restart := readJPEG(t, "testdata/video-001.restart2.jpeg")
	if index := bytes.Index(restart, []byte{0xff, 0xd1}); index < 0 {
		t.Fatal("restart test input has no RST1")
	} else {
		restart[index+1] = 0xd2
	}
	got, d, err := decodeResize(bytes.NewReader(restart), 37, 29, &ResizeOptions{Workers: 4})
	resizeStreamAssertJoined(t, d)
	if got != nil || err == nil {
		t.Fatalf("accepted incorrect restart marker: got %v, %v", got, err)
	}
	readErr := errors.New("injected reader failure")
	if got, err := DecodeResize(iotest.ErrReader(readErr), 1, 1, nil); got != nil || !errors.Is(err, readErr) {
		t.Fatalf("reader failure: got %v, %v; want injected error", got, err)
	}
	// Fail after valid headers and entropy have started, not just before
	// any workers can exist.
	late := io.MultiReader(bytes.NewReader(data[:len(data)/2]), iotest.ErrReader(readErr))
	got, d, err = decodeResize(late, 37, 29, &ResizeOptions{Workers: 4})
	resizeStreamAssertJoined(t, d)
	if got != nil || !errors.Is(err, readErr) {
		t.Fatalf("late reader failure: got %v, %v; want injected error", got, err)
	}
}

func TestDecodeResizeErrorCleanup(t *testing.T) {
	// Repeated late failures must return and leave subsequent invocations
	// usable. Do not sleep or depend on the process-wide goroutine count,
	// which includes unrelated runtime and testing goroutines.
	data := readJPEG(t, "testdata/video-001.q50.420.jpeg")
	done := make(chan error, 1)
	go func() {
		for i := 0; i < 48; i++ {
			workers := []int{1, 2, 4}[i%3]
			cut := []int{len(data) / 2, len(data) - 2}[i%2]
			got, d, err := decodeResize(bytes.NewReader(data[:cut]), 37, 29, &ResizeOptions{Workers: workers})
			if got != nil || err == nil {
				done <- fmt.Errorf("iteration %d: accepted truncated input: %v", i, err)
				return
			}
			if d.resize.pipeline == nil || !d.resize.pipeline.joined {
				done <- fmt.Errorf("iteration %d: late error did not join workers", i)
				return
			}
			got, d, err = decodeResize(bytes.NewReader(data), 37, 29, &ResizeOptions{Workers: workers})
			if err != nil || got == nil {
				done <- fmt.Errorf("iteration %d: subsequent valid decode: %v", i, err)
				return
			}
			if d.resize.pipeline == nil || !d.resize.pipeline.joined {
				done <- fmt.Errorf("iteration %d: success did not join workers", i)
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
		t.Fatal("repeated error/success invocations did not finish; possible blocked worker")
	}
}

func resizeStreamMalformedProbe(data []byte, workers int, fallback bool) error {
	got, d, err := decodeResize(bytes.NewReader(data), 7, 5, &ResizeOptions{
		Workers: workers, AllowFullDecode: fallback, MaxSourcePixels: 1 << 20,
	})
	if d != nil && d.resize != nil && d.resize.pipeline != nil && !d.resize.pipeline.joined {
		return errors.New("decode returned with unjoined workers")
	}
	if d != nil && d.resize != nil && d.resize.streaming {
		if d.img1 != nil || d.img3 != nil || d.blackPix != nil {
			return errors.New("streaming decode allocated main source image planes")
		}
		for ci := range d.progCoeffs {
			if d.progCoeffs[ci] != nil || d.progNonzero[ci] != nil {
				return errors.New("streaming decode allocated image-wide coefficients")
			}
		}
	}
	if err != nil {
		if got != nil {
			return errors.New("decode returned an image along with an error")
		}
		return nil
	}
	if got == nil || got.Bounds() != image.Rect(0, 0, 7, 5) {
		return errors.New("successful decode has incorrect output bounds")
	}
	for y := 0; y < 5; y++ {
		for x := 0; x < 7; x++ {
			if got.RGBAAt(x, y).A != 255 {
				return errors.New("successful decode is not opaque")
			}
		}
	}
	return nil
}

func TestDecodeResizeMalformedProbes(t *testing.T) {
	var seeds [][]byte
	for _, path := range corpus(t) {
		seeds = append(seeds, readJPEG(t, path))
	}
	seeds = append(seeds, resizeStreamSeparateScans(), resizeStreamConstantCMYK(2, true))
	done := make(chan error, 1)
	go func() {
		random := rand.New(rand.NewSource(0x726573697a65))
		for i := 0; i < 192; i++ {
			data := bytes.Clone(seeds[i%len(seeds)])
			switch i % 5 {
			case 0:
				data = data[:random.Intn(len(data))]
			case 1:
				for range 1 + random.Intn(4) {
					data[random.Intn(len(data))] ^= byte(1 + random.Intn(255))
				}
			case 2:
				at := random.Intn(len(data))
				data = append(data[:at], data[at+1:]...)
			case 3:
				for j := 0; j < min(16, len(data)); j++ {
					data[len(data)-1-j] = byte(random.Intn(256))
				}
			case 4:
				// Also exercise valid inputs between malformed attempts.
			}
			if err := resizeStreamMalformedProbe(data, []int{1, 2, 4}[i%3], i%2 == 0); err != nil {
				done <- fmt.Errorf("probe %d: %w", i, err)
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
		t.Fatal("deterministic malformed probes did not finish; possible pipeline deadlock")
	}
}

func FuzzDecodeResize(f *testing.F) {
	for _, path := range corpus(f) {
		f.Add(readJPEG(f, path))
	}
	f.Add(resizeStreamSeparateScans())
	f.Add(resizeStreamConstantCMYK(2, true))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 1<<20 {
			return // Bound compressed input as well as decoded source pixels.
		}
		for _, fallback := range []bool{false, true} {
			got, d, err := decodeResize(bytes.NewReader(data), 7, 5, &ResizeOptions{
				Workers: 1 + len(data)%4, AllowFullDecode: fallback, MaxSourcePixels: 1 << 20,
			})
			resizeStreamAssertJoined(t, d)
			if d != nil && d.resize != nil && d.resize.streaming {
				resizeStreamAssertBounded(t, d, got)
			}
			if err != nil {
				if got != nil {
					t.Fatal("error returned a partial output image")
				}
				continue
			}
			if got == nil || got.Bounds() != image.Rect(0, 0, 7, 5) {
				t.Fatal("successful decode has incorrect output bounds")
			}
			cfg, cfgErr := jpeg.DecodeConfig(bytes.NewReader(data))
			if cfgErr != nil {
				continue // Malformed-input acceptance need not match stdlib.
			}
			if uint64(cfg.Width)*uint64(cfg.Height) > 1<<20 {
				t.Fatal("decode exceeded MaxSourcePixels")
			}
			if src, err := jpeg.Decode(bytes.NewReader(data)); err == nil {
				resizeStreamAssertNear(t, got, resizeStreamReference(src, 7, 5))
			}
		}
	})
}

// resizeStreamBenchmarkPhoto is deterministic and photo-like, not an actual
// photograph: smooth gradients, textured local detail, and strong boundaries
// give entropy decoding and filtering more work than the striped microbench.
func resizeStreamBenchmarkPhoto() image.Image {
	const width, height = 4000, 3000
	src := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			hash := uint32(x)*0x9e3779b1 ^ uint32(y)*0x85ebca77
			hash ^= hash >> 13
			texture := int(hash&31) - 15
			wave := int(22*math.Sin(float64(x)/47) + 17*math.Sin(float64(x+y)/83))
			r := 40 + 150*x/width + wave + texture
			g := 35 + 160*y/height - wave/2 + texture
			b := 155 - 85*y/height + 45*x/width + texture
			if (x/367+y/281)%5 == 0 {
				r, g, b = r+32, g-25, b-35
			}
			offset := y*src.Stride + 4*x
			src.Pix[offset] = byte(max(0, min(255, r)))
			src.Pix[offset+1] = byte(max(0, min(255, g)))
			src.Pix[offset+2] = byte(max(0, min(255, b)))
			src.Pix[offset+3] = 255
		}
	}
	return src
}

var resizeStreamBenchmarkResult *image.RGBA

func resizeStreamBenchmark(b *testing.B, data []byte) {
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		b.Fatal(err)
	}
	for _, edge := range []int{256, 128} {
		target := image.Pt(edge, max(1, int(math.Round(float64(cfg.Height)*float64(edge)/float64(cfg.Width)))))
		if cfg.Height > cfg.Width {
			target = image.Pt(max(1, int(math.Round(float64(cfg.Width)*float64(edge)/float64(cfg.Height)))), edge)
		}
		b.Run(fmt.Sprintf("%dx%d", target.X, target.Y), func(b *testing.B) {
			if target.X > cfg.Width || target.Y > cfg.Height {
				b.Skip("input is smaller than target; no upsampling")
			}
			for _, workers := range []int{1, 2, 4, 0} {
				name := fmt.Sprintf("workers=%d", workers)
				if workers == 0 {
					name = "workers=auto"
				}
				b.Run(name, func(b *testing.B) {
					b.ReportAllocs()
					b.SetBytes(int64(len(data)))
					opts := &ResizeOptions{Workers: workers, AllowFullDecode: true}
					b.ResetTimer()
					for range b.N {
						got, err := DecodeResize(bytes.NewReader(data), target.X, target.Y, opts)
						if err != nil {
							b.Fatal(err)
						}
						resizeStreamBenchmarkResult = got
					}
					b.StopTimer()
					b.ReportMetric(float64(runtime.GOMAXPROCS(0)), "gomaxprocs")
				})
			}
			b.Run("stdlib-full+Lanczos3", func(b *testing.B) {
				b.ReportAllocs()
				b.SetBytes(int64(len(data)))
				b.ResetTimer()
				for range b.N {
					src, err := jpeg.Decode(bytes.NewReader(data))
					if err != nil {
						b.Fatal(err)
					}
					resizeStreamBenchmarkResult = resizeStreamReference(src, target.X, target.Y)
				}
				b.StopTimer()
				b.ReportMetric(float64(runtime.GOMAXPROCS(0)), "gomaxprocs")
			})
			b.Run("reduced-factor8+Lanczos3", func(b *testing.B) {
				// Informative speed/memory baseline only: reduced IDCT does
				// not meet DecodeResize's full-decode quality contract.
				if (cfg.Width+7)/8 < target.X || (cfg.Height+7)/8 < target.Y {
					b.Skip("factor 8 would require upsampling")
				}
				b.ReportAllocs()
				b.SetBytes(int64(len(data)))
				b.ResetTimer()
				for range b.N {
					src, err := Decode(bytes.NewReader(data), 8)
					if err != nil {
						b.Fatal(err)
					}
					resizeStreamBenchmarkResult = resizeStreamReference(src, target.X, target.Y)
				}
				b.StopTimer()
				b.ReportMetric(float64(runtime.GOMAXPROCS(0)), "gomaxprocs")
			})
		})
	}
}

// Run with GOMAXPROCS=2 for the two-vCPU VM baseline, for example:
//
//	GOMAXPROCS=2 CGO_ENABLED=0 go test ./internal/images/resizejpeg \
//	  -run '^$' -bench '^BenchmarkDecodeResize$' -benchmem -benchtime=1s
//
// Worker counts are software concurrency, not a claim of four physical cores.
// Input generation and JPEG encoding happen before any measured leaf loop.
func BenchmarkDecodeResize(b *testing.B) {
	data := resizeStreamEncode(b, resizeStreamBenchmarkPhoto())
	b.Run("generated-4000x3000-420", func(b *testing.B) {
		resizeStreamBenchmark(b, data)
	})
}

// Optional real-photo benchmark; input is read unchanged, never downloaded or
// re-encoded. Omission is explicitly a skip, not real-photograph coverage.
func BenchmarkDecodeResizeRealJPEG(b *testing.B) {
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
		resizeStreamBenchmark(b, data)
	})
}

var _ func(io.Reader, int, int, *ResizeOptions) (*image.RGBA, error) = DecodeResize
