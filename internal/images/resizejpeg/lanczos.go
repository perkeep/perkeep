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
	"image"
	"math"
	"unsafe"
)

// lanczosSpan describes the contributing source samples for one output sample.
// The half-open bounds omit samples outside the kernel support. Zero weights
// inside that support are retained so span endpoints remain monotonic, even
// for Catmull-Rom's interior zeros. Samples outside the image are discarded,
// and invWeight renormalizes the remaining samples.
type lanczosSpan struct {
	first, last       int
	center, invWeight float64
}

// lanczosResizer filters opaque, clipped RGB samples in sRGB-coded space.
//
// Horizontal filtering is independent for each source row and only reads
// immutable fields, so workers may call horizontal concurrently. An ordered
// collector calls addRows. Instead of retaining horizontally filtered source
// rows, it distributes each row into the currently active destination rows.
// Downsampling needs at most a small constant number of these accumulation
// rows, even when each output row has thousands of contributing source rows.
type lanczosResizer struct {
	sw, sh, dw, dh int
	dst            *image.RGBA

	x, y     []lanczosSpan
	xWeights []float64
	yScale   float64
	kernel   func(float64) float64

	sums                  []float64
	ringRows              int
	nextSourceY           int
	firstActive, nextOpen int
}

// lanczosSize checks both multiplication and the allocation's byte size.
// Like checkAllocationSize, this is an overflow check, not a resource budget.
func lanczosSize(factors ...int) (int, error) {
	size := uint64(1)
	limit := uint64(^uint(0) >> 1)
	for _, f := range factors {
		if f < 0 || (f != 0 && size > limit/uint64(f)) {
			return 0, UnsupportedError("image is too large")
		}
		size *= uint64(f)
	}
	if err := checkAllocationSize(size); err != nil {
		return 0, err
	}
	return int(size), nil
}

func lanczosKernel(distance float64) float64 {
	if distance >= 3 {
		return 0
	}
	if distance == 0 {
		return 1
	}
	x := distance * math.Pi
	return math.Sin(x) * math.Sin(x/3) / (x * x / 3)
}

// filterBounds uses pixel centers, not pixel corners. Its mapping, widened
// support on reduction, and truncated edge normalization agree with
// golang.org/x/image/draw's Kernel scaler (scale.go, newDistrib).
// Clamp in floating point before converting to int: the unbounded support can
// extend past maxInt even when the source row itself has a valid byte size.
//
// extent is the original source length divided by its IDCT reduction factor.
// It may be fractional: the final reduced sample of an odd-sized JPEG covers
// some padded pixels. Mapping the rounded-up sample count as if it were the
// original extent would shift the sampling phase across the entire image.
func filterBounds(src int, extent float64, dst, pos, support int) (first, last int, center, argScale float64) {
	scale := extent / float64(dst)
	halfWidth := float64(support)
	argScale = 1
	if scale > 1 {
		halfWidth *= scale
		argScale = 1 / scale
	}
	center = float64((float64(pos)+0.5)*scale) - 0.5
	left := math.Floor(center - halfWidth)
	right := math.Ceil(center + halfWidth)
	first, last = 0, src
	if left > 0 {
		if left >= float64(src) {
			first = src
		} else {
			first = int(left)
		}
	}
	if right < float64(src) {
		if right <= 0 {
			last = 0
		} else {
			last = int(right)
		}
	}
	return
}

func filterSpans(src int, extent float64, dst, support int, kernel func(float64) float64) []lanczosSpan {
	spans := make([]lanczosSpan, dst)
	for d := range spans {
		first, last, center, argScale := filterBounds(src, extent, dst, d, support)
		sum := 0.0
		lo, hi := last, first
		for s := first; s < last; s++ {
			distance := math.Abs((center - float64(s)) * argScale)
			if distance < float64(support) {
				lo = min(lo, s)
				hi = s + 1
				sum += kernel(distance)
			}
		}
		spans[d] = lanczosSpan{lo, hi, center, 1 / sum}
	}
	return spans
}

func newLanczosResizer(sw, sh, dw, dh int) (*lanczosResizer, error) {
	return newFilteredResizer(sw, sh, dw, dh, Lanczos3)
}

func newFilteredResizer(sw, sh, dw, dh int, filter Filter) (*lanczosResizer, error) {
	return newFilteredResizerExtent(sw, sh, dw, dh, float64(sw), float64(sh), filter)
}

// newScaledLanczosResizer consumes the ceil-rounded reduced-IDCT sample grid
// while preserving the original image's sampling coordinates. Factor 1 is
// exactly the existing full-resolution resizer.
func newScaledLanczosResizer(originalWidth, originalHeight, factor, dw, dh int) (*lanczosResizer, error) {
	return newScaledFilteredResizer(originalWidth, originalHeight, factor, dw, dh, Lanczos3)
}

func newScaledFilteredResizer(originalWidth, originalHeight, factor, dw, dh int, filter Filter) (*lanczosResizer, error) {
	switch factor {
	case 1, 2, 4, 8:
	default:
		return nil, UnsupportedError("invalid resize IDCT factor")
	}
	if originalWidth <= 0 || originalHeight <= 0 {
		return nil, UnsupportedError("invalid resize dimensions")
	}
	// Quotient/remainder avoids overflowing original+factor-1 on 32-bit
	// platforms or when the constructor is called directly by tests.
	sw, sh := originalWidth/factor, originalHeight/factor
	if originalWidth%factor != 0 {
		sw++
	}
	if originalHeight%factor != 0 {
		sh++
	}
	return newFilteredResizerExtent(sw, sh, dw, dh,
		float64(originalWidth)/float64(factor), float64(originalHeight)/float64(factor), filter)
}

// filterAllocation describes the constructor's bulk backing arrays without
// computing any spans or weights. Memory admission uses this same geometry so
// changes to the constructor's conservative capacities cannot drift from it.
type filterAllocation struct {
	weightCount, ringRows, sumBytes int
}

func filterAllocationGeometry(sw, sh, dw, dh int, extentY float64, filter Filter) (filterAllocation, error) {
	if !filter.valid() {
		return filterAllocation{}, UnsupportedError("invalid resize filter")
	}
	if sw <= 0 || sh <= 0 || dw <= 0 || dh <= 0 {
		return filterAllocation{}, UnsupportedError("invalid resize dimensions")
	}
	support, _ := filter.kernel()
	// Check all known allocation sizes before allocating anything. Source
	// height does not participate in a pixel or floating-point allocation.
	for _, factors := range [][]int{
		{sw, 4},
		{dw, dh, 4},
		{dw, 3, 8},
		{dw, int(unsafe.Sizeof(lanczosSpan{}))},
		{dh, int(unsafe.Sizeof(lanczosSpan{}))},
	} {
		if _, err := lanczosSize(factors...); err != nil {
			return filterAllocation{}, err
		}
	}
	// Bound horizontal storage without first iterating over potentially huge
	// dimensions. Each span contains at most its support width plus two
	// boundary samples; alternatively, it cannot exceed the whole source.
	// The source-row and span-size checks above make this sum fit uint64.
	taps := uint64(2*support)*uint64(max(sw, dw)) + 2*uint64(dw)
	if uint64(sw) <= taps/uint64(dw) {
		taps = uint64(sw) * uint64(dw)
	}
	if taps > uint64(^uint(0)>>1)/8 {
		return filterAllocation{}, UnsupportedError("image is too large")
	}
	if err := checkAllocationSize(taps * 8); err != nil {
		return filterAllocation{}, err
	}

	// An output support interval is 2*support scale units wide on reduction,
	// or 2*support pixels wide on enlargement. Thus an input row can overlap
	// at most 2*support output rows when shrinking; allow one spare slot for
	// boundary arithmetic. Enlargement scales the bound by dh/extentY,
	// including fractional extents smaller than one. Clamp before conversion.
	rowBound := float64(2*support + 1)
	if float64(dh) > extentY {
		rowBound = math.Ceil(float64(2*support)*(float64(dh)/extentY)) + 1
	}
	ringRows := dh
	if rowBound < float64(dh) {
		ringRows = int(rowBound)
	}
	sumBytes, err := lanczosSize(ringRows, dw, 3, 8)
	if err != nil {
		return filterAllocation{}, err
	}
	return filterAllocation{int(taps), ringRows, sumBytes}, nil
}

func newFilteredResizerExtent(sw, sh, dw, dh int, extentX, extentY float64, filter Filter) (*lanczosResizer, error) {
	a, err := filterAllocationGeometry(sw, sh, dw, dh, extentY, filter)
	if err != nil {
		return nil, err
	}
	support, kernel := filter.kernel()

	r := &lanczosResizer{
		sw: sw, sh: sh, dw: dw, dh: dh,
		x:        filterSpans(sw, extentX, dw, support, kernel),
		y:        filterSpans(sh, extentY, dh, support, kernel),
		kernel:   kernel,
		ringRows: a.ringRows,
	}
	r.sums = make([]float64, a.sumBytes/8)
	r.xWeights = make([]float64, 0, a.weightCount)
	_, _, _, argScale := filterBounds(sw, extentX, dw, 0, support)
	for _, span := range r.x {
		for s := span.first; s < span.last; s++ {
			r.xWeights = append(r.xWeights,
				kernel(math.Abs((span.center-float64(s))*argScale)))
		}
	}
	_, _, _, r.yScale = filterBounds(sh, extentY, dh, 0, support)
	r.dst = image.NewRGBA(image.Rect(0, 0, dw, dh))
	return r, nil
}

// horizontal writes one packed RGB row (3*dw float64s) from an opaque RGBA row
// (4*sw bytes). Neither clamping nor rounding is performed between passes.
// Values are normalized to [0,1] before ringing, so may be outside that range.
// The caller supplies sufficiently long slices, owned exclusively by this call.
func (r *lanczosResizer) horizontal(dst []float64, src []byte) {
	offset := 0
	for x, span := range r.x {
		var red, green, blue float64
		for s := span.first; s < span.last; s++ {
			weight := r.xWeights[offset]
			offset++
			// Use the reference scaler's 16-bit sample arithmetic and
			// normalization order to avoid extra quantization differences.
			red += float64(float64(uint32(src[4*s])*257) * weight)
			green += float64(float64(uint32(src[4*s+1])*257) * weight)
			blue += float64(float64(uint32(src[4*s+2])*257) * weight)
		}
		norm := span.invWeight / 65535
		dst[3*x] = red * norm
		dst[3*x+1] = green * norm
		dst[3*x+2] = blue * norm
	}
}

// addRows consumes n consecutive, horizontally filtered source rows starting at
// y. It does not retain rows. Invalid calls leave the resizer unchanged.
func (r *lanczosResizer) addRows(rows []float64, y, n int) error {
	if y < 0 || y > r.sh || n < 0 || n > r.sh-y || y != r.nextSourceY {
		return FormatError("invalid resize row order or bounds")
	}
	stride := 3 * r.dw // Checked by the constructor.
	if n > len(rows)/stride {
		return FormatError("short resize rows")
	}
	for sourceY := y; sourceY < y+n; sourceY++ {
		for r.nextOpen < r.dh && r.y[r.nextOpen].first <= sourceY {
			base := (r.nextOpen % r.ringRows) * stride
			clear(r.sums[base : base+stride])
			r.nextOpen++
		}
		src := rows[(sourceY-y)*stride : (sourceY-y+1)*stride]
		for d := r.firstActive; d < r.nextOpen; d++ {
			span := r.y[d]
			weight := r.kernel(math.Abs((span.center - float64(sourceY)) * r.yScale))
			if weight == 0 {
				continue
			}
			base := (d % r.ringRows) * stride
			sum := r.sums[base : base+stride]
			for i, value := range src {
				sum[i] += float64(value * weight)
			}
		}
		for r.firstActive < r.nextOpen && r.y[r.firstActive].last == sourceY+1 {
			r.writeRow(r.firstActive)
			r.firstActive++
		}
		r.nextSourceY++
	}
	return nil
}

func (r *lanczosResizer) writeRow(y int) {
	stride := 3 * r.dw
	base := (y % r.ringRows) * stride
	sum := r.sums[base : base+stride]
	dst := r.dst.Pix[y*r.dst.Stride : (y+1)*r.dst.Stride]
	norm := r.y[y].invWeight
	for x := 0; x < r.dw; x++ {
		for c := 0; c < 3; c++ {
			// x/image/draw rounds to 16 bits and then takes the high byte;
			// rounding directly to 8 bits has different thresholds.
			value := float64(65535*(sum[3*x+c]*norm)) + 0.5
			if value >= 65535 {
				dst[4*x+c] = 255
			} else if value > 0 {
				dst[4*x+c] = uint8(uint16(value) >> 8)
			} else {
				dst[4*x+c] = 0
			}
		}
		dst[4*x+3] = 255
	}
}

func (r *lanczosResizer) finish() error {
	if r.nextSourceY != r.sh || r.firstActive != r.dh {
		return FormatError("incomplete resize rows")
	}
	return nil
}
