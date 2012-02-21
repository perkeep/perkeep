// Copyright 2011 The LevelDB-Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

/*
Package table implements readers and writers of leveldb tables.

Tables are either opened for reading or created for writing but not both.

A reader can create iterators, which yield all key/value pairs whose keys
are 'greater than or equal' to a starting key. There may be multiple key/
value pairs that have the same key.

A reader can be used concurrently. Multiple goroutines can call Find
concurrently, and each iterator can run concurrently with other iterators.
However, any particular iterator should not be used concurrently, and
iterators should not be used once a reader is closed.

A writer writes key/value pairs in increasing key order, and cannot be used
concurrently. A table cannot be read until the writer has finished.

Readers and writers can be created with various options. Passing a nil
Options pointer is valid and means to use the default values.

One such option is to define the 'less than' ordering for keys. The default
Comparer uses the natural ordering consistent with bytes.Compare. The same
ordering should be used for reading and writing a table.

To return the value for a key:

	r := table.NewReader(file, options)
	defer r.Close()
	return r.Get(key)

To count the number of entries in a table:

	i, n := r.Find(nil), 0
	for i.Next() {
		n++
	}
	if err := i.Close(); err != nil {
		return 0, err
	}
	return n, nil

To write a table with three entries:

	w := table.NewWriter(file, options)
	if err := w.Set([]byte("apple"), []byte("red")); err != nil {
		w.Close()
		return err
	}
	if err := w.Set([]byte("banana"), []byte("yellow")); err != nil {
		w.Close()
		return err
	}
	if err := w.Set([]byte("cherry"), []byte("red")); err != nil {
		w.Close()
		return err
	}
	return w.Close()
*/
package table

/*
The table file format looks like:

<start_of_file>
[data block 0]
[data block 1]
...
[data block N-1]
[meta block 0]
[meta block 1]
...
[meta block K-1]
[metaindex block]
[index block]
[footer]
<end_of_file>

Each block consists of some data and a 5 byte trailer: a 1 byte block type and
a 4 byte checksum of the compressed data. The block type gives the per-block
compression used; each block is compressed independently. The checksum
algorithm is described in the leveldb/crc package.

The decompressed block data consists of a sequence of key/value entries
followed by a trailer. Each key is encoded as a shared prefix length and a
remainder string. For example, if two adjacent keys are "tweedledee" and
"tweedledum", then the second key would be encoded as {8, "um"}. The shared
prefix length is varint encoded. The remainder string and the value are
encoded as a varint-encoded length followed by the literal contents. To
continue the example, suppose that the key "tweedledum" mapped to the value
"socks". The encoded key/value entry would be: "\x08\x02\x05umsocks".

Every block has a restart interval I. Every I'th key/value entry in that block
is called a restart point, and shares no key prefix with the previous entry.
Continuing the example above, if the key after "tweedledum" was "two", but was
part of a restart point, then that key would be encoded as {0, "two"} instead
of {2, "o"}. If a block has P restart points, then the block trailer consists
of (P+1)*4 bytes: (P+1) little-endian uint32 values. The first P of these
uint32 values are the block offsets of each restart point. The final uint32
value is P itself. Thus, when seeking for a particular key, one can use binary
search to find the largest restart point whose key is <= the key sought.

An index block is a block with N key/value entries. The i'th value is the
encoded block handle of the i'th data block. The i'th key is a separator for
i < N-1, and a successor for i == N-1. The separator between blocks i and i+1
is a key that is >= every key in block i and is < every key i block i+1. The
successor for the final block is a key that is >= every key in block N-1. The
index block restart interval is 1: every entry is a restart point.

The table footer is exactly 48 bytes long:
  - the block handle for the metaindex block,
  - the block handle for the index block,
  - padding to take the two items above up to 40 bytes,
  - an 8-byte magic string.

A block handle is an offset and a length; the length does not include the 5
byte trailer. Both numbers are varint-encoded, with no padding between the two
values. The maximum size of an encoded block handle is therefore 20 bytes.
*/

const (
	blockTrailerLen = 5
	footerLen       = 48

	magic = "\x57\xfb\x80\x8b\x24\x75\x47\xdb"

	// The block type gives the per-block compression format.
	// These constants are part of the file format and should not be changed.
	// They are different from the db.Compression constants because the latter
	// are designed so that the zero value of the db.Compression type means to
	// use the default compression (which is snappy).
	noCompressionBlockType     = 0
	snappyCompressionBlockType = 1
)
