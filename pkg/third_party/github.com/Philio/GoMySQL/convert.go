// GoMySQL - A MySQL client library for Go
//
// Copyright 2010-2011 Phil Bayfield. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
package mysql

import (
	"io"
	"math"
	"strconv"
)

// bytes to int
func btoi(b []byte) int {
	return int(btoui(b))
}

// int to bytes
func itob(n int) []byte {
	return uitob(uint(n))
}

// bytes to uint
func btoui(b []byte) (n uint) {
	for i := uint8(0); i < uint8(strconv.IntSize)/8; i++ {
		n |= uint(b[i]) << (i * 8)
	}
	return
}

// uint to bytes
func uitob(n uint) (b []byte) {
	b = make([]byte, strconv.IntSize/8)
	for i := uint8(0); i < uint8(strconv.IntSize)/8; i++ {
		b[i] = byte(n >> (i * 8))
	}
	return
}

// bytes to int16
func btoi16(b []byte) int16 {
	return int16(btoui16(b))
}

// int16 to bytes
func i16tob(n int16) []byte {
	return ui16tob(uint16(n))
}

// bytes to uint16
func btoui16(b []byte) (n uint16) {
	n |= uint16(b[0])
	n |= uint16(b[1]) << 8
	return
}

// uint16 to bytes
func ui16tob(n uint16) (b []byte) {
	b = make([]byte, 2)
	b[0] = byte(n)
	b[1] = byte(n >> 8)
	return
}

// bytes to int24
func btoi24(b []byte) (n int32) {
	u := btoui24(b)
	if u&0x800000 != 0 {
		u |= 0xff000000
	}
	n = int32(u)
	return
}

// int24 to bytes
func i24tob(n int32) []byte {
	return ui24tob(uint32(n))
}

// bytes to uint24
func btoui24(b []byte) (n uint32) {
	for i := uint8(0); i < 3; i++ {
		n |= uint32(b[i]) << (i * 8)
	}
	return
}

// uint24 to bytes
func ui24tob(n uint32) (b []byte) {
	b = make([]byte, 3)
	for i := uint8(0); i < 3; i++ {
		b[i] = byte(n >> (i * 8))
	}
	return
}

// bytes to int32
func btoi32(b []byte) int32 {
	return int32(btoui32(b))
}

// int32 to bytes
func i32tob(n int32) []byte {
	return ui32tob(uint32(n))
}

// bytes to uint32
func btoui32(b []byte) (n uint32) {
	for i := uint8(0); i < 4; i++ {
		n |= uint32(b[i]) << (i * 8)
	}
	return
}

// uint32 to bytes
func ui32tob(n uint32) (b []byte) {
	b = make([]byte, 4)
	for i := uint8(0); i < 4; i++ {
		b[i] = byte(n >> (i * 8))
	}
	return
}

// bytes to int64
func btoi64(b []byte) int64 {
	return int64(btoui64(b))
}

// int64 to bytes
func i64tob(n int64) []byte {
	return ui64tob(uint64(n))
}

// bytes to uint64
func btoui64(b []byte) (n uint64) {
	for i := uint8(0); i < 8; i++ {
		n |= uint64(b[i]) << (i * 8)
	}
	return
}

// uint64 to bytes
func ui64tob(n uint64) (b []byte) {
	b = make([]byte, 8)
	for i := uint8(0); i < 8; i++ {
		b[i] = byte(n >> (i * 8))
	}
	return
}

// bytes to float32
func btof32(b []byte) float32 {
	return math.Float32frombits(btoui32(b))
}

// float32 to bytes
func f32tob(f float32) []byte {
	return ui32tob(math.Float32bits(f))
}

// bytes to float64
func btof64(b []byte) float64 {
	return math.Float64frombits(btoui64(b))
}

// float64 to bytes
func f64tob(f float64) []byte {
	return ui64tob(math.Float64bits(f))
}

// bytes to length
func btolcb(b []byte) (num uint64, n int, err error) {
	switch {
	// 0-250 = value of first byte
	case b[0] <= 250:
		num = uint64(b[0])
		n = 1
		return
	// 251 column value = NULL
	case b[0] == 251:
		num = 0
		n = 1
		return
	// 252 following 2 = value of following 16-bit word
	case b[0] == 252:
		n = 3
	// 253 following 3 = value of following 24-bit word
	case b[0] == 253:
		n = 4
	// 254 following 8 = value of following 64-bit word
	case b[0] == 254:
		n = 9
	}
	// Check there are enough bytes
	if len(b) < n {
		err = io.EOF
		return
	}
	// Get uint64
	t := make([]byte, 8)
	copy(t, b[1:n])
	num = btoui64(t)
	return
}

// length to bytes
func lcbtob(n uint64) (b []byte) {
	switch {
	// <= 250 = 1 byte
	case n <= 250:
		b = []byte{byte(n)}
	// <= 0xffff = 252 + 2 bytes
	case n <= 0xffff:
		b = []byte{0xfc, byte(n), byte(n >> 8)}
	// <= 0xffffff = 253 + 3 bytes
	case n <= 0xffffff:
		b = []byte{0xfd, byte(n), byte(n >> 8), byte(n >> 16)}
		// Due to max packet size the 8 byte version is never actually used so is ommited
	}
	return
}

// any to uint64
func atoui64(i interface{}) (n uint64) {
	switch t := i.(type) {
	case int64:
		n = uint64(t)
	case uint64:
		return t
	case string:
		// Convert to int64 first for signing bit
		in, err := strconv.ParseInt(t, 10, 64)
		if err != nil {
			panic("Invalid string for integer conversion")
		}
		n = uint64(in)
	default:
		panic("Not a numeric type")
	}
	return
}

// any to float64
func atof64(i interface{}) (f float64) {
	switch t := i.(type) {
	case float32:
		f = float64(t)
	case float64:
		return t
	case string:
		var err error
		f, err = strconv.ParseFloat(t, 64)
		if err != nil {
			panic("Invalid string for floating point conversion")
		}
	default:
		panic("Not a floating point type")
	}
	return
}

// any to string
func atos(i interface{}) (s string) {
	switch t := i.(type) {
	case int64:
		s = strconv.FormatInt(t, 10)
	case uint64:
		s = strconv.FormatUint(t, 10)
	case float32:
		s = strconv.FormatFloat(float64(t), 'f', -1, 32)
	case float64:
		s = strconv.FormatFloat(t, 'f', -1, 64)
	case []byte:
		s = string(t)
	case Date:
		return t.String()
	case Time:
		return t.String()
	case DateTime:
		return t.String()
	case string:
		return t
	default:
		panic("Not a string or compatible type")
	}
	return
}
