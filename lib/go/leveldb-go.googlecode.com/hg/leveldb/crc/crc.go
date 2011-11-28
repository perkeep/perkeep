// Copyright 2011 The LevelDB-Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package crc implements the checksum algorithm used throughout leveldb.
//
// The algorithm is CRC-32 with Castagnoli's polynomial, followed by a bit
// rotation and an additional delta. The additional processing is to lessen
// the probability of arbitrary key/value data coincidental contains bytes
// that look like a checksum.
//
// To calculate the uint32 checksum of some data:
//	var u uint32 = crc.New(data).Value()
// In leveldb, the uint32 value is then stored in little-endian format.
package crc

import (
	"hash/crc32"
)

var table = crc32.MakeTable(crc32.Castagnoli)

type CRC uint32

func New(b []byte) CRC {
	return CRC(0).Update(b)
}

func (c CRC) Update(b []byte) CRC {
	return CRC(crc32.Update(uint32(c), table, b))
}

func (c CRC) Value() uint32 {
	return uint32(c>>15|c<<17) + 0xa282ead8
}
