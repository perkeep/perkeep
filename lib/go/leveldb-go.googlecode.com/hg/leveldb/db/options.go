// Copyright 2011 The LevelDB-Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package db

// Compression is the per-block compression algorithm to use.
type Compression int

const (
	DefaultCompression Compression = iota
	NoCompression
	SnappyCompression
	nCompression
)

// Options holds the optional parameters for leveldb's DB implementations.
// They are typically passed to a constructor function as a struct literal.
// The GetXxx methods are used inside the DB implementations; they return the
// default parameter value if the *Options receiver is nil or the field value
// is zero.
//
// Read/Write options:
//   - Comparer
// Read options:
//   - VerifyChecksums
// Write options:
//   - BlockRestartInterval
//   - BlockSize
//   - Compression
type Options struct {
	// BlockRestartInterval is the number of keys between restart points
	// for delta encoding of keys.
	//
	// The default value is 16.
	BlockRestartInterval int

	// BlockSize is the minimum uncompressed size in bytes of each table block.
	//
	// The default value is 4096.
	BlockSize int

	// Comparer defines a total ordering over the space of []byte keys: a 'less
	// than' relationship. The same comparison algorithm must be used for reads
	// and writes over the lifetime of the DB.
	//
	// The default value uses the same ordering as bytes.Compare.
	Comparer Comparer

	// Compression defines the per-block compression to use.
	//
	// The default value (DefaultCompression) uses snappy compression.
	Compression Compression

	// VerifyChecksums is whether to verify the per-block checksums in a DB.
	//
	// The default value is false.
	VerifyChecksums bool
}

func (o *Options) GetBlockRestartInterval() int {
	if o == nil || o.BlockRestartInterval <= 0 {
		return 16
	}
	return o.BlockRestartInterval
}

func (o *Options) GetBlockSize() int {
	if o == nil || o.BlockSize <= 0 {
		return 4096
	}
	return o.BlockSize
}

func (o *Options) GetComparer() Comparer {
	if o == nil || o.Comparer == nil {
		return DefaultComparer
	}
	return o.Comparer
}

func (o *Options) GetCompression() Compression {
	if o == nil || o.Compression <= DefaultCompression || o.Compression >= nCompression {
		// Default to SnappyCompression.
		return SnappyCompression
	}
	return o.Compression
}

func (o *Options) GetVerifyChecksums() bool {
	if o == nil {
		return false
	}
	return o.VerifyChecksums
}
