// Copyright 2014 The dbm Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package dbm

import (
	"bytes"
	"fmt"

	"camlistore.org/third_party/github.com/cznic/exp/lldb"
	"camlistore.org/third_party/github.com/cznic/fileutil"
	"camlistore.org/third_party/github.com/cznic/mathutil"
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

func collate(a, b []byte) (r int) {
	da, err := lldb.DecodeScalars(a)
	if err != nil {
		panic(err)
	}

	db, err := lldb.DecodeScalars(b)
	if err != nil {
		panic(err)
	}

	r, err = lldb.Collate(da, db, nil)
	if err != nil {
		panic(err)
	}

	return
}

type treeCache map[string]*lldb.BTree

func (t *treeCache) get() (r map[string]*lldb.BTree) {
	if r = *t; r != nil {
		return
	}

	*t = map[string]*lldb.BTree{}
	return *t
}

func (t *treeCache) getTree(db *DB, prefix int, name string, canCreate bool, cacheSize int) (r *lldb.BTree, err error) {
	m := t.get()
	r, ok := m[name]
	if ok {
		return
	}

	root, err := db.root()
	if err != nil {
		return
	}

	val, err := root.get(prefix, name)
	if err != nil {
		return
	}

	switch x := val.(type) {
	case nil:
		if !canCreate {
			return
		}

		var h int64
		r, h, err = lldb.CreateBTree(db.alloc, collate)
		if err != nil {
			return nil, err
		}

		if err = root.set(h, prefix, name); err != nil {
			return nil, err
		}
	case int64:
		if r, err = lldb.OpenBTree(db.alloc, collate, x); err != nil {
			return nil, err
		}
	default:
		return nil, &lldb.ErrINVAL{Src: "corrupted root directory value for", Val: fmt.Sprintf("%q, %q", prefix, name)}
	}

	if len(m) > cacheSize {
		i, j, n := 0, cacheSize/2, mathutil.Min(cacheSize/20, 10)
	loop:
		for k := range m {
			if i++; i >= j {
				delete(m, k)
				if n == 0 {
					break loop
				}

				n--
			}
		}
	}

	m[name] = r
	return
}

func encVal(val interface{}) (r []byte, err error) {
	switch x := val.(type) {
	case []interface{}:
		return lldb.EncodeScalars(x...)
	default:
		return lldb.EncodeScalars(x)
	}
}

func noEof(e error) (err error) {
	if !fileutil.IsEOF(e) {
		err = e
	}
	return
}
