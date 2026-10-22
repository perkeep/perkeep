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
	"fmt"
	"image"
	"image/draw"
	"math"
	"math/rand"
	"sync"
	"testing"

	xdraw "golang.org/x/image/draw"
)

// Keep this independent of lanczosKernel and of the optional jpegquality tests.
func lanczosTestReference(src *image.RGBA, width, height int) *image.RGBA {
	kernel := &xdraw.Kernel{
		Support: 3,
		At: func(x float64) float64 {
			if x == 0 {
				return 1
			}
			x *= math.Pi
			return math.Sin(x) * math.Sin(x/3) / (x * x / 3)
		},
	}
	dst := image.NewRGBA(image.Rect(0, 0, width, height))
	kernel.Scale(dst, dst.Bounds(), src, src.Bounds(), draw.Src, nil)
	return dst
}

func lanczosTestSource(width, height int, pattern string) *image.RGBA {
	// A non-zero source origin exercises slicing the actual row span rather
	// than accidentally relying on image coordinates or a zero source origin.
	src := image.NewRGBA(image.Rect(9, -5, width+9, height-5))
	random := rand.New(rand.NewSource(int64(width*123 + height)))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			offset := y*src.Stride + 4*x
			pixel := src.Pix[offset : offset+4]
			switch pattern {
			case "rgb":
				_, _ = random.Read(pixel[:3])
			case "gray":
				v := byte(random.Intn(256))
				pixel[0], pixel[1], pixel[2] = v, v, v
			case "edge":
				if x*3 >= width {
					pixel[0] = 255
				}
				if y*2 >= height {
					pixel[1] = 255
				}
				if (x+y)%7 == 0 || x == width-1 || y == 0 {
					pixel[2] = 255
				}
			case "stripes":
				pixel[0] = byte((x % 2) * 255)
				pixel[1] = byte((y % 2) * 255)
				pixel[2] = byte(((x + y) % 2) * 255)
			case "constant":
				pixel[0], pixel[1], pixel[2] = 37, 129, 253
			default:
				panic(pattern)
			}
			pixel[3] = 255
		}
	}
	return src
}

func lanczosTestStream(t *testing.T, src *image.RGBA, width, height, batch int) *lanczosResizer {
	t.Helper()
	r, err := newLanczosResizer(src.Bounds().Dx(), src.Bounds().Dy(), width, height)
	if err != nil {
		t.Fatal(err)
	}
	rows := make([]float64, batch*3*width)
	for y := 0; y < r.sh; {
		n := min(batch, r.sh-y)
		for i := 0; i < n; i++ {
			r.horizontal(rows[i*3*width:(i+1)*3*width], src.Pix[(y+i)*src.Stride:])
		}
		if err := r.addRows(rows, y, n); err != nil {
			t.Fatal(err)
		}
		// Overwriting the same buffer for the next batch also checks that
		// addRows does not retain its input.
		y += n
	}
	if err := r.finish(); err != nil {
		t.Fatal(err)
	}
	return r
}

func lanczosTestCompare(t *testing.T, got, want *image.RGBA) {
	t.Helper()
	if got.Bounds() != want.Bounds() {
		t.Fatalf("bounds = %v, want %v", got.Bounds(), want.Bounds())
	}
	for y := 0; y < got.Bounds().Dy(); y++ {
		for x := 0; x < got.Bounds().Dx(); x++ {
			for c := 0; c < 4; c++ {
				a := int(got.Pix[y*got.Stride+4*x+c])
				b := int(want.Pix[y*want.Stride+4*x+c])
				if c == 3 && a != 255 {
					t.Fatalf("alpha(%d,%d) = %d, want 255", x, y, a)
				}
				if delta := a - b; delta < -1 || delta > 1 {
					t.Fatalf("pixel(%d,%d) channel %d = %d, reference %d", x, y, c, a, b)
				}
			}
		}
	}
}

func TestLanczosReference(t *testing.T) {
	sizes := [][4]int{
		{1, 1, 1, 1},
		{1, 49, 1, 7},
		{91, 1, 13, 1},
		{7, 5, 1, 1},
		{31, 23, 17, 11},
		{127, 67, 8, 4},
		{77, 41, 19, 33},
		{257, 131, 3, 1},
		{17, 19, 17, 19},
		{3, 5, 11, 7},
		{23, 4, 5, 19},
		{1, 1, 9, 11},
		{1537, 769, 13, 7},
	}
	for _, size := range sizes {
		for _, pattern := range []string{"rgb", "gray", "edge", "stripes", "constant"} {
			t.Run(fmt.Sprintf("%dx%d-%dx%d/%s", size[0], size[1], size[2], size[3], pattern), func(t *testing.T) {
				src := lanczosTestSource(size[0], size[1], pattern)
				want := lanczosTestReference(src, size[2], size[3])
				var first []byte
				for _, batch := range []int{1, 4, 17} {
					r := lanczosTestStream(t, src, size[2], size[3], batch)
					lanczosTestCompare(t, r.dst, want)
					if first == nil {
						first = append([]byte(nil), r.dst.Pix...)
					} else if !bytes.Equal(first, r.dst.Pix) {
						t.Fatalf("batch %d changed the result", batch)
					}
				}
			})
		}
	}
}

func TestLanczosRandomSizes(t *testing.T) {
	random := rand.New(rand.NewSource(734))
	for i := 0; i < 100; i++ {
		sw, sh := 1+random.Intn(96), 1+random.Intn(96)
		dw, dh := 1+random.Intn(96), 1+random.Intn(96)
		t.Run(fmt.Sprintf("%dx%d-%dx%d", sw, sh, dw, dh), func(t *testing.T) {
			src := lanczosTestSource(sw, sh, "rgb")
			r := lanczosTestStream(t, src, dw, dh, 7)
			lanczosTestCompare(t, r.dst, lanczosTestReference(src, dw, dh))
		})
	}
}

func TestLanczosIntermediateNotClamped(t *testing.T) {
	r, err := newLanczosResizer(8, 1, 31, 1)
	if err != nil {
		t.Fatal(err)
	}
	src := make([]byte, 4*8)
	for x := 0; x < 8; x++ {
		if x >= 4 {
			src[4*x] = 255
		}
		src[4*x+3] = 255
	}
	original := append([]byte(nil), src...)
	row := make([]float64, 3*31)
	r.horizontal(row, src)
	var negative, overshoot, fractional bool
	for x := 0; x < 31; x++ {
		v := row[3*x]
		negative = negative || v < 0
		overshoot = overshoot || v > 1
		fractional = fractional || math.Abs(v*255-math.Round(v*255)) > 1e-6
	}
	if !negative || !overshoot || !fractional {
		t.Fatalf("horizontal values were clamped/rounded: negative=%v overshoot=%v fractional=%v",
			negative, overshoot, fractional)
	}
	if !bytes.Equal(src, original) {
		t.Fatal("horizontal changed source pixels")
	}
}

func TestLanczosMemoryIndependentOfSourceHeight(t *testing.T) {
	short, err := newLanczosResizer(1021, 257, 13, 9)
	if err != nil {
		t.Fatal(err)
	}
	tall, err := newLanczosResizer(1021, 65535, 13, 9)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range []*lanczosResizer{short, tall} {
		if r.ringRows > 7 || len(r.sums) > 7*3*r.dw {
			t.Fatalf("source %d rows: %d accumulation rows, %d float64s", r.sh, r.ringRows, len(r.sums))
		}
		if len(r.y) != r.dh || cap(r.y) != r.dh {
			t.Fatalf("vertical metadata size = %d/%d, want %d", len(r.y), cap(r.y), r.dh)
		}
		if cap(r.xWeights) > 7*(r.sw+r.dw) {
			t.Fatalf("horizontal weights capacity = %d for %d -> %d", cap(r.xWeights), r.sw, r.dw)
		}
		if len(r.dst.Pix) != 4*r.dw*r.dh {
			t.Fatalf("output size = %d", len(r.dst.Pix))
		}
	}
	if len(short.sums) != len(tall.sums) || cap(short.xWeights) != cap(tall.xWeights) {
		t.Fatal("source height increased floating-point storage")
	}
	if span := tall.y[4]; span.last-span.first < 10000 {
		t.Fatalf("vertical support was not widened: %+v", span)
	}
	if span := tall.x[6]; span.last-span.first < 400 {
		t.Fatalf("horizontal support was not widened: %+v", span)
	}
}

func TestLanczosConcurrentHorizontal(t *testing.T) {
	src := lanczosTestSource(193, 101, "rgb")
	r, err := newLanczosResizer(193, 101, 29, 17)
	if err != nil {
		t.Fatal(err)
	}
	ready := make([]chan []float64, r.sh)
	for y := range ready {
		ready[y] = make(chan []float64, 1)
	}
	jobs := make(chan int, r.sh)
	for y := 0; y < r.sh; y++ {
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
	// The ordered collector runs while other workers are still filtering.
	for y := 0; y < r.sh; y++ {
		if err := r.addRows(<-ready[y], y, 1); err != nil {
			t.Fatal(err)
		}
	}
	workers.Wait()
	if err := r.finish(); err != nil {
		t.Fatal(err)
	}
	lanczosTestCompare(t, r.dst, lanczosTestReference(src, r.dw, r.dh))
}

func TestLanczosRowErrors(t *testing.T) {
	r, err := newLanczosResizer(9, 7, 3, 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.finish(); err == nil {
		t.Fatal("finish accepted no source rows")
	}
	if err := r.addRows(nil, 0, 0); err != nil {
		t.Fatal(err)
	}
	row := make([]float64, 3*r.dw)
	for _, tc := range []struct {
		rows []float64
		y, n int
	}{
		{row, -1, 1},
		{row, 1, 1},
		{row, 0, -1},
		{row, 0, 8},
		{row, 0, int(^uint(0) >> 1)},
		{row[:len(row)-1], 0, 1},
		{row, 8, 0},
	} {
		if err := r.addRows(tc.rows, tc.y, tc.n); err == nil {
			t.Fatalf("accepted y=%d, n=%d, len=%d", tc.y, tc.n, len(tc.rows))
		}
		if r.nextSourceY != 0 || r.firstActive != 0 || r.nextOpen != 0 {
			t.Fatal("invalid call changed the filter state")
		}
	}
	for y := 0; y < r.sh; y++ {
		if err := r.addRows(row, y, 1); err != nil {
			t.Fatal(err)
		}
		if y != r.sh-1 && r.finish() == nil {
			t.Fatalf("finish accepted only %d source rows", y+1)
		}
		if err := r.addRows(row, y, 1); err == nil {
			t.Fatalf("accepted repeated source row %d", y)
		}
	}
	if err := r.finish(); err != nil {
		t.Fatal(err)
	}
	if err := r.finish(); err != nil {
		t.Fatalf("second finish: %v", err)
	}
	if err := r.addRows(nil, r.sh, 0); err != nil {
		t.Fatal(err)
	}
	if err := r.addRows(row, r.sh, 1); err == nil {
		t.Fatal("accepted rows after end of source")
	}
}

func TestLanczosDimensions(t *testing.T) {
	maxInt := int(^uint(0) >> 1)
	for _, dims := range [][4]int{
		{0, 1, 1, 1}, {1, 0, 1, 1}, {1, 1, 0, 1}, {1, 1, 1, 0},
		{-1, 1, 1, 1}, {1, -1, 1, 1}, {1, 1, -1, 1}, {1, 1, 1, -1},
		{maxInt, 1, 1, 1}, {1, 1, maxInt, 1}, {1, 1, 1, maxInt},
		{maxInt / 4, 1, 1, 1}, {1, 1, maxInt / 16, 1},
		{1, 1, maxInt / 4, maxInt / 4},
		// Each individual row/metadata allocation fits, but a ring or the
		// widened horizontal weights do not. Reject before looping over or
		// allocating the enormous (but individually representable) axes.
		{1, 1, maxInt / 64, 4},
		{1, 1, 4, maxInt / 64},
		{maxInt / 64, 8, maxInt / 64, 4},
		{maxInt / 16, 1, maxInt / 64, 1},
	} {
		if _, err := newLanczosResizer(dims[0], dims[1], dims[2], dims[3]); err == nil {
			t.Fatalf("accepted dimensions %v", dims)
		}
	}
}

func TestLanczosNoPerRowAllocations(t *testing.T) {
	r, err := newLanczosResizer(47, 101, 11, 13)
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
		t.Fatalf("stream allocated %g times", got)
	}
}
