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

// Filter selects the final, separable RGB reconstruction filter used by
// DecodeResize and DecodeThumbnail. Each filter widens its footprint when
// shrinking, normalizes truncated image edges, and filters encoded RGB rather
// than linear-light values.
type Filter int

const (
	// Lanczos3 is the default windowed-sinc filter, with support radius three.
	// It preserves detail but can ring around sharp edges.
	Lanczos3 Filter = iota
	// CatmullRom is a cubic filter with support radius two. It uses fewer
	// source samples than Lanczos3, but can also ring around sharp edges.
	CatmullRom
	// Bilinear is a tent filter with support radius one. It uses the fewest
	// source samples and produces softer results without introducing
	// filter ringing.
	Bilinear
)

func (f Filter) valid() bool {
	return f == Lanczos3 || f == CatmullRom || f == Bilinear
}

// String returns the filter's name, or "unknown" for an invalid filter.
func (f Filter) String() string {
	switch f {
	case Lanczos3:
		return "Lanczos3"
	case CatmullRom:
		return "CatmullRom"
	case Bilinear:
		return "Bilinear"
	}
	return "unknown"
}

// kernel returns immutable reconstruction parameters. Callers validate f first.
func (f Filter) kernel() (support int, at func(float64) float64) {
	switch f {
	case CatmullRom:
		return 2, catmullRomKernel
	case Bilinear:
		return 1, bilinearKernel
	default:
		return 3, lanczosKernel
	}
}

func bilinearKernel(distance float64) float64 {
	if distance >= 1 {
		return 0
	}
	return 1 - distance
}

func catmullRomKernel(distance float64) float64 {
	if distance >= 2 {
		return 0
	}
	// Match x/image/draw's evaluation order. Explicit float64 conversions
	// prevent fused operations from changing its rounding on other CPUs.
	if distance < 1 {
		return float64((float64(1.5*distance)-2.5)*distance*distance) + 1
	}
	return float64((float64(float64(float64(-0.5*distance)+2.5)*distance)-4)*distance) + 2
}
