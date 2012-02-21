// Copyright 2011 The LevelDB-Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package table

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	"camlistore.org/third_party/code.google.com/p/leveldb-go/leveldb/crc"
	"camlistore.org/third_party/code.google.com/p/leveldb-go/leveldb/db"
	"camlistore.org/third_party/code.google.com/p/snappy-go/snappy"
)

// indexEntry is a block handle and the length of the separator key.
type indexEntry struct {
	bh     blockHandle
	keyLen int
}

// Writer is a table writer. It implements the DB interface, as documented
// in the leveldb/db package.
type Writer struct {
	writer    io.Writer
	bufWriter *bufio.Writer
	closer    io.Closer
	err       error
	// The next four fields are copied from a db.Options.
	blockRestartInterval int
	blockSize            int
	cmp                  db.Comparer
	compression          db.Compression
	// A table is a series of blocks and a block's index entry contains a
	// separator key between one block and the next. Thus, a finished block
	// cannot be written until the first key in the next block is seen.
	// pendingBH is the blockHandle of a finished block that is waiting for
	// the next call to Set. If the writer is not in this state, pendingBH
	// is zero.
	pendingBH blockHandle
	// offset is the offset (relative to the table start) of the next block
	// to be written.
	offset uint64
	// prevKey is a copy of the key most recently passed to Set.
	prevKey []byte
	// indexKeys and indexEntries hold the separator keys between each block
	// and the successor key for the final block. indexKeys contains the key's
	// bytes concatenated together. The keyLen field of each indexEntries
	// element is the length of the respective separator key.
	indexKeys    []byte
	indexEntries []indexEntry
	// The next three fields hold data for the current block:
	//   - buf is the accumulated uncompressed bytes,
	//   - nEntries is the number of entries,
	//   - restarts are the offsets (relative to the block start) of each
	//     restart point.
	buf      bytes.Buffer
	nEntries int
	restarts []uint32
	// compressedBuf is the destination buffer for snappy compression. It is
	// re-used over the lifetime of the writer, avoiding the allocation of a
	// temporary buffer for each block.
	compressedBuf []byte
	// tmp is a scratch buffer, large enough to hold either footerLen bytes,
	// blockTrailerLen bytes, or (5 * binary.MaxVarintLen64) bytes.
	tmp [50]byte
}

// Writer implements the db.DB interface.
var _ db.DB = (*Writer)(nil)

// Get is provided to implement the DB interface, but returns an error, as a
// Writer cannot read from a table.
func (w *Writer) Get(key []byte, o *db.ReadOptions) ([]byte, error) {
	return nil, errors.New("leveldb/table: cannot Get from a write-only table")
}

// Delete is provided to implement the DB interface, but returns an error, as a
// Writer can only append key/value pairs.
func (w *Writer) Delete(key []byte, o *db.WriteOptions) error {
	return errors.New("leveldb/table: cannot Delete from a table")
}

// Find is provided to implement the DB interface, but returns an error, as a
// Writer cannot read from a table.
func (w *Writer) Find(key []byte, o *db.ReadOptions) db.Iterator {
	return &tableIter{
		err: errors.New("leveldb/table: cannot Find from a write-only table"),
	}
}

// Set implements DB.Set, as documented in the leveldb/db package. For a given
// Writer, the keys passed to Set must be in increasing order.
func (w *Writer) Set(key, value []byte, o *db.WriteOptions) error {
	if w.err != nil {
		return w.err
	}
	if w.cmp.Compare(w.prevKey, key) >= 0 {
		w.err = fmt.Errorf("leveldb/table: Set called in non-increasing key order: %q, %q", w.prevKey, key)
		return w.err
	}
	w.flushPendingBH(key)
	w.append(key, value, w.nEntries%w.blockRestartInterval == 0)
	// If the estimated block size is sufficiently large, finish the current block.
	if w.buf.Len()+4*(len(w.restarts)+1) >= w.blockSize {
		bh, err := w.finishBlock()
		if err != nil {
			w.err = err
			return w.err
		}
		w.pendingBH = bh
	}
	return nil
}

// flushPendingBH adds any pending block handle to the index entries.
func (w *Writer) flushPendingBH(key []byte) {
	if w.pendingBH.length == 0 {
		// A valid blockHandle must be non-zero.
		// In particular, it must have a non-zero length.
		return
	}
	n0 := len(w.indexKeys)
	w.indexKeys = w.cmp.AppendSeparator(w.indexKeys, w.prevKey, key)
	n1 := len(w.indexKeys)
	w.indexEntries = append(w.indexEntries, indexEntry{w.pendingBH, n1 - n0})
	w.pendingBH = blockHandle{}
}

// append appends a key/value pair, which may also be a restart point.
func (w *Writer) append(key, value []byte, restart bool) {
	nShared := 0
	if restart {
		w.restarts = append(w.restarts, uint32(w.buf.Len()))
	} else {
		nShared = db.SharedPrefixLen(w.prevKey, key)
	}
	w.prevKey = append(w.prevKey[:0], key...)
	w.nEntries++
	n := binary.PutUvarint(w.tmp[0:], uint64(nShared))
	n += binary.PutUvarint(w.tmp[n:], uint64(len(key)-nShared))
	n += binary.PutUvarint(w.tmp[n:], uint64(len(value)))
	w.buf.Write(w.tmp[:n])
	w.buf.Write(key[nShared:])
	w.buf.Write(value)
}

// finishBlock finishes the current block and returns its block handle, which is
// its offset and length in the table.
func (w *Writer) finishBlock() (blockHandle, error) {
	// Write the restart points to the buffer.
	if w.nEntries == 0 {
		// Every block must have at least one restart point.
		w.restarts = w.restarts[:1]
		w.restarts[0] = 0
	}
	tmp4 := w.tmp[:4]
	for _, x := range w.restarts {
		binary.LittleEndian.PutUint32(tmp4, x)
		w.buf.Write(tmp4)
	}
	binary.LittleEndian.PutUint32(tmp4, uint32(len(w.restarts)))
	w.buf.Write(tmp4)

	// Compress the buffer, discarding the result if the improvement
	// isn't at least 12.5%.
	b := w.buf.Bytes()
	w.tmp[0] = noCompressionBlockType
	if w.compression == db.SnappyCompression {
		compressed, err := snappy.Encode(w.compressedBuf, b)
		if err != nil {
			return blockHandle{}, err
		}
		w.compressedBuf = compressed[:cap(compressed)]
		if len(compressed) < len(b)-len(b)/8 {
			w.tmp[0] = snappyCompressionBlockType
			b = compressed
		}
	}

	// Calculate the checksum.
	checksum := crc.New(b).Update(w.tmp[:1]).Value()
	binary.LittleEndian.PutUint32(w.tmp[1:5], checksum)

	// Write the bytes to the file.
	if _, err := w.writer.Write(b); err != nil {
		return blockHandle{}, err
	}
	if _, err := w.writer.Write(w.tmp[:5]); err != nil {
		return blockHandle{}, err
	}
	bh := blockHandle{w.offset, uint64(len(b))}
	w.offset += uint64(len(b)) + blockTrailerLen

	// Reset the per-block state.
	w.buf.Reset()
	w.nEntries = 0
	w.restarts = w.restarts[:0]
	return bh, nil
}

// Close implements DB.Close, as documented in the leveldb/db package.
func (w *Writer) Close() (err error) {
	defer func() {
		if w.closer == nil {
			return
		}
		err1 := w.closer.Close()
		if err == nil {
			err = err1
		}
		w.closer = nil
	}()
	if w.err != nil {
		return w.err
	}

	// Finish the last data block, or force an empty data block if there
	// aren't any data blocks at all.
	if w.nEntries > 0 || len(w.indexEntries) == 0 {
		bh, err := w.finishBlock()
		if err != nil {
			w.err = err
			return w.err
		}
		w.pendingBH = bh
		w.flushPendingBH(nil)
	}

	// Write the (empty) metaindex block.
	metaindexBlockHandle, err := w.finishBlock()
	if err != nil {
		w.err = err
		return w.err
	}

	// Write the index block.
	// writer.append uses w.tmp[:3*binary.MaxVarintLen64].
	i0, tmp := 0, w.tmp[3*binary.MaxVarintLen64:5*binary.MaxVarintLen64]
	for _, ie := range w.indexEntries {
		n := encodeBlockHandle(tmp, ie.bh)
		i1 := i0 + ie.keyLen
		w.append(w.indexKeys[i0:i1], tmp[:n], true)
		i0 = i1
	}
	indexBlockHandle, err := w.finishBlock()
	if err != nil {
		w.err = err
		return w.err
	}

	// Write the table footer.
	footer := w.tmp[:footerLen]
	for i := range footer {
		footer[i] = 0
	}
	n := encodeBlockHandle(footer, metaindexBlockHandle)
	encodeBlockHandle(footer[n:], indexBlockHandle)
	copy(footer[footerLen-len(magic):], magic)
	if _, err := w.writer.Write(footer); err != nil {
		w.err = err
		return w.err
	}

	// Flush the buffer.
	if w.bufWriter != nil {
		if err := w.bufWriter.Flush(); err != nil {
			w.err = err
			return err
		}
	}

	// Make any future calls to Set or Close return an error.
	w.err = errors.New("leveldb/table: writer is closed")
	return nil
}

// NewWriter returns a new table writer for the file. Closing the writer will
// close the file.
func NewWriter(f File, o *db.Options) *Writer {
	w := &Writer{
		closer:               f,
		blockRestartInterval: o.GetBlockRestartInterval(),
		blockSize:            o.GetBlockSize(),
		cmp:                  o.GetComparer(),
		compression:          o.GetCompression(),
		prevKey:              make([]byte, 0, 256),
		restarts:             make([]uint32, 0, 256),
	}
	if f == nil {
		w.err = errors.New("leveldb/table: nil file")
		return w
	}
	// If f does not have a Flush method, do our own buffering.
	type flusher interface {
		Flush() error
	}
	if _, ok := f.(flusher); ok {
		w.writer = f
	} else {
		w.bufWriter = bufio.NewWriter(f)
		w.writer = w.bufWriter
	}
	return w
}
