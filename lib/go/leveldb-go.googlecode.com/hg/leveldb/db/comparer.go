// Copyright 2011 The LevelDB-Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package db

import (
	"bytes"
)

// Comparer defines a total ordering over the space of []byte keys: a 'less
// than' relationship.
type Comparer interface {
	// Compare returns -1, 0, or +1 depending on whether a is 'less than',
	// 'equal to' or 'greater than' b. The two arguments can only be 'equal'
	// if their contents are exactly equal. Furthermore, the empty slice
	// must be 'less than' any non-empty slice.
	Compare(a, b []byte) int

	// AppendSeparator appends a sequence of bytes x to dst such that
	// a <= x && x < b, where 'less than' is consistent with Compare.
	// It returns the enlarged slice, like the built-in append function.
	//
	// Precondition: either a is 'less than' b, or b is an empty slice.
	// In the latter case, empty means 'positive infinity', and appending any
	// x such that a <= x will be valid.
	//
	// An implementation may simply be "return append(dst, a...)" but appending
	// fewer bytes will result in smaller tables.
	//
	// For example, if dst, a and b are the []byte equivalents of the strings
	// "aqua", "black" and "blue", then the result may be "aquablb".
	// Similarly, if the arguments were "aqua", "green" and "", then the result
	// may be "aquah".
	AppendSeparator(dst, a, b []byte) []byte
}

// DefaultComparer is the default implementation of the Comparer interface.
// It uses the natural ordering, consistent with bytes.Compare.
var DefaultComparer Comparer = defCmp{}

type defCmp struct{}

func (defCmp) Compare(a, b []byte) int {
	return bytes.Compare(a, b)
}

func (defCmp) AppendSeparator(dst, a, b []byte) []byte {
	i, n := SharedPrefixLen(a, b), len(dst)
	dst = append(dst, a...)
	if len(b) > 0 {
		if i == len(a) {
			return dst
		}
		if i == len(b) {
			panic("a < b is a precondition, but b is a prefix of a")
		}
		if a[i] == 0xff || a[i]+1 >= b[i] {
			// This isn't optimal, but it matches the C++ Level-DB implementation, and
			// it's good enough. For example, if a is "1357" and b is "2", then the
			// optimal (i.e. shortest) result is appending "14", but we append "1357".
			return dst
		}
	}
	i += n
	for ; i < len(dst); i++ {
		if dst[i] != 0xff {
			dst[i]++
			return dst[:i+1]
		}
	}
	return dst
}

// SharedPrefixLen returns the largest i such that a[:i] equals b[:i].
// This function can be useful in implementing the Comparer interface.
func SharedPrefixLen(a, b []byte) int {
	i, n := 0, len(a)
	if n > len(b) {
		n = len(b)
	}
	for i < n && a[i] == b[i] {
		i++
	}
	return i
}
