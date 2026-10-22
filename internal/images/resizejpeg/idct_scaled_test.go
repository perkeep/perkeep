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
	"math"
	"math/rand"
	"testing"
)

// referenceScaledIDCT directly evaluates the two-dimensional cosine sum. It
// does not use the implementation's separable passes, fixed-point constants,
// or butterfly factorization.
func referenceScaledIDCT(b block, factor int) []float64 {
	n := 8 / factor
	dst := make([]float64, n*n)
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			for v := 0; v < n; v++ {
				cv := math.Cos(float64((2*y+1)*v) * math.Pi / float64(2*n))
				if v != 0 {
					cv *= math.Sqrt2
				}
				for u := 0; u < n; u++ {
					cu := math.Cos(float64((2*x+1)*u) * math.Pi / float64(2*n))
					if u != 0 {
						cu *= math.Sqrt2
					}
					dst[y*n+x] += float64(b[v*8+u]) * cu * cv / 8
				}
			}
		}
	}
	return dst
}

func TestScaledIDCTReference(t *testing.T) {
	rng := rand.New(rand.NewSource(27491))
	blocks := []block{{}, {8}, {-8}}
	// Every retained or discarded mode, of both signs, catches frequency
	// selection, transposition, packing, and odd/even basis sign errors.
	for i := 0; i < 64; i++ {
		for _, amplitude := range []int32{-1024, -1, 1, 1024} {
			var b block
			b[i] = amplitude
			blocks = append(blocks, b)
		}
	}
	for i := 0; i < 200; i++ {
		var b block
		for j := range b {
			// Include both dense and sparse signed coefficient blocks.
			if i%2 == 0 || rng.Intn(4) == 0 {
				b[j] = int32(rng.Intn(4097) - 2048)
			}
		}
		blocks = append(blocks, b)
	}
	for _, factor := range []int{2, 4, 8} {
		t.Run(fmt.Sprint(factor), func(t *testing.T) {
			for i, src := range blocks {
				want := referenceScaledIDCT(src, factor)
				got := src
				scaledIDCT(&got, factor)
				for j, w := range want {
					// Nearest-integer rounding contributes at most 0.5;
					// Q18 constants and row rounding add <0.02 here.
					if delta := math.Abs(float64(got[j]) - w); delta > 0.52 {
						t.Fatalf("block %d sample %d: got %d, want %.9f (error %.9f)",
							i, j, got[j], w, delta)
					}
				}
			}
		})
	}
}

func TestScaledIDCTFullSize(t *testing.T) {
	rng := rand.New(rand.NewSource(653))
	for i := 0; i < 100; i++ {
		var b block
		for j := range b {
			b[j] = int32(rng.Intn(513) - 256)
		}
		want, got := b, b
		idct(&want)
		scaledIDCT(&got, 1)
		if got != want {
			t.Fatalf("block %d: factor 1 differs from idct", i)
		}
	}
}

func TestScaledIDCTDC(t *testing.T) {
	for _, factor := range []int{1, 2, 4, 8} {
		t.Run(fmt.Sprint(factor), func(t *testing.T) {
			for dc := int32(-2048); dc <= 2048; dc++ {
				src := block{dc}
				scaledIDCT(&src, factor)
				want := (dc + 4) >> 3 // Ties toward positive infinity.
				n := 8 / factor
				for i := 0; i < n*n; i++ {
					if src[i] != want {
						t.Fatalf("DC %d sample %d: got %d, want %d", dc, i, src[i], want)
					}
				}
			}
		})
	}
}

func TestScaledIDCTDiscardedFrequencies(t *testing.T) {
	rng := rand.New(rand.NewSource(482))
	for _, factor := range []int{2, 4, 8} {
		t.Run(fmt.Sprint(factor), func(t *testing.T) {
			n := 8 / factor
			for i := 0; i < 100; i++ {
				var retained, noisy block
				for v := 0; v < 8; v++ {
					for u := 0; u < 8; u++ {
						j := v*8 + u
						noisy[j] = int32(rng.Uint32())
						if v < n && u < n {
							retained[j] = int32(rng.Intn(4097) - 2048)
							noisy[j] = retained[j]
						}
					}
				}
				scaledIDCT(&retained, factor)
				scaledIDCT(&noisy, factor)
				for j := 0; j < n*n; j++ {
					if retained[j] != noisy[j] {
						t.Fatalf("block %d sample %d: discarded coefficients affected output", i, j)
					}
				}
			}
		})
	}
}

func TestScaledIDCTLargeCoefficients(t *testing.T) {
	// Coefficients beyond any ordinary 8-bit JPEG stress int64 intermediate
	// arithmetic. Account for constant error proportional to input amplitude.
	rng := rand.New(rand.NewSource(106))
	for _, factor := range []int{2, 4, 8} {
		for i := 0; i < 40; i++ {
			var src block
			for j := range src {
				src[j] = int32(rng.Intn(1<<28) - 1<<27)
			}
			want := referenceScaledIDCT(src, factor)
			got := src
			scaledIDCT(&got, factor)
			tolerance := 0.500001 // Allow floating-point error at exact half ties.
			if factor == 2 {
				tolerance = 1024
			}
			for j, w := range want {
				if delta := math.Abs(float64(got[j]) - w); delta > tolerance {
					t.Fatalf("factor %d block %d sample %d: got %d, want %.6f",
						factor, i, j, got[j], w)
				}
			}
		}
	}
}

func TestScaledIDCTNotBoxAverage(t *testing.T) {
	// A mode outside the retained 4x4 square contributes to a full-size
	// IDCT's box averages, but must be absent from the reduced-frequency IDCT.
	src := block{7: 1024}
	reduced := src
	scaledIDCT(&reduced, 2)
	for i := 0; i < 16; i++ {
		if reduced[i] != 0 {
			t.Fatalf("discarded mode sample %d: got %d", i, reduced[i])
		}
	}
	full := referenceScaledIDCT(src, 1)
	average := (full[0] + full[1] + full[8] + full[9]) / 4
	if math.Abs(average) < 1 {
		t.Fatalf("test mode does not distinguish box averaging: %.9f", average)
	}
}

func TestScaledIDCTAllocations(t *testing.T) {
	for _, factor := range []int{1, 2, 4, 8} {
		t.Run(fmt.Sprint(factor), func(t *testing.T) {
			b := block{100, -20, 30, -40, 0, 0, 0, 0, -80, 20}
			if got := testing.AllocsPerRun(100, func() {
				scaledIDCT(&b, factor)
			}); got != 0 {
				t.Fatalf("got %g allocations per block, want zero", got)
			}
		})
	}
}

func TestScaledIDCTInvalidFactor(t *testing.T) {
	for _, factor := range []int{-1, 0, 3, 16} {
		t.Run(fmt.Sprint(factor), func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatal("did not panic")
				}
			}()
			scaledIDCT(new(block), factor)
		})
	}
}

func BenchmarkScaledIDCT(b *testing.B) {
	for _, factor := range []int{1, 2, 4, 8} {
		b.Run(fmt.Sprint(factor), func(b *testing.B) {
			src := block{
				-250, -24, 18, 7, -3, 0, -1, 0,
				-31, 27, -12, 9, -4, 2, 0, 0,
				14, -16, 8, -5, 2, 0, 0, 0,
				-11, 7, -4, 3, 0, 0, 0, 0,
			}
			b.ReportAllocs()
			for b.Loop() {
				dst := src
				scaledIDCT(&dst, factor)
			}
		})
	}
}
