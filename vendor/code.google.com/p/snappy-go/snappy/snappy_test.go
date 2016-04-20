// Copyright 2011 The Snappy-Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package snappy

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"math/rand"
	"strings"
	"testing"
)

func roundtrip(b []byte) error {
	e, err := Encode(nil, b)
	if err != nil {
		return fmt.Errorf("encoding error: %v", err)
	}
	d, err := Decode(nil, e)
	if err != nil {
		return fmt.Errorf("decoding error: %v", err)
	}
	if !bytes.Equal(b, d) {
		return fmt.Errorf("roundtrip mismatch:\n\twant %v\n\tgot  %v", b, d)
	}
	return nil
}

func TestSmallCopy(t *testing.T) {
	for i := 0; i < 32; i++ {
		s := "aaaa" + strings.Repeat("b", i) + "aaaabbbb"
		if err := roundtrip([]byte(s)); err != nil {
			t.Fatalf("i=%d: %v", i, err)
		}
	}
}

func TestSmallRand(t *testing.T) {
	rand.Seed(27354294)
	for n := 1; n < 20000; n += 23 {
		b := make([]byte, n)
		for i, _ := range b {
			b[i] = uint8(rand.Uint32())
		}
		if err := roundtrip(b); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSmallRegular(t *testing.T) {
	for n := 1; n < 20000; n += 23 {
		b := make([]byte, n)
		for i, _ := range b {
			b[i] = uint8(i%10 + 'a')
		}
		if err := roundtrip(b); err != nil {
			t.Fatal(err)
		}
	}
}

func benchWords(b *testing.B, n int, decode bool) {
	b.StopTimer()

	// Make src, a []byte of length n containing copies of the words file.
	words, err := ioutil.ReadFile("/usr/share/dict/words")
	if err != nil {
		panic(err)
	}
	if len(words) == 0 {
		panic("/usr/share/dict/words has zero length")
	}
	src := make([]byte, n)
	for x := src; len(x) > 0; {
		n := copy(x, words)
		x = x[n:]
	}

	// If benchmarking decoding, encode the src.
	if decode {
		src, err = Encode(nil, src)
		if err != nil {
			panic(err)
		}
	}
	b.SetBytes(int64(len(src)))

	// Allocate a sufficiently large dst buffer.
	var dst []byte
	if decode {
		dst = make([]byte, n)
	} else {
		dst = make([]byte, MaxEncodedLen(n))
	}

	// Run the loop.
	b.StartTimer()
	for i := 0; i < b.N; i++ {
		if decode {
			Decode(dst, src)
		} else {
			Encode(dst, src)
		}
	}
}

func BenchmarkDecodeWords1e3(b *testing.B) { benchWords(b, 1e3, true) }
func BenchmarkDecodeWords1e4(b *testing.B) { benchWords(b, 1e4, true) }
func BenchmarkDecodeWords1e5(b *testing.B) { benchWords(b, 1e5, true) }
func BenchmarkDecodeWords1e6(b *testing.B) { benchWords(b, 1e6, true) }
func BenchmarkEncodeWords1e3(b *testing.B) { benchWords(b, 1e3, false) }
func BenchmarkEncodeWords1e4(b *testing.B) { benchWords(b, 1e4, false) }
func BenchmarkEncodeWords1e5(b *testing.B) { benchWords(b, 1e5, false) }
func BenchmarkEncodeWords1e6(b *testing.B) { benchWords(b, 1e6, false) }
