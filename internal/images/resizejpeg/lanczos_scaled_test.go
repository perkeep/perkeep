/*
Copyright 2026 The Perkeep Authors

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
	"fmt"
	"image/color"
	"math"
	"testing"
)

func TestScaledLanczosAlignedReference(t *testing.T) {
	for _, factor := range []int{1, 2, 4, 8} {
		for _, pattern := range []string{"rgb", "constant", "stripes", "edge"} {
			t.Run(fmt.Sprintf("factor-%d/%s", factor, pattern), func(t *testing.T) {
				src := lanczosTestSource(47, 35, pattern)
				f, err := newScaledLanczosResizer(47*factor, 35*factor, factor, 11, 9)
				if err != nil {
					t.Fatal(err)
				}
				row := make([]float64, 3*f.dw)
				for y := 0; y < f.sh; y++ {
					f.horizontal(row, src.Pix[y*src.Stride:])
					if err := f.addRows(row, y, 1); err != nil {
						t.Fatal(err)
					}
				}
				if err := f.finish(); err != nil {
					t.Fatal(err)
				}
				lanczosTestCompare(t, f.dst, lanczosTestReference(src, f.dw, f.dh))
			})
		}
	}
}

func TestScaledLanczosOddPhase(t *testing.T) {
	for _, factor := range []int{2, 4, 8} {
		f, err := newScaledLanczosResizer(513, 385, factor, 16, 12)
		if err != nil {
			t.Fatal(err)
		}
		if f.sw != (513+factor-1)/factor || f.sh != (385+factor-1)/factor {
			t.Fatal("reduced sample counts were not rounded up")
		}
		for x, span := range f.x {
			want := (float64(x)+0.5)*513/(float64(factor)*16) - 0.5
			if math.Abs(span.center-want) > 1e-12 {
				t.Fatalf("factor %d x=%d center=%g, want %g", factor, x, span.center, want)
			}
		}
		for y, span := range f.y {
			want := (float64(y)+0.5)*385/(float64(factor)*12) - 0.5
			if math.Abs(span.center-want) > 1e-12 {
				t.Fatalf("factor %d y=%d center=%g, want %g", factor, y, span.center, want)
			}
		}
		if want := float64(factor*12) / 385; math.Abs(f.yScale-want) > 1e-12 {
			t.Fatalf("vertical kernel scale=%g, want %g", f.yScale, want)
		}
		last := f.x[len(f.x)-1]
		naive := (float64(f.dw)-0.5)*float64(f.sw)/float64(f.dw) - 0.5
		if math.Abs(last.center-naive) < 0.1 {
			t.Fatal("fixture does not expose ceil-rounded sampling drift")
		}
	}
}

func TestScaledLanczosTinyConstant(t *testing.T) {
	// A one-pixel axis may still allow reduction of the other axis. Its
	// nominal extent can be smaller than one sample, but edge normalization
	// must preserve constant colors and keep the accumulation ring valid.
	for _, dims := range [][5]int{{1, 1, 8, 1, 1}, {1, 65, 8, 1, 2}, {65, 1, 8, 2, 1}, {3, 5, 4, 2, 3}} {
		f, err := newScaledLanczosResizer(dims[0], dims[1], dims[2], dims[3], dims[4])
		if err != nil {
			t.Fatal(err)
		}
		src := lanczosTestSource(f.sw, f.sh, "constant")
		row := make([]float64, 3*f.dw)
		for y := 0; y < f.sh; y++ {
			f.horizontal(row, src.Pix[y*src.Stride:])
			if err := f.addRows(row, y, 1); err != nil {
				t.Fatal(err)
			}
		}
		if err := f.finish(); err != nil {
			t.Fatal(err)
		}
		for y := 0; y < f.dh; y++ {
			for x := 0; x < f.dw; x++ {
				if got := f.dst.RGBAAt(x, y); got != (color.RGBA{37, 129, 253, 255}) {
					t.Fatalf("dimensions %v pixel %d,%d=%v", dims, x, y, got)
				}
			}
		}
	}
}

func TestScaledLanczosInvalid(t *testing.T) {
	maxInt := int(^uint(0) >> 1)
	for _, dims := range [][5]int{
		{1, 1, 0, 1, 1}, {1, 1, 3, 1, 1}, {1, 1, -1, 1, 1},
		{0, 1, 2, 1, 1}, {1, -1, 2, 1, 1},
		{maxInt, 1, 8, 1, 1}, {1, 1, 8, maxInt, 1},
		{1, 1, 8, 1, maxInt}, {1, 1, 8, 0, 1},
	} {
		if _, err := newScaledLanczosResizer(dims[0], dims[1], dims[2], dims[3], dims[4]); err == nil {
			t.Fatalf("accepted invalid or overflowing dimensions %v", dims)
		}
	}
}
