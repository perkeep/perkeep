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
	"fmt"
	"testing"
)

func memoryPlanDecoder(width, height, factor int, sampling [][2]int) *decoder {
	d := &decoder{width: width, height: height, factor: factor, nComp: len(sampling)}
	for ci, hv := range sampling {
		d.comp[ci] = component{h: hv[0], v: hv[1]}
		d.maxH, d.maxV = max(d.maxH, hv[0]), max(d.maxV, hv[1])
	}
	return d
}

func memoryPlanMCUs(d *decoder) (int, int) {
	return (d.width-1)/(8*d.maxH) + 1, (d.height-1)/(8*d.maxV) + 1
}

func TestMemoryFilterGeometry(t *testing.T) {
	for _, filter := range []Filter{Lanczos3, CatmullRom, Bilinear} {
		for _, dims := range [][4]int{
			{17, 19, 3, 2}, {33, 7, 8, 3}, {1, 1, 1, 1}, {3, 5, 3, 5},
		} {
			for _, factor := range []int{1, 2, 4, 8} {
				r, err := newScaledFilteredResizer(dims[0], dims[1], factor, dims[2], dims[3], filter)
				if err != nil {
					t.Fatal(err)
				}
				a, err := filterAllocationGeometry(r.sw, r.sh, r.dw, r.dh, float64(dims[1])/float64(factor), filter)
				if err != nil {
					t.Fatal(err)
				}
				if a.weightCount != cap(r.xWeights) || a.ringRows != r.ringRows || a.sumBytes != 8*len(r.sums) {
					t.Fatalf("%s dims=%v factor=%d: plan %+v, actual weights=%d rows=%d sums=%d",
						filter, dims, factor, a, cap(r.xWeights), r.ringRows, 8*len(r.sums))
				}
			}
		}
	}
}

func TestMemorySourcePlanes(t *testing.T) {
	for name, sampling := range map[string][][2]int{
		"gray":    {{1, 1}},
		"444":     {{1, 1}, {1, 1}, {1, 1}},
		"440":     {{1, 2}, {1, 1}, {1, 1}},
		"422":     {{2, 1}, {1, 1}, {1, 1}},
		"420":     {{2, 2}, {1, 1}, {1, 1}},
		"411":     {{4, 1}, {1, 1}, {1, 1}},
		"410":     {{4, 2}, {1, 1}, {1, 1}},
		"flexUV":  {{2, 2}, {1, 2}, {2, 1}},
		"flexY":   {{1, 1}, {2, 2}, {1, 1}},
		"flexH":   {{3, 1}, {1, 1}, {1, 1}},
		"cmyk":    {{1, 1}, {1, 1}, {1, 1}, {1, 1}},
		"cmyk420": {{2, 2}, {1, 1}, {1, 1}, {2, 2}},
	} {
		for _, factor := range []int{1, 2, 4, 8} {
			t.Run(fmt.Sprintf("%s/factor%d", name, factor), func(t *testing.T) {
				d := memoryPlanDecoder(17, 19, factor, sampling)
				mxx, myy := memoryPlanMCUs(d)
				for _, rows := range []int{1, myy} {
					var got, want memoryPlan
					if err := got.sourcePlanes(d, mxx, rows, 3); err != nil {
						t.Fatal(err)
					}
					if err := d.makeImg(mxx, rows); err != nil {
						t.Fatal(err)
					}
					n := 0
					if d.img1 != nil {
						n = len(d.img1.Pix)
					} else {
						n = len(d.img3.Y) + len(d.img3.Cb) + len(d.img3.Cr)
					}
					if err := want.allocation(3, uint64(n)); err != nil {
						t.Fatal(err)
					}
					if err := want.allocation(3, uint64(len(d.blackPix))); err != nil {
						t.Fatal(err)
					}
					if got != want {
						t.Fatalf("MCU rows=%d: planned %d, actual backing arrays with allowance %d",
							rows, got.bytes, want.bytes)
					}
				}
			})
		}
	}
}

func TestMemoryEstimatePaths(t *testing.T) {
	// The largest legal JPEG is safe to plan without allocating its pixels or
	// coefficients, including on 32-bit systems at this reduced scale.
	d := memoryPlanDecoder(65535, 65535, 8, [][2]int{{2, 2}, {1, 1}, {1, 1}})
	r := &resizeRequest{width: 64, height: 64, thumbnail: true, opts: ResizeOptions{Workers: 2}}
	mxx, myy := memoryPlanMCUs(d)
	stream, err := r.memoryEstimate(d, mxx, myy)
	if err != nil {
		t.Fatal(err)
	}
	d.progressive, r.fallback, r.deferred = true, true, true
	deferred, err := r.memoryEstimate(d, mxx, myy)
	if err != nil {
		t.Fatal(err)
	}
	var progressive memoryPlan
	for ci := 0; ci < d.nComp; ci++ {
		blocks := uint64(mxx) * uint64(myy) * uint64(d.comp[ci].h*d.comp[ci].v)
		if err := progressive.allocation(1, blocks, 4); err != nil {
			t.Fatal(err)
		}
		if err := progressive.allocation(1, blocks, 8); err != nil {
			t.Fatal(err)
		}
	}
	if deferred-stream != int64(progressive.bytes) {
		t.Fatalf("progressive state: planned delta %d, want %d", deferred-stream, progressive.bytes)
	}
	r.deferred = false
	full, err := r.memoryEstimate(d, mxx, myy)
	if err != nil {
		t.Fatal(err)
	}
	if full <= deferred {
		t.Fatalf("full fallback %d does not include its full source images (deferred %d)", full, deferred)
	}
	// Neither currently transmitted components nor current APP14 metadata can
	// reduce the up-front reservation.
	d.progCoeffs[0] = []int32{1}
	d.adobeTransformValid, d.adobeTransform = true, adobeTransformUnknown
	rgb, err := r.memoryEstimate(d, mxx, myy)
	if err != nil || rgb != full {
		t.Fatalf("late RGB/current coefficients changed plan: got %d, %v; want %d", rgb, err, full)
	}
}

func TestMemoryEstimateStreamingBounds(t *testing.T) {
	sampling := [][2]int{{2, 2}, {1, 1}, {1, 1}}
	r := &resizeRequest{width: 64, height: 64, opts: ResizeOptions{Workers: 2}}
	var first int64
	for _, height := range []int{4096, 65535} {
		d := memoryPlanDecoder(4096, height, 1, sampling)
		mxx, myy := memoryPlanMCUs(d)
		got, err := r.memoryEstimate(d, mxx, myy)
		if err != nil {
			t.Fatal(err)
		}
		if first != 0 && got != first {
			t.Fatalf("streaming plan grows with source height: %d versus %d", got, first)
		}
		first = got
	}
	d := memoryPlanDecoder(4096, 1, 1, sampling)
	mxx, myy := memoryPlanMCUs(d)
	r.height = 1
	r.opts.Workers = 1
	one, err := r.memoryEstimate(d, mxx, myy)
	if err != nil {
		t.Fatal(err)
	}
	r.opts.Workers = 64
	many, err := r.memoryEstimate(d, mxx, myy)
	if err != nil || many != one {
		t.Fatalf("one MCU row worker cap: got %d, %v; want %d", many, err, one)
	}
}

func TestMemoryEstimateNoAllocations(t *testing.T) {
	d := memoryPlanDecoder(65535, 65535, 8, [][2]int{{2, 2}, {1, 1}, {1, 1}})
	d.progressive = true
	r := &resizeRequest{
		width: 64, height: 64, thumbnail: true, fallback: true, deferred: true,
		opts: ResizeOptions{Workers: 64},
	}
	mxx, myy := memoryPlanMCUs(d)
	if _, err := r.memoryEstimate(d, mxx, myy); err != nil {
		t.Fatal(err)
	}
	if n := testing.AllocsPerRun(10, func() {
		if _, err := r.memoryEstimate(d, mxx, myy); err != nil {
			panic(err)
		}
	}); n != 0 {
		t.Fatalf("planning allocated %g times", n)
	}
	if d.img1 != nil || d.img3 != nil || d.progCoeffs[0] != nil || r.filter != nil || r.pipeline != nil {
		t.Fatal("planning allocated or installed decode buffers")
	}
}

func TestMemoryPlanOverflow(t *testing.T) {
	maxInt := uint64(^uint(0) >> 1)
	if _, err := memorySize(maxInt, 2); err == nil {
		t.Fatal("accepted overflowing slice byte size")
	}
	p := memoryPlan{bytes: maxMemoryEstimate - 3}
	if err := p.add(4); err == nil || p.bytes != maxMemoryEstimate-3 {
		t.Fatal("accepted overflowing total")
	}
	p = memoryPlan{}
	if err := p.allocation(2, maxInt); err == nil && maxInt == maxMemoryEstimate {
		t.Fatal("accepted overflowing repeated-allocation total")
	}
	// A sum larger than maxInt is valid on 32-bit when every individual
	// backing array fits. No allocation is actually performed here.
	p = memoryPlan{}
	if err := p.allocation(3, 1<<30); err != nil || p.bytes <= 1<<31 {
		t.Fatalf("rejected valid int64 total: %d, %v", p.bytes, err)
	}
	d := memoryPlanDecoder(int(maxInt), 17, 1, [][2]int{{1, 1}})
	r := &resizeRequest{width: 1, height: 1, opts: ResizeOptions{Workers: 1}}
	mxx, myy := memoryPlanMCUs(d)
	if _, err := r.memoryEstimate(d, mxx, myy); err == nil {
		t.Fatal("accepted source-row/filter-table allocation overflow")
	}
}

func TestMemoryAllocationRounding(t *testing.T) {
	// These Go 1.27 size-class jumps exceed a simple 12.5% allowance.
	// The reservation must cover them without relying on its fixed overhead.
	for _, tc := range []struct{ payload, class uint64 }{
		{4097, 4864}, {8193, 9472}, {28673, 32768},
		{32768, 32768}, {32769, 40960},
	} {
		var p memoryPlan
		if err := p.allocation(1, tc.payload); err != nil {
			t.Fatal(err)
		}
		if p.bytes < tc.class {
			t.Errorf("payload %d: reservation %d does not cover class %d", tc.payload, p.bytes, tc.class)
		}
	}
	var previous uint64
	for n := uint64(1); n <= 64<<10; n++ {
		var p memoryPlan
		if err := p.allocation(1, n); err != nil {
			t.Fatal(err)
		}
		if p.bytes < n || p.bytes < previous {
			t.Fatalf("non-conservative or decreasing reservation at %d: %d, previous %d", n, p.bytes, previous)
		}
		previous = p.bytes
	}
}
