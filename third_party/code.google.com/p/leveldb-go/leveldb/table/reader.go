// Copyright 2011 The LevelDB-Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package table

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"

	"camlistore.org/third_party/code.google.com/p/leveldb-go/leveldb/crc"
	"camlistore.org/third_party/code.google.com/p/leveldb-go/leveldb/db"
	"camlistore.org/third_party/code.google.com/p/snappy-go/snappy"
)

// blockHandle is the file offset and length of a block.
type blockHandle struct {
	offset, length uint64
}

// decodeBlockHandle returns the block handle encoded at the start of src, as
// well as the number of bytes it occupies. It returns zero if given invalid
// input.
func decodeBlockHandle(src []byte) (blockHandle, int) {
	offset, n := binary.Uvarint(src)
	length, m := binary.Uvarint(src[n:])
	if n == 0 || m == 0 {
		return blockHandle{}, 0
	}
	return blockHandle{offset, length}, n + m
}

func encodeBlockHandle(dst []byte, b blockHandle) int {
	n := binary.PutUvarint(dst, b.offset)
	m := binary.PutUvarint(dst[n:], b.length)
	return n + m
}

// block is a []byte that holds a sequence of key/value pairs plus an index
// over those pairs.
type block []byte

// seek returns a blockIter positioned at the first key/value pair whose key is
// >= the given key. If there is no such key, the blockIter returned is done.
func (b block) seek(c db.Comparer, key []byte) (*blockIter, error) {
	numRestarts := int(binary.LittleEndian.Uint32(b[len(b)-4:]))
	if numRestarts == 0 {
		return nil, errors.New("leveldb/table: invalid table (block has no restart points)")
	}
	n := len(b) - 4*(1+numRestarts)
	var offset int
	if len(key) > 0 {
		// Find the index of the smallest restart point whose key is > the key
		// sought; index will be numRestarts if there is no such restart point.
		index := sort.Search(numRestarts, func(i int) bool {
			o := int(binary.LittleEndian.Uint32(b[n+4*i:]))
			// For a restart point, there are 0 bytes shared with the previous key.
			// The varint encoding of 0 occupies 1 byte.
			o++
			// Decode the key at that restart point, and compare it to the key sought.
			v1, n1 := binary.Uvarint(b[o:])
			_, n2 := binary.Uvarint(b[o+n1:])
			m := o + n1 + n2
			s := b[m : m+int(v1)]
			return c.Compare(s, key) > 0
		})
		// Since keys are strictly increasing, if index > 0 then the restart
		// point at index-1 will be the largest whose key is <= the key sought.
		// If index == 0, then all keys in this block are larger than the key
		// sought, and offset remains at zero.
		if index > 0 {
			offset = int(binary.LittleEndian.Uint32(b[n+4*(index-1):]))
		}
	}
	// Initialize the blockIter to the restart point.
	i := &blockIter{
		data: b[offset:n],
		key:  make([]byte, 0, 256),
	}
	// Iterate from that restart point to somewhere >= the key sought.
	for i.Next() && c.Compare(i.key, key) < 0 {
	}
	if i.err != nil {
		return nil, i.err
	}
	i.soi = !i.eoi
	return i, nil
}

// blockIter is an iterator over a single block of data.
type blockIter struct {
	data     []byte
	key, val []byte
	err      error
	// soi and eoi mark the start and end of iteration.
	// Both cannot simultaneously be true.
	soi, eoi bool
}

// blockIter implements the db.Iterator interface.
var _ db.Iterator = (*blockIter)(nil)

// Next implements Iterator.Next, as documented in the leveldb/db package.
func (i *blockIter) Next() bool {
	if i.eoi || i.err != nil {
		return false
	}
	if i.soi {
		i.soi = false
		return true
	}
	if len(i.data) == 0 {
		i.Close()
		return false
	}
	v0, n0 := binary.Uvarint(i.data)
	v1, n1 := binary.Uvarint(i.data[n0:])
	v2, n2 := binary.Uvarint(i.data[n0+n1:])
	n := n0 + n1 + n2
	i.key = append(i.key[:v0], i.data[n:n+int(v1)]...)
	i.val = i.data[n+int(v1) : n+int(v1+v2)]
	i.data = i.data[n+int(v1+v2):]
	return true
}

// Key implements Iterator.Key, as documented in the leveldb/db package.
func (i *blockIter) Key() []byte {
	if i.soi {
		return nil
	}
	return i.key
}

// Value implements Iterator.Value, as documented in the leveldb/db package.
func (i *blockIter) Value() []byte {
	if i.soi {
		return nil
	}
	return i.val
}

// Close implements Iterator.Close, as documented in the leveldb/db package.
func (i *blockIter) Close() error {
	i.key = nil
	i.val = nil
	i.eoi = true
	return i.err
}

// tableIter is an iterator over an entire table of data. It is a two-level
// iterator: to seek for a given key, it first looks in the index for the
// block that contains that key, and then looks inside that block.
type tableIter struct {
	reader *Reader
	data   *blockIter
	index  *blockIter
	err    error
}

// tableIter implements the db.Iterator interface.
var _ db.Iterator = (*tableIter)(nil)

// nextBlock loads the next block and positions i.data at the first key in that
// block which is >= the given key. If unsuccessful, it sets i.err to any error
// encountered, which may be nil if we have simply exhausted the entire table.
func (i *tableIter) nextBlock(key []byte) bool {
	if !i.index.Next() {
		i.err = i.index.err
		return false
	}
	// Load the next block.
	v := i.index.Value()
	h, n := decodeBlockHandle(v)
	if n == 0 || n != len(v) {
		i.err = errors.New("leveldb/table: corrupt index entry")
		return false
	}
	k, err := i.reader.readBlock(h)
	if err != nil {
		i.err = err
		return false
	}
	// Look for the key inside that block.
	data, err := k.seek(i.reader.comparer, key)
	if err != nil {
		i.err = err
		return false
	}
	i.data = data
	return true
}

// Next implements Iterator.Next, as documented in the leveldb/db package.
func (i *tableIter) Next() bool {
	if i.data == nil {
		return false
	}
	for {
		if i.data.Next() {
			return true
		}
		if i.data.err != nil {
			i.err = i.data.err
			break
		}
		if !i.nextBlock(nil) {
			break
		}
	}
	i.Close()
	return false
}

// Key implements Iterator.Key, as documented in the leveldb/db package.
func (i *tableIter) Key() []byte {
	if i.data == nil {
		return nil
	}
	return i.data.Key()
}

// Value implements Iterator.Value, as documented in the leveldb/db package.
func (i *tableIter) Value() []byte {
	if i.data == nil {
		return nil
	}
	return i.data.Value()
}

// Close implements Iterator.Close, as documented in the leveldb/db package.
func (i *tableIter) Close() error {
	i.data = nil
	return i.err
}

// Reader is a table reader. It implements the DB interface, as documented
// in the leveldb/db package.
type Reader struct {
	file            File
	err             error
	index           block
	comparer        db.Comparer
	verifyChecksums bool
	// TODO: add a (goroutine-safe) LRU block cache.
}

// Reader implements the db.DB interface.
var _ db.DB = (*Reader)(nil)

// Close implements DB.Close, as documented in the leveldb/db package.
func (r *Reader) Close() error {
	if r.err != nil {
		if r.file != nil {
			r.file.Close()
			r.file = nil
		}
		return r.err
	}
	if r.file != nil {
		r.err = r.file.Close()
		r.file = nil
		if r.err != nil {
			return r.err
		}
	}
	// Make any future calls to Get, Find or Close return an error.
	r.err = errors.New("leveldb/table: reader is closed")
	return nil
}

// Get implements DB.Get, as documented in the leveldb/db package.
func (r *Reader) Get(key []byte, o *db.ReadOptions) (value []byte, err error) {
	if r.err != nil {
		return nil, r.err
	}
	i := r.Find(key, o)
	if !i.Next() || !bytes.Equal(key, i.Key()) {
		err := i.Close()
		if err == nil {
			err = db.ErrNotFound
		}
		return nil, err
	}
	return i.Value(), i.Close()
}

// Set is provided to implement the DB interface, but returns an error, as a
// Reader cannot write to a table.
func (r *Reader) Set(key, value []byte, o *db.WriteOptions) error {
	return errors.New("leveldb/table: cannot Set into a read-only table")
}

// Delete is provided to implement the DB interface, but returns an error, as a
// Reader cannot write to a table.
func (r *Reader) Delete(key []byte, o *db.WriteOptions) error {
	return errors.New("leveldb/table: cannot Delete from a read-only table")
}

// Find implements DB.Find, as documented in the leveldb/db package.
func (r *Reader) Find(key []byte, o *db.ReadOptions) db.Iterator {
	if r.err != nil {
		return &tableIter{err: r.err}
	}
	index, err := r.index.seek(r.comparer, key)
	if err != nil {
		return &tableIter{err: err}
	}
	i := &tableIter{
		reader: r,
		index:  index,
	}
	i.nextBlock(key)
	return i
}

// readBlock reads and decompresses a block from disk into memory.
func (r *Reader) readBlock(bh blockHandle) (block, error) {
	b := make([]byte, bh.length+blockTrailerLen)
	if _, err := r.file.ReadAt(b, int64(bh.offset)); err != nil {
		return nil, err
	}
	if r.verifyChecksums {
		checksum0 := binary.LittleEndian.Uint32(b[bh.length+1:])
		checksum1 := crc.New(b[:bh.length+1]).Value()
		if checksum0 != checksum1 {
			return nil, errors.New("leveldb/table: invalid table (checksum mismatch)")
		}
	}
	switch b[bh.length] {
	case noCompressionBlockType:
		return b[:bh.length], nil
	case snappyCompressionBlockType:
		b, err := snappy.Decode(nil, b[:bh.length])
		if err != nil {
			return nil, err
		}
		return b, nil
	}
	return nil, fmt.Errorf("leveldb/table: unknown block compression: %d", b[bh.length])
}

// TODO(nigeltao): move the File interface to the standard package library?
// Package http already defines something similar.

// File holds the raw bytes for a table.
type File interface {
	io.Closer
	io.ReaderAt
	io.Writer
	Stat() (os.FileInfo, error)
}

// NewReader returns a new table reader for the file. Closing the reader will
// close the file.
func NewReader(f File, o *db.Options) *Reader {
	r := &Reader{
		file:            f,
		comparer:        o.GetComparer(),
		verifyChecksums: o.GetVerifyChecksums(),
	}
	if f == nil {
		r.err = errors.New("leveldb/table: nil file")
		return r
	}
	stat, err := f.Stat()
	if err != nil {
		r.err = fmt.Errorf("leveldb/table: invalid table (could not stat file): %v", err)
		return r
	}
	var footer [footerLen]byte
	if stat.Size() < int64(len(footer)) {
		r.err = errors.New("leveldb/table: invalid table (file size is too small)")
		return r
	}
	_, err = f.ReadAt(footer[:], stat.Size()-int64(len(footer)))
	if err != nil && err != io.EOF {
		r.err = fmt.Errorf("leveldb/table: invalid table (could not read footer): %v", err)
		return r
	}
	if string(footer[footerLen-len(magic):footerLen]) != magic {
		r.err = errors.New("leveldb/table: invalid table (bad magic number)")
		return r
	}
	// Ignore the metaindex.
	_, n := decodeBlockHandle(footer[:])
	if n == 0 {
		r.err = errors.New("leveldb/table: invalid table (bad metaindex block handle)")
		return r
	}
	// Read the index into memory.
	indexBH, n := decodeBlockHandle(footer[n:])
	if n == 0 {
		r.err = errors.New("leveldb/table: invalid table (bad index block handle)")
		return r
	}
	r.index, r.err = r.readBlock(indexBH)
	return r
}
