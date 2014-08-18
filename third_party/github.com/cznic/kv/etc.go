// Copyright 2014 The kv Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package kv

import (
	"bytes"
	"fmt"

	"camlistore.org/third_party/github.com/cznic/fileutil"
)

type header struct {
	magic    []byte
	ver      byte
	reserved []byte
}

func (h *header) rd(b []byte) error {
	if len(b) != 16 {
		panic("internal error")
	}

	if h.magic = b[:4]; bytes.Compare(h.magic, []byte(magic)) != 0 {
		return fmt.Errorf("Unknown file format")
	}

	b = b[4:]
	h.ver = b[0]
	h.reserved = b[1:]
	return nil
}

// Get a 7B int64 from b
func b2h(b []byte) (h int64) {
	for _, v := range b[:7] {
		h = h<<8 | int64(v)
	}
	return
}

// Put a 7B int64 into b
func h2b(b []byte, h int64) []byte {
	for i := range b[:7] {
		b[i], h = byte(h>>48), h<<8
	}
	return b
}

func noEof(e error) (err error) {
	if !fileutil.IsEOF(e) {
		err = e
	}
	return
}
