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

import mathbits "math/bits"

// Progressive AC refinement codes depend on which coefficients are already
// nonzero, including coefficients outside the reduced IDCT's frequency window.
// Discarding those coefficients without retaining their nonzero flags would
// desynchronize Huffman decoding in subsequent scans.
//
// Keep the low-frequency values plus one bit per coefficient. When decoding
// a scan, expand discarded nonzero coefficients to positive placeholders in
// a single scratch block. Refinement only needs their zero/nonzero state to
// consume the right bits; their magnitude and sign do not affect other values.
// The placeholders remain nonzero after refinement and are never reconstructed.
func (d *decoder) loadProgressiveBlock(b *block, compIndex, index int) uint64 {
	bs := 8 / d.factor
	offset := index * bs * bs
	src := d.progCoeffs[compIndex][offset : offset+bs*bs]
	if bs == 8 {
		copy(b[:], src)
		return 0
	}
	*b = block{}
	mask := d.progNonzero[compIndex][index]
	for remaining := mask; remaining != 0; remaining &= remaining - 1 {
		b[mathbits.TrailingZeros64(remaining)] = 1
	}
	for y := 0; y < bs; y++ {
		copy(b[y*8:y*8+bs], src[y*bs:(y+1)*bs])
	}
	return mask
}

// The entropy decoder sets mask bits as it encounters new nonzero AC values.
// Such values stay nonzero during refinement. Retained coefficients are loaded
// from their actual values, so their mask bits (including DC) are immaterial.
// Updating flags at the AC writes avoids rescanning all 64 coefficients after
// every progressive scan.
func (d *decoder) saveProgressiveBlock(b *block, compIndex, index int, mask uint64) {
	bs := 8 / d.factor
	offset := index * bs * bs
	dst := d.progCoeffs[compIndex][offset : offset+bs*bs]
	if bs == 8 {
		copy(dst, b[:])
		return
	}
	for y := 0; y < bs; y++ {
		copy(dst[y*bs:(y+1)*bs], b[y*8:y*8+bs])
	}
	d.progNonzero[compIndex][index] = mask
}
