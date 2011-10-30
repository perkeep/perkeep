// Copyright 2011 The Snappy-Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package varint implements a variable-width encoding of unsigned integers.
//
// It is the same format used by protocol buffers. The format is described at
// http://code.google.com/apis/protocolbuffers/docs/encoding.html
package varint

// MaxLen is the maximum encoded length of a uint64.
const MaxLen = 10

// Len returns the number of bytes used to represent v.
func Len(v uint64) (n int) {
	for v > 0x7f {
		v >>= 7
		n++
	}
	return n + 1
}

// Decode returns the value encoded at the start of src, as well as the number
// of bytes it occupies. It returns n == 0 if given invalid input.
func Decode(src []byte) (v uint64, n int) {
	for shift := uint(0); ; shift += 7 {
		if n >= len(src) {
			return 0, 0
		}
		b := src[n]
		n++
		v |= uint64(b&0x7f) << shift
		if b&0x80 == 0 {
			break
		}
	}
	return v, n
}

// Encode writes the value to the start of dst, and returns the number of bytes
// written. It panics if len(dst) < Len(v).
func Encode(dst []byte, v uint64) (n int) {
	for v > 0x7f {
		dst[n] = 0x80 | uint8(v&0x7f)
		v >>= 7
		n++
	}
	dst[n] = uint8(v)
	return n + 1
}
