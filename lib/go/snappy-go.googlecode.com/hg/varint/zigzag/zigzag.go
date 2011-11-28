// Copyright 2011 The Snappy-Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package zigzag implements the zigzag mapping between signed and unsigned
// integers:
//	+0 <-> 0
//	-1 <-> 1
//	+1 <-> 2
//	-2 <-> 3
//	+2 <-> 4
//	etcetera
//
// It is the same format used by protocol buffers. The format is described at
// http://code.google.com/apis/protocolbuffers/docs/encoding.html
package zigzag

// Itou64 maps a signed integer to an unsigned integer.
// If i >= 0, the result is 2*i.
// If i < 0, the result is -2*i - 1.
// The formulae above are in terms of ideal integers, with no overflow.
func Itou64(i int64) uint64 {
	return uint64(i<<1 ^ i>>63)
}

// Utoi64 maps an unsigned integer to a signed integer.
// If u%2 == 0, the result is u/2.
// If u%2 == 1, the result is -(u+1)/2.
// The formulae above are in terms of ideal integers, with no overflow.
func Utoi64(u uint64) int64 {
	return int64(u>>1) ^ -int64(u&1)
}
