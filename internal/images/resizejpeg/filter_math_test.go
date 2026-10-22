/*
Copyright 2026 The Perkeep Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

     https://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package resizejpeg

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"math"
	"math/rand"
	"sync"
	"testing"

	xdraw "golang.org/x/image/draw"
)

func TestFilterValues(t *testing.T) {
	if Lanczos3 != 0 || CatmullRom != 1 || Bilinear != 2 {
		t.Fatal("filter values changed")
	}
	for _, tc := range []struct {
		filter Filter
		name   string
	}{
		{Lanczos3, "Lanczos3"},
		{CatmullRom, "CatmullRom"},
		{Bilinear, "Bilinear"},
	} {
		if !tc.filter.valid() || tc.filter.String() != tc.name {
			t.Fatalf("filter %d: valid=%v name=%q", tc.filter, tc.filter.valid(), tc.filter.String())
		}
	}
	for _, f := range []Filter{-1, 3, Filter(int(^uint(0) >> 1))} {
		if f.valid() || f.String() != "unknown" {
			t.Fatalf("invalid filter %d accepted", f)
		}
	}
}

func TestFilterDefaultLanczosGolden(t *testing.T) {
	// Captured by running the original resizer at commit 543817215, before
	// adding filter selection. Preserve its pixels exactly, not merely within
	// the reference scaler's one-level tolerance.
	h := sha256.New()
	for _, dims := range [][5]int{
		{47, 35, 1, 11, 9}, {513, 385, 2, 16, 12},
		{513, 385, 4, 16, 12}, {513, 385, 8, 16, 12},
		{1, 1, 8, 33, 29}, {3, 5, 4, 2, 3},
		{91, 1, 1, 13, 1}, {23, 4, 1, 5, 19}, {17, 19, 1, 17, 19},
	} {
		r, err := newScaledFilteredResizer(dims[0], dims[1], dims[2], dims[3], dims[4], Filter(0))
		if err != nil {
			t.Fatal(err)
		}
		src := image.NewRGBA(image.Rect(0, 0, r.sw, r.sh))
		for y := 0; y < r.sh; y++ {
			for x := 0; x < r.sw; x++ {
				for c := 0; c < 3; c++ {
					src.Pix[y*src.Stride+4*x+c] = byte(17*x + 29*y + 31*c + 7*x*y)
				}
				src.Pix[y*src.Stride+4*x+3] = 255
			}
		}
		filterMathStream(t, r, src, 7)
		h.Write(r.dst.Pix)
	}
	const want = "cb697f24c5d9bf74253d7a334253fbf42c40fca5895b6645b7067cf25113ebf2"
	if got := fmt.Sprintf("%x", h.Sum(nil)); got != want {
		t.Fatalf("default Lanczos pixels changed: digest %s, want %s", got, want)
	}
}

func filterMathReference(src *image.RGBA, dw, dh int, filter Filter) *image.RGBA {
	if filter == Lanczos3 {
		return lanczosTestReference(src, dw, dh)
	}
	kernel := xdraw.BiLinear
	if filter == CatmullRom {
		kernel = xdraw.CatmullRom
	}
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	kernel.Scale(dst, dst.Bounds(), src, src.Bounds(), draw.Src, nil)
	return dst
}

func filterMathStream(t *testing.T, r *lanczosResizer, src *image.RGBA, batch int) {
	t.Helper()
	rows := make([]float64, batch*3*r.dw)
	for y := 0; y < r.sh; {
		n := min(batch, r.sh-y)
		for i := 0; i < n; i++ {
			r.horizontal(rows[i*3*r.dw:(i+1)*3*r.dw], src.Pix[(y+i)*src.Stride:])
		}
		if err := r.addRows(rows, y, n); err != nil {
			t.Fatal(err)
		}
		y += n
	}
	if err := r.finish(); err != nil {
		t.Fatal(err)
	}
}

func TestFilterReference(t *testing.T) {
	sizes := [][4]int{
		{1, 1, 1, 1}, {1, 49, 1, 7}, {91, 1, 13, 1},
		{7, 5, 1, 1}, {31, 23, 17, 11}, {127, 67, 8, 4},
		{77, 41, 19, 33}, {257, 131, 3, 1}, {17, 19, 17, 19},
		{3, 5, 11, 7}, {23, 4, 5, 19}, {1, 1, 9, 11},
		{1537, 769, 13, 7},
	}
	for _, filter := range []Filter{Lanczos3, CatmullRom, Bilinear} {
		for _, size := range sizes {
			for _, pattern := range []string{"rgb", "gray", "edge", "stripes", "constant"} {
				t.Run(fmt.Sprintf("%s/%v/%s", filter, size, pattern), func(t *testing.T) {
					src := lanczosTestSource(size[0], size[1], pattern)
					want := filterMathReference(src, size[2], size[3], filter)
					var first []byte
					for _, batch := range []int{1, 7, 17} {
						r, err := newFilteredResizer(size[0], size[1], size[2], size[3], filter)
						if err != nil {
							t.Fatal(err)
						}
						filterMathStream(t, r, src, batch)
						lanczosTestCompare(t, r.dst, want)
						if first == nil {
							first = append([]byte(nil), r.dst.Pix...)
						} else if !bytes.Equal(first, r.dst.Pix) {
							t.Fatalf("batch %d changed pixels", batch)
						}
					}
				})
			}
		}
	}
}

func TestFilterRandomReference(t *testing.T) {
	random := rand.New(rand.NewSource(81271))
	for _, filter := range []Filter{Lanczos3, CatmullRom, Bilinear} {
		for i := 0; i < 100; i++ {
			sw, sh := 1+random.Intn(96), 1+random.Intn(96)
			dw, dh := 1+random.Intn(96), 1+random.Intn(96)
			t.Run(fmt.Sprintf("%s/%dx%d-%dx%d", filter, sw, sh, dw, dh), func(t *testing.T) {
				r, err := newFilteredResizer(sw, sh, dw, dh, filter)
				if err != nil {
					t.Fatal(err)
				}
				src := lanczosTestSource(sw, sh, "rgb")
				filterMathStream(t, r, src, 7)
				lanczosTestCompare(t, r.dst, filterMathReference(src, dw, dh, filter))
			})
		}
	}
}

func TestFilterScaledAlignedReference(t *testing.T) {
	for _, filter := range []Filter{Lanczos3, CatmullRom, Bilinear} {
		for _, factor := range []int{1, 2, 4, 8} {
			for _, pattern := range []string{"rgb", "stripes", "edge"} {
				t.Run(fmt.Sprintf("%s/factor-%d/%s", filter, factor, pattern), func(t *testing.T) {
					r, err := newScaledFilteredResizer(47*factor, 35*factor, factor, 11, 9, filter)
					if err != nil {
						t.Fatal(err)
					}
					src := lanczosTestSource(r.sw, r.sh, pattern)
					filterMathStream(t, r, src, 5)
					lanczosTestCompare(t, r.dst, filterMathReference(src, r.dw, r.dh, filter))
				})
			}
		}
	}
}

// filterMathKernel is deliberately independent of the production functions:
// expanded cubics and a product of sinc functions rather than their factored
// implementations. Its input can include samples outside the support.
func filterMathKernel(filter Filter, x float64) float64 {
	x = math.Abs(x)
	switch filter {
	case Bilinear:
		return math.Max(0, 1-x)
	case CatmullRom:
		if x < 1 {
			return 1.5*x*x*x - 2.5*x*x + 1
		}
		if x < 2 {
			return -0.5*x*x*x + 2.5*x*x - 4*x + 2
		}
		return 0
	default:
		if x >= 3 {
			return 0
		}
		if x == 0 {
			return 1
		}
		return (math.Sin(math.Pi*x) / (math.Pi * x)) *
			(math.Sin(math.Pi*x/3) / (math.Pi * x / 3))
	}
}

func TestFilterKernelMath(t *testing.T) {
	for _, filter := range []Filter{Lanczos3, CatmullRom, Bilinear} {
		_, at := filter.kernel()
		for i := 0; i <= 1000; i++ {
			x := float64(i) / 250
			if got, want := at(x), filterMathKernel(filter, x); math.Abs(got-want) > 1e-13 {
				t.Fatalf("%s(%g) = %g, want %g", filter, x, got, want)
			}
		}
	}
}

func TestFilterSpanOrder(t *testing.T) {
	// Catmull-Rom has exact interior zeros at integer distances. Trimming
	// those zeros used to make a row finish before its predecessor, leaving
	// the ordered collector stuck. Geometric support bounds must stay ordered.
	for _, filter := range []Filter{Lanczos3, CatmullRom, Bilinear} {
		support, at := filter.kernel()
		for _, factor := range []int{1, 2, 4, 8} {
			for original := 1; original <= 35; original++ {
				src := (original-1)/factor + 1
				for dst := 1; dst <= 47; dst++ {
					spans := filterSpans(src, float64(original)/float64(factor), dst, support, at)
					first, last := 0, 0
					for d, span := range spans {
						if span.first < first || span.last < last || span.last <= span.first ||
							math.IsNaN(span.invWeight) || math.IsInf(span.invWeight, 0) {
							t.Fatalf("%s %d/%d -> %d at %d: unordered or empty span %+v",
								filter, original, factor, dst, d, span)
						}
						first, last = span.first, span.last
					}
				}
			}
		}
	}
}

// filterMathDenseReference does a direct 2D convolution over every source pixel,
// without production spans, tables, ring storage, or separable intermediates.
// The nominal extent is independent of the ceil-rounded sample grid.
func filterMathDenseReference(src *image.RGBA, ow, oh, factor, dw, dh int, filter Filter) *image.RGBA {
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	sx, sy := float64(ow)/float64(factor*dw), float64(oh)/float64(factor*dh)
	for y := 0; y < dh; y++ {
		cy := (float64(y)+0.5)*sy - 0.5
		for x := 0; x < dw; x++ {
			cx := (float64(x)+0.5)*sx - 0.5
			var sum [3]float64
			total := 0.0
			for v := 0; v < src.Bounds().Dy(); v++ {
				wy := filterMathKernel(filter, (cy-float64(v))/math.Max(1, sy))
				for u := 0; u < src.Bounds().Dx(); u++ {
					w := wy * filterMathKernel(filter, (cx-float64(u))/math.Max(1, sx))
					total += w
					for c := range sum {
						sum[c] += w * float64(src.Pix[v*src.Stride+4*u+c])
					}
				}
			}
			if total == 0 || math.IsNaN(total) || math.IsInf(total, 0) {
				panic("invalid reference normalization")
			}
			for c := range sum {
				value := math.Floor(65535*(sum[c]/(255*total)) + 0.5)
				dst.Pix[y*dst.Stride+4*x+c] = uint8(uint16(math.Max(0, math.Min(65535, value))) >> 8)
			}
			dst.Pix[y*dst.Stride+4*x+3] = 255
		}
	}
	return dst
}

func TestFilterScaledOddDenseReference(t *testing.T) {
	for _, filter := range []Filter{Lanczos3, CatmullRom, Bilinear} {
		for _, dims := range [][5]int{
			{65, 49, 2, 13, 9}, {65, 49, 4, 13, 9},
			{513, 385, 8, 16, 12}, {9, 7, 4, 11, 13},
			{1, 9, 8, 7, 9}, {3, 1, 8, 9, 7},
		} {
			t.Run(fmt.Sprintf("%s/%v", filter, dims), func(t *testing.T) {
				ow, oh, factor, dw, dh := dims[0], dims[1], dims[2], dims[3], dims[4]
				r, err := newScaledFilteredResizer(ow, oh, factor, dw, dh, filter)
				if err != nil {
					t.Fatal(err)
				}
				if r.sw != (ow-1)/factor+1 || r.sh != (oh-1)/factor+1 {
					t.Fatal("sample dimensions were not ceil-rounded")
				}
				for x, span := range r.x {
					want := (float64(x)+0.5)*float64(ow)/float64(factor*dw) - 0.5
					if math.Abs(span.center-want) > 1e-12 {
						t.Fatalf("x=%d center=%g, want %g", x, span.center, want)
					}
				}
				for y, span := range r.y {
					want := (float64(y)+0.5)*float64(oh)/float64(factor*dh) - 0.5
					if math.Abs(span.center-want) > 1e-12 {
						t.Fatalf("y=%d center=%g, want %g", y, span.center, want)
					}
				}
				src := lanczosTestSource(r.sw, r.sh, "rgb")
				filterMathStream(t, r, src, 3)
				lanczosTestCompare(t, r.dst, filterMathDenseReference(src, ow, oh, factor, dw, dh, filter))
			})
		}
	}
}

func TestFilterTinyConstant(t *testing.T) {
	for _, filter := range []Filter{Lanczos3, CatmullRom, Bilinear} {
		for _, dims := range [][5]int{
			{1, 1, 8, 1, 1}, {1, 65, 8, 1, 2}, {65, 1, 8, 2, 1},
			{3, 5, 4, 2, 3}, {1, 1, 8, 33, 29}, {1, 7, 8, 19, 23},
		} {
			t.Run(fmt.Sprintf("%s/%v", filter, dims), func(t *testing.T) {
				r, err := newScaledFilteredResizer(dims[0], dims[1], dims[2], dims[3], dims[4], filter)
				if err != nil {
					t.Fatal(err)
				}
				filterMathStream(t, r, lanczosTestSource(r.sw, r.sh, "constant"), 7)
				for y := 0; y < r.dh; y++ {
					for x := 0; x < r.dw; x++ {
						if got := r.dst.RGBAAt(x, y); got != (color.RGBA{37, 129, 253, 255}) {
							t.Fatalf("pixel %d,%d = %v", x, y, got)
						}
					}
				}
			})
		}
	}
}

func TestFilterUnclampedIntermediate(t *testing.T) {
	for _, filter := range []Filter{Lanczos3, CatmullRom, Bilinear} {
		r, err := newFilteredResizer(8, 1, 31, 1, filter)
		if err != nil {
			t.Fatal(err)
		}
		src := make([]byte, 4*r.sw)
		for x := 0; x < r.sw; x++ {
			if x >= r.sw/2 {
				src[4*x] = 255
			}
			src[4*x+3] = 255
		}
		before := append([]byte(nil), src...)
		row := make([]float64, 3*r.dw)
		r.horizontal(row, src)
		var negative, overshoot, fractional bool
		for x := 0; x < r.dw; x++ {
			v := row[3*x]
			negative = negative || v < -1e-12
			overshoot = overshoot || v > 1+1e-12
			fractional = fractional || math.Abs(v*255-math.Round(v*255)) > 1e-6
		}
		wantRinging := filter != Bilinear
		if negative != wantRinging || overshoot != wantRinging || !fractional {
			t.Fatalf("%s: negative=%v overshoot=%v fractional=%v", filter, negative, overshoot, fractional)
		}
		if !bytes.Equal(src, before) {
			t.Fatal("horizontal changed the source")
		}
	}
}

func TestFilterRingMemory(t *testing.T) {
	for _, filter := range []Filter{Lanczos3, CatmullRom, Bilinear} {
		support, _ := filter.kernel()
		var short *lanczosResizer
		for _, sh := range []int{257, 65535} {
			r, err := newFilteredResizer(1021, sh, 13, 19, filter)
			if err != nil {
				t.Fatal(err)
			}
			if r.ringRows > 2*support+1 || len(r.sums) > (2*support+1)*3*r.dw {
				t.Fatalf("%s: %d accumulation rows, %d floats", filter, r.ringRows, len(r.sums))
			}
			if len(r.y) != r.dh || cap(r.y) != r.dh {
				t.Fatal("vertical metadata depends on source height")
			}
			if cap(r.xWeights) > 2*support*max(r.sw, r.dw)+2*r.dw {
				t.Fatal("excess horizontal table capacity")
			}
			if short == nil {
				short = r
			} else if len(short.sums) != len(r.sums) || cap(short.xWeights) != cap(r.xWeights) {
				t.Fatal("source height increased floating-point storage")
			}
		}
	}
}

func TestFilterConcurrentHorizontal(t *testing.T) {
	for _, filter := range []Filter{Lanczos3, CatmullRom, Bilinear} {
		t.Run(filter.String(), func(t *testing.T) {
			r, err := newFilteredResizer(193, 101, 29, 17, filter)
			if err != nil {
				t.Fatal(err)
			}
			src := lanczosTestSource(r.sw, r.sh, "rgb")
			ready := make([]chan []float64, r.sh)
			jobs := make(chan int, r.sh)
			for y := range ready {
				ready[y] = make(chan []float64, 1)
				jobs <- y
			}
			close(jobs)
			var workers sync.WaitGroup
			for i := 0; i < 8; i++ {
				workers.Add(1)
				go func() {
					defer workers.Done()
					for y := range jobs {
						row := make([]float64, 3*r.dw)
						r.horizontal(row, src.Pix[y*src.Stride:])
						ready[y] <- row
					}
				}()
			}
			for y := 0; y < r.sh; y++ {
				if err := r.addRows(<-ready[y], y, 1); err != nil {
					t.Fatal(err)
				}
			}
			workers.Wait()
			if err := r.finish(); err != nil {
				t.Fatal(err)
			}
			lanczosTestCompare(t, r.dst, filterMathReference(src, r.dw, r.dh, filter))
		})
	}
}

func TestFilterNoPerRowAllocations(t *testing.T) {
	for _, filter := range []Filter{Lanczos3, CatmullRom, Bilinear} {
		r, err := newScaledFilteredResizer(93, 201, 2, 11, 13, filter)
		if err != nil {
			t.Fatal(err)
		}
		src := lanczosTestSource(r.sw, r.sh, "rgb")
		row := make([]float64, 3*r.dw)
		if got := testing.AllocsPerRun(10, func() {
			copy := *r
			for y := 0; y < copy.sh; y++ {
				copy.horizontal(row, src.Pix[y*src.Stride:])
				if err := copy.addRows(row, y, 1); err != nil {
					panic(err)
				}
			}
			if err := copy.finish(); err != nil {
				panic(err)
			}
		}); got != 0 {
			t.Fatalf("%s: stream allocated %g times", filter, got)
		}
	}
}

func TestFilterInvalidAndOverflow(t *testing.T) {
	maxInt := int(^uint(0) >> 1)
	for _, filter := range []Filter{-1, 3, Filter(maxInt)} {
		if _, err := newFilteredResizer(1, 1, 1, 1, filter); err == nil {
			t.Fatalf("accepted filter %d", filter)
		}
		if _, err := newScaledFilteredResizer(1, 1, 8, 1, 1, filter); err == nil {
			t.Fatalf("accepted reduced filter %d", filter)
		}
	}
	for _, filter := range []Filter{Lanczos3, CatmullRom, Bilinear} {
		for _, dims := range [][4]int{
			{0, 1, 1, 1}, {1, 0, 1, 1}, {1, 1, 0, 1}, {1, 1, 1, 0},
			{-1, 1, 1, 1}, {1, -1, 1, 1}, {1, 1, -1, 1}, {1, 1, 1, -1},
			{maxInt, 1, 1, 1}, {1, 1, maxInt, 1}, {1, 1, 1, maxInt},
			{maxInt / 4, 1, 1, 1}, {1, 1, maxInt / 16, 1},
			{1, 1, maxInt / 4, maxInt / 4},
			// Individually valid allocations whose combined support table
			// or accumulation-ring allocation overflows.
			{1, 1, maxInt / 64, 4}, {1, 1, 4, maxInt / 64},
			{maxInt / 64, 8, maxInt / 64, 4},
			{maxInt / 16, 1, maxInt / 64, 1},
		} {
			if _, err := newFilteredResizer(dims[0], dims[1], dims[2], dims[3], filter); err == nil {
				t.Fatalf("%s accepted dimensions %v", filter, dims)
			}
		}
		for _, dims := range [][5]int{
			{1, 1, 0, 1, 1}, {1, 1, 3, 1, 1}, {1, 1, -1, 1, 1},
			{0, 1, 2, 1, 1}, {1, -1, 2, 1, 1},
			{maxInt, 1, 8, 1, 1}, {1, 1, 8, maxInt, 1},
			{1, 1, 8, 1, maxInt}, {1, 1, 8, 0, 1},
		} {
			if _, err := newScaledFilteredResizer(dims[0], dims[1], dims[2], dims[3], dims[4], filter); err == nil {
				t.Fatalf("%s accepted reduced dimensions %v", filter, dims)
			}
		}
	}
}
