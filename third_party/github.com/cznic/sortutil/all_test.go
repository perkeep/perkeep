// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package sortutil

import (
	"sort"
	"testing"
)

func TestByteSlice(t *testing.T) {
	const N = 1e4
	s := make(ByteSlice, N)
	for i := range s {
		s[i] = byte(i) ^ 0x55
	}
	s.Sort()
	if !sort.IsSorted(s) {
		t.Fatal()
	}
}

func TestSearchBytes(t *testing.T) {
	const N = 1e1
	s := make(ByteSlice, N)
	for i := range s {
		s[i] = byte(2 * i)
	}
	if g, e := SearchBytes(s, 12), 6; g != e {
		t.Fatal(g, e)
	}
}

func TestFloat32Slice(t *testing.T) {
	const N = 1e4
	s := make(Float32Slice, N)
	for i := range s {
		s[i] = float32(i ^ 0x55aa55aa)
	}
	s.Sort()
	if !sort.IsSorted(s) {
		t.Fatal()
	}
}

func TestSearchFloat32s(t *testing.T) {
	const N = 1e4
	s := make(Float32Slice, N)
	for i := range s {
		s[i] = float32(2 * i)
	}
	if g, e := SearchFloat32s(s, 12), 6; g != e {
		t.Fatal(g, e)
	}
}

func TestInt8Slice(t *testing.T) {
	const N = 1e4
	s := make(Int8Slice, N)
	for i := range s {
		s[i] = int8(i) ^ 0x55
	}
	s.Sort()
	if !sort.IsSorted(s) {
		t.Fatal()
	}
}

func TestSearchInt8s(t *testing.T) {
	const N = 1e1
	s := make(Int8Slice, N)
	for i := range s {
		s[i] = int8(2 * i)
	}
	if g, e := SearchInt8s(s, 12), 6; g != e {
		t.Fatal(g, e)
	}
}

func TestInt16Slice(t *testing.T) {
	const N = 1e4
	s := make(Int16Slice, N)
	for i := range s {
		s[i] = int16(i) ^ 0x55aa
	}
	s.Sort()
	if !sort.IsSorted(s) {
		t.Fatal()
	}
}

func TestSearchInt16s(t *testing.T) {
	const N = 1e4
	s := make(Int16Slice, N)
	for i := range s {
		s[i] = int16(2 * i)
	}
	if g, e := SearchInt16s(s, 12), 6; g != e {
		t.Fatal(g, e)
	}
}

func TestInt32Slice(t *testing.T) {
	const N = 1e4
	s := make(Int32Slice, N)
	for i := range s {
		s[i] = int32(i) ^ 0x55aa55aa
	}
	s.Sort()
	if !sort.IsSorted(s) {
		t.Fatal()
	}
}

func TestSearchInt32s(t *testing.T) {
	const N = 1e4
	s := make(Int32Slice, N)
	for i := range s {
		s[i] = int32(2 * i)
	}
	if g, e := SearchInt32s(s, 12), 6; g != e {
		t.Fatal(g, e)
	}
}

func TestInt64Slice(t *testing.T) {
	const N = 1e4
	s := make(Int64Slice, N)
	for i := range s {
		s[i] = int64(i) ^ 0x55aa55aa55aa55aa
	}
	s.Sort()
	if !sort.IsSorted(s) {
		t.Fatal()
	}
}

func TestSearchInt64s(t *testing.T) {
	const N = 1e4
	s := make(Int64Slice, N)
	for i := range s {
		s[i] = int64(2 * i)
	}
	if g, e := SearchInt64s(s, 12), 6; g != e {
		t.Fatal(g, e)
	}
}

func TestUintSlice(t *testing.T) {
	const N = 1e4
	s := make(UintSlice, N)
	for i := range s {
		s[i] = uint(i) ^ 0x55aa55aa
	}
	s.Sort()
	if !sort.IsSorted(s) {
		t.Fatal()
	}
}

func TestSearchUints(t *testing.T) {
	const N = 1e4
	s := make(UintSlice, N)
	for i := range s {
		s[i] = uint(2 * i)
	}
	if g, e := SearchUints(s, 12), 6; g != e {
		t.Fatal(g, e)
	}
}

func TestUint16Slice(t *testing.T) {
	const N = 1e4
	s := make(Uint16Slice, N)
	for i := range s {
		s[i] = uint16(i) ^ 0x55aa
	}
	s.Sort()
	if !sort.IsSorted(s) {
		t.Fatal()
	}
}

func TestSearchUint16s(t *testing.T) {
	const N = 1e4
	s := make(Uint16Slice, N)
	for i := range s {
		s[i] = uint16(2 * i)
	}
	if g, e := SearchUint16s(s, 12), 6; g != e {
		t.Fatal(g, e)
	}
}

func TestUint32Slice(t *testing.T) {
	const N = 1e4
	s := make(Uint32Slice, N)
	for i := range s {
		s[i] = uint32(i) ^ 0x55aa55aa
	}
	s.Sort()
	if !sort.IsSorted(s) {
		t.Fatal()
	}
}

func TestSearchUint32s(t *testing.T) {
	const N = 1e4
	s := make(Uint32Slice, N)
	for i := range s {
		s[i] = uint32(2 * i)
	}
	if g, e := SearchUint32s(s, 12), 6; g != e {
		t.Fatal(g, e)
	}
}

func TestUint64Slice(t *testing.T) {
	const N = 1e4
	s := make(Uint64Slice, N)
	for i := range s {
		s[i] = uint64(i) ^ 0x55aa55aa55aa55aa
	}
	s.Sort()
	if !sort.IsSorted(s) {
		t.Fatal()
	}
}

func TestSearchUint64s(t *testing.T) {
	const N = 1e4
	s := make(Uint64Slice, N)
	for i := range s {
		s[i] = uint64(2 * i)
	}
	if g, e := SearchUint64s(s, 12), 6; g != e {
		t.Fatal(g, e)
	}
}

func TestRuneSlice(t *testing.T) {
	const N = 1e4
	s := make(RuneSlice, N)
	for i := range s {
		s[i] = rune(i ^ 0x55aa55aa)
	}
	s.Sort()
	if !sort.IsSorted(s) {
		t.Fatal()
	}
}

func TestSearchRunes(t *testing.T) {
	const N = 1e4
	s := make(RuneSlice, N)
	for i := range s {
		s[i] = rune(2 * i)
	}
	if g, e := SearchRunes(s, rune('\x0c')), 6; g != e {
		t.Fatal(g, e)
	}
}
