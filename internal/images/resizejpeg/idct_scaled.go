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

// scaledIDCT replaces dequantized JPEG coefficients with signed spatial
// samples. factor must be 1, 2, 4, or 8. The n*n output samples, n = 8/factor,
// are packed in b[:n*n], rather than retaining the input's eight-element stride.
// Level shifting and clipping to bytes are the caller's responsibility.
//
// For factor > 1 this is a reduced-frequency IDCT: only coefficients b[v*8+u]
// with u,v < n contribute. In exact arithmetic its output is
//
//	(1/8) Σ_v Σ_u alpha(u) alpha(v) b[v*8+u] *
//	    cos((2*x+1)*u*pi/(2*n)) cos((2*y+1)*v*pi/(2*n)),
//
// where alpha(0) = 1 and alpha(k) = sqrt(2) otherwise. Keeping the JPEG
// normalization, rather than the normalization of an independent n-point DCT,
// makes a DC coefficient produce b[0]/8 at every scale.
//
// Discarding high frequencies provides a low-pass reduction, not a box average
// of full-resolution samples: the retained cosine modes are evaluated on the
// smaller grid. In particular, neither clipping full-resolution pixels before
// averaging nor averaging their unrounded values is equivalent to this IDCT.
// The same frequency selection must be used for sequential and progressive
// JPEGs. No full-resolution spatial block or heap allocation is needed.
func scaledIDCT(b *block, factor int) {
	switch factor {
	case 1:
		idct(b)
	case 2:
		scaledIDCT4(b)
	case 4:
		// The two-point basis is exactly [1, 1], [1, -1].
		// Use int64 before adding so large dequantized inputs cannot
		// overflow an intermediate int32.
		a, c := int64(b[0])+int64(b[1]), int64(b[0])-int64(b[1])
		d, e := int64(b[8])+int64(b[9]), int64(b[8])-int64(b[9])
		b[0] = int32((a + d + 4) >> 3)
		b[1] = int32((c + e + 4) >> 3)
		b[2] = int32((a - d + 4) >> 3)
		b[3] = int32((c - e + 4) >> 3)
	case 8:
		b[0] = int32((int64(b[0]) + 4) >> 3)
	default:
		panic("resizejpeg: invalid IDCT scale factor")
	}
}

// scaledIDCT4 computes the four-point transform in each dimension. A 1D
// transform has the even terms x0 +/- x2 and the odd terms
//
//	a*x1 + c*x3, c*x1 - a*x3,
//
// where a = sqrt(2)*cos(pi/8) and c = sqrt(2)*cos(3*pi/8). A shared product
// reduces these rotations to three integer multiplies per 1D transform.
//
// The constants have 18 fractional bits. After the row pass we retain eight
// fractional bits, so the column pass is rounded and divided by eight with
// a shift of 18+8+3. The row rounding error is at most 1/512 before the
// column transform. int64 intermediates avoid overflow even with int32 input
// coefficients: a 1D basis row's absolute sum is less than four, so the
// second pass is bounded by 2^31 * 4^2 * 2^(18+8) < 2^62.
func scaledIDCT4(b *block) {
	var rows [16]int64
	for y := 0; y < 4; y++ {
		i := y * 8
		a, c, d, e := scaledIDCT4Pass(
			int64(b[i]), int64(b[i+1]), int64(b[i+2]), int64(b[i+3]))
		j := y * 4
		rows[j] = (a + 1<<9) >> 10
		rows[j+1] = (c + 1<<9) >> 10
		rows[j+2] = (d + 1<<9) >> 10
		rows[j+3] = (e + 1<<9) >> 10
	}
	for x := 0; x < 4; x++ {
		a, c, d, e := scaledIDCT4Pass(
			rows[x], rows[x+4], rows[x+8], rows[x+12])
		b[x] = int32((a + 1<<28) >> 29)
		b[x+4] = int32((c + 1<<28) >> 29)
		b[x+8] = int32((d + 1<<28) >> 29)
		b[x+12] = int32((e + 1<<28) >> 29)
	}
}

// scaledIDCT4Pass returns an unnormalized four-point transform with 18 extra
// fractional bits. Its coefficients are rounded once from the exact cosines;
// all code and the factorization here are independent of external JPEG code.
func scaledIDCT4Pass(x0, x1, x2, x3 int64) (y0, y1, y2, y3 int64) {
	const (
		a = 342508 // round(sqrt(2) * cos(pi/8) * 2^18)
		c = 141871 // round(sqrt(2) * cos(3*pi/8) * 2^18)
	)
	even0, even1 := (x0+x2)<<18, (x0-x2)<<18
	t := c * (x1 + x3)
	odd0, odd1 := t+(a-c)*x1, t-(a+c)*x3
	return even0 + odd0, even1 + odd1, even1 - odd1, even0 - odd0
}
