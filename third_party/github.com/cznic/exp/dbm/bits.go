// Copyright 2014 The dbm Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package dbm

import (
	"sync"

	"camlistore.org/third_party/github.com/cznic/mathutil"
)

const (
	opOn = iota
	opOff
	opCpl
)

const (
	bitCacheBits = 11
	bitCacheSize = 1 << bitCacheBits
	bitCacheMask = bitCacheSize - 1
)

/*

bitCacheBits: 8
BenchmarkBitsGetSeq	20000000	       124 ns/op
BenchmarkBitsGetRnd	   10000	    146852 ns/op

bitCacheBits: 9
BenchmarkBitsGetSeq	20000000	        99.9 ns/op
BenchmarkBitsGetRnd	   10000	    146174 ns/op

bitCacheBits: 10
BenchmarkBitsGetSeq	20000000	        88.3 ns/op
BenchmarkBitsGetRnd	   10000	    148670 ns/op

bitCacheBits: 11
BenchmarkBitsGetSeq	20000000	        80.9 ns/op
BenchmarkBitsGetRnd	   10000	    146512 ns/op

bitCacheBits: 12
BenchmarkBitsGetSeq	20000000	        80.9 ns/op
BenchmarkBitsGetRnd	   10000	    146713 ns/op

bitCacheBits: 13
BenchmarkBitsGetSeq	20000000	        79.4 ns/op
BenchmarkBitsGetRnd	   10000	    146347 ns/op

bitCacheBits: 14
BenchmarkBitsGetSeq	20000000	        79.0 ns/op
BenchmarkBitsGetRnd	   10000	    146128 ns/op

bitCacheBits: 15
BenchmarkBitsGetSeq	20000000	        78.2 ns/op
BenchmarkBitsGetRnd	   10000	    146194 ns/op

bitCacheBits: 16
BenchmarkBitsGetSeq	20000000	        78.0 ns/op
BenchmarkBitsGetRnd	   10000	    144808 ns/op

*/

var (
	byteMask = [8][8]byte{ // [from][to]
		[8]uint8{0x01, 0x03, 0x07, 0x0f, 0x1f, 0x3f, 0x7f, 0xff},
		[8]uint8{0x00, 0x02, 0x06, 0x0e, 0x1e, 0x3e, 0x7e, 0xfe},
		[8]uint8{0x00, 0x00, 0x04, 0x0c, 0x1c, 0x3c, 0x7c, 0xfc},
		[8]uint8{0x00, 0x00, 0x00, 0x08, 0x18, 0x38, 0x78, 0xf8},
		[8]uint8{0x00, 0x00, 0x00, 0x00, 0x10, 0x30, 0x70, 0xf0},
		[8]uint8{0x00, 0x00, 0x00, 0x00, 0x00, 0x20, 0x60, 0xe0},
		[8]uint8{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x40, 0xc0},
		[8]uint8{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x80},
	}

	bitMask = [8]byte{0x01, 0x02, 0x04, 0x08, 0x10, 0x20, 0x40, 0x80}

	onePage [pgSize]byte
)

func init() {
	for i := range onePage {
		onePage[i] = 0xff
	}
}

// Bits is a File with a bit-manipulation set of methods. It can be useful as
// e.g. a bitmap index[1].
//
// Mutating or reading single bits in a disk file is not a fast operation. Bits
// include a memory cache improving sequential scan/access by Get. The cache is
// coherent with writes/updates but _is not_ coherent with other Bits instances
// of the same underlying File. It is thus recommended to share a single *Bits
// instance between all writers and readers of the same bit file.  Concurrent
// overlapping updates are safe, but the order of their execution is
// unspecified and they may even interleave.  Coordination in the dbm client is
// needed in such case.
//
//   [1]: http://en.wikipedia.org/wiki/Bitmap_index
type Bits struct {
	f     *File
	rwmu  sync.RWMutex
	page  int64
	cache [bitCacheSize]byte
}

func (b *Bits) pageBytes(pgI int64, pgFrom, pgTo, op int) (err error) {
	f := b.f
	a := (*Array)(f)
	switch op {
	case opOn:
		if pgFrom == 0 && pgTo == pgSize*8-1 {
			return a.Set(onePage[:], pgI)
		}

		_, err = f.writeAt(onePage[pgFrom:pgTo+1], pgI*pgSize+int64(pgFrom), true)
		return
	case opOff:
		if pgFrom == 0 && pgTo == pgSize*8-1 {
			return a.Delete(pgI)
		}

		_, err = f.writeAt(zeroPage[pgFrom:pgTo+1], pgI*pgSize+int64(pgFrom), true)
		return
	}

	// case opCpl:
	var buf [pgSize]byte
	var n int
	if n, err = f.readAt(buf[:], pgSize, true); n != pgSize {
		return
	}

	for i, v := range buf[pgFrom : pgTo+1] {
		buf[i] = ^v
	}
	if buf == zeroPage {
		return a.Delete(pgI)
	}

	_, err = f.writeAt(buf[:], pgI*pgSize+int64(pgFrom), true)
	return
}

func (b *Bits) pageByte(off int64, fromBit, toBit, op int) (err error) {
	f := b.f
	var buf [1]byte
	if _, err = f.readAt(buf[:], off, true); err != nil {
		return
	}

	switch op {
	case opOn:
		buf[0] |= byteMask[fromBit][toBit]
	case opOff:
		buf[0] &^= byteMask[fromBit][toBit]
	case opCpl:
		buf[0] ^= byteMask[fromBit][toBit]
	}
	_, err = f.writeAt(buf[:], off, true)
	return
}

func (b *Bits) pageBits(pgI int64, fromBit, toBit, op int) (err error) {
	pgFrom, pgTo := fromBit>>3, toBit>>3
	from, to := fromBit&7, toBit&7
	switch {
	case from == 0 && to == 7:
		return b.pageBytes(pgI, pgFrom, pgTo, op)
	case from == 0 && to != 7:
		switch pgTo - pgFrom {
		case 0:
			return b.pageByte(pgI*pgSize+int64(pgFrom), from, to, op)
		case 1:
			if err = b.pageByte(pgI*pgSize+int64(pgFrom), from, 7, op); err != nil {
				return
			}

			return b.pageByte(pgI*pgSize+int64(pgTo), 0, to, op)
		default:
			if err = b.pageByte(pgI*pgSize+int64(pgFrom), from, 7, op); err != nil {
				return
			}

			if err = b.pageBytes(pgI, pgFrom+1, pgTo-1, op); err != nil {
				return
			}

			return b.pageByte(pgI*pgSize+int64(pgTo), 0, to, op)
		}
	case from != 0 && to == 7:
		switch pgTo - pgFrom {
		case 0:
			return b.pageByte(pgI*pgSize+int64(pgFrom), from, 7, op)
		case 1:
			if err = b.pageByte(pgI*pgSize+int64(pgFrom), from, 7, op); err != nil {
				return
			}

			return b.pageByte(pgI*pgSize+int64(pgTo), 0, 7, op)
		default:
			if err = b.pageByte(pgI*pgSize+int64(pgFrom), from, 7, op); err != nil {
				return
			}

			if err = b.pageBytes(pgI, pgFrom+1, pgTo-1, op); err != nil {
				return
			}

			return b.pageByte(pgI*pgSize+int64(pgTo), 0, 7, op)
		}
	}
	// case from != 0 && to != 7:
	switch pgTo - pgFrom {
	case 0:
		return b.pageByte(pgI*pgSize+int64(pgFrom), from, to, op)
	case 1:
		if err = b.pageByte(pgI*pgSize+int64(pgFrom), from, 7, op); err != nil {
			return
		}

		return b.pageByte(pgI*pgSize+int64(pgTo), 0, to, op)
	default:
		if err = b.pageByte(pgI*pgSize+int64(pgFrom), from, 7, op); err != nil {
			return
		}

		if err = b.pageBytes(pgI, pgFrom+1, pgTo-1, op); err != nil {
			return
		}

		return b.pageByte(pgI*pgSize+int64(pgTo), 0, to, op)
	}
}

func (b *Bits) ops(fromBit, toBit uint64, op int) (err error) {
	const (
		bitsPerPage     = pgSize * 8
		bitsPerPageMask = bitsPerPage - 1
	)

	b.page = -1
	rem := toBit - fromBit + 1
	pgI := int64(fromBit >> (pgBits + 3))
	for rem != 0 {
		pgFrom := fromBit & bitsPerPageMask
		pgTo := mathutil.MinUint64(bitsPerPage-1, pgFrom+rem-1)
		n := pgTo - pgFrom + 1
		if err = b.pageBits(pgI, int(pgFrom), int(pgTo), op); err != nil {
			return
		}

		pgI++
		rem -= n
		fromBit += n
	}
	return
}

// On sets run bits starting from bit.
func (b *Bits) On(bit, run uint64) (err error) {
	if run == 0 {
		return
	}

	return b.ops(bit, bit+run-1, opOn)
}

// Off resets run bits starting from bit.
func (b *Bits) Off(bit, run uint64) (err error) {
	if run == 0 {
		return
	}

	return b.ops(bit, bit+run-1, opOff)
}

// Cpl complements run bits starting from bit.
func (b *Bits) Cpl(bit, run uint64) (err error) {
	if run == 0 {
		return
	}

	return b.ops(bit, bit+run-1, opCpl)
}

// Get returns the value at bit.
func (b *Bits) Get(bit uint64) (val bool, err error) {
	f := b.f
	byte_ := bit >> 3
	pg := int64(byte_ >> bitCacheBits)
	b.rwmu.Lock()
	if pg != b.page {
		if _, err = f.readAt(b.cache[:], pg*bitCacheSize, true); err != nil {
			b.rwmu.Unlock()
			b.page = -1
			return
		}
		b.page = pg
	}

	val = b.cache[byte_&bitCacheMask]&bitMask[bit&7] != 0
	b.rwmu.Unlock()
	return
}
