// GoMySQL - A MySQL client library for Go
//
// Copyright 2010-2011 Phil Bayfield. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
package mysql

import (
	"crypto/sha1"
	"math"
)

const SCRAMBLE_LENGTH_323 = 8

// Random struct, see libmysql/password.c
type randStruct struct {
	maxValue    uint32
	maxValueDbl float64
	seed1       uint32
	seed2       uint32
}

// Initialise rand struct, see libmysql/password.c
func randominit(seed1, seed2 uint32) *randStruct {
	return &randStruct{
		maxValue:    0x3FFFFFFF,
		maxValueDbl: 0x3FFFFFFF,
		seed1:       seed1 % 0x3FFFFFFF,
		seed2:       seed2 % 0x3FFFFFFF,
	}
}

// Generate a random number, see libmysql/password.c
func (r *randStruct) myRnd() float64 {
	r.seed1 = (r.seed1*3 + r.seed2) % r.maxValue
	r.seed2 = (r.seed1 + r.seed2 + 33) % r.maxValue
	return float64(r.seed1) / r.maxValueDbl
}

// Password hash used in pre-4.1, see libmysql/password.c
func hashPassword(password []byte) []uint32 {
	nr := uint32(1345345333)
	add := uint32(7)
	nr2 := uint32(0x12345671)
	for i := 0; i < len(password); i++ {
		if password[i] == ' ' || password[i] == '\t' {
			continue
		}
		tmp := uint32(password[i])
		nr ^= (((nr & 63) + add) * tmp) + (nr << 8)
		nr2 += (nr2 << 8) ^ nr
		add += tmp
	}
	result := make([]uint32, 2)
	result[0] = nr & ((1 << 31) - 1)
	result[1] = nr2 & ((1 << 31) - 1)
	return result
}

// Encrypt password the pre-4.1 way, see libmysql/password.c
func scramble323(message, password []byte) (result []byte) {
	if len(password) == 0 {
		return
	}
	// Check message is no longer than max length
	if len(message) > SCRAMBLE_LENGTH_323 {
		message = message[:SCRAMBLE_LENGTH_323]
	}
	// Generate hashes
	hashPass := hashPassword(password)
	hashMessage := hashPassword(message)
	// Initialise random struct
	rand := randominit(hashPass[0]^hashMessage[0], hashPass[1]^hashMessage[1])
	// Generate result
	result = make([]byte, SCRAMBLE_LENGTH_323)
	for i := 0; i < SCRAMBLE_LENGTH_323; i++ {
		result[i] = byte(math.Floor(rand.myRnd()*31) + 64)
	}
	extra := byte(math.Floor(rand.myRnd() * 31))
	for i := 0; i < SCRAMBLE_LENGTH_323; i++ {
		result[i] ^= extra
	}
	return
}

// Encrypt password using 4.1+ method
func scramble41(message, password []byte) (result []byte) {
	if len(password) == 0 {
		return
	}
	// stage1_hash = SHA1(password)
	// SHA1 encode
	crypt := sha1.New()
	crypt.Write(password)
	stg1Hash := crypt.Sum(nil)
	// token = SHA1(SHA1(stage1_hash), scramble) XOR stage1_hash
	// SHA1 encode again
	crypt.Reset()
	crypt.Write(stg1Hash)
	stg2Hash := crypt.Sum(nil)
	// SHA1 2nd hash and scramble
	crypt.Reset()
	crypt.Write(message)
	crypt.Write(stg2Hash)
	stg3Hash := crypt.Sum(nil)
	// XOR with first hash
	result = make([]byte, 20)
	for i := range result {
		result[i] = stg3Hash[i] ^ stg1Hash[i]
	}
	return
}
