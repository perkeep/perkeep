// Copyright 2011 The LevelDB-Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package db defines the interfaces for a key/value store.
//
// A DB's basic operations (Get, Set, Delete) should be self-explanatory. Get
// and Delete will return ErrNotFound if the requested key is not in the store.
// Callers are free to ignore this error.
//
// A DB also allows for iterating over the key/value pairs in key order. If d
// is a DB, the code below prints all key/value pairs whose keys are 'greater
// than or equal to' k:
//
//	iter := d.Find(k)
//	for iter.Next() {
//		fmt.Printf("key=%q value=%q\n", iter.Key(), iter.Value())
//	}
//	return iter.Close()
//
// Other leveldb packages provide implementations of these interfaces. The
// Options struct in this package holds the optional parameters for these
// implementations, including a Comparer to define a 'less than' relationship
// over keys. It is always valid to pass a nil *Options, which means to use
// the default parameter values. Any zero field of a non-nil *Options also
// means to use the default value for that parameter. Thus, the code below
// uses a custom Comparer, but the default values for every other parameter:
//
//	db := memdb.New(&db.Options{
//		Comparer: myComparer,
//	})
package db

import (
	"errors"
)

// ErrNotFound means that a get or delete call did not find the requested key.
var ErrNotFound = errors.New("leveldb/db: not found")

// Iterator iterates over a DB's key/value pairs in key order.
//
// An iterator must be closed after use, but it is not necessary to read an
// iterator until exhaustion.
//
// An iterator is not necessarily goroutine-safe, but it is safe to use
// multiple iterators concurrently, with each in a dedicated goroutine.
//
// It is also safe to use an iterator concurrently with modifying its
// underlying DB, if that DB permits modification. However, the resultant
// key/value pairs are not guaranteed to be a consistent snapshot of that DB
// at a particular point in time.
type Iterator interface {
	// Next moves the iterator to the next key/value pair.
	// It returns whether the iterator is exhausted.
	Next() bool

	// Key returns the key of the current key/value pair, or nil if done.
	// The caller should not modify the contents of the returned slice, and
	// its contents may change on the next call to Next.
	Key() []byte

	// Value returns the value of the current key/value pair, or nil if done.
	// The caller should not modify the contents of the returned slice, and
	// its contents may change on the next call to Next.
	Value() []byte

	// Close closes the iterator and returns any accumulated error. Exhausting
	// all the key/value pairs in a table is not considered to be an error.
	// It is valid to call Close multiple times. Other methods should not be
	// called after the iterator has been closed.
	Close() error
}

// DB is a key/value store.
//
// It is safe to call Get and Find from concurrent goroutines. It is not
// necessarily safe to do so for Set and Delete.
//
// Some implementations may impose additional restrictions. For example:
//   - Set calls may need to be in increasing key order.
//   - a DB may be read-only or write-only.
type DB interface {
	// Get gets the value for the given key. It returns ErrNotFound if the DB
	// does not contain the key.
	//
	// The caller should not modify the contents of the returned slice, but
	// it is safe to modify the contents of the argument after Get returns.
	Get(key []byte, o *ReadOptions) (value []byte, err error)

	// Set sets the value for the given key. It overwrites any previous value
	// for that key; a DB is not a multi-map.
	//
	// It is safe to modify the contents of the arguments after Set returns.
	Set(key, value []byte, o *WriteOptions) error

	// Delete deletes the value for the given key. It returns ErrNotFound if
	// the DB does not contain the key.
	//
	// It is safe to modify the contents of the arguments after Delete returns.
	Delete(key []byte, o *WriteOptions) error

	// Find returns an iterator positioned before the first key/value pair
	// whose key is 'greater than or equal to' the given key. There may be no
	// such pair, in which case the iterator will return false on Next.
	//
	// Any error encountered will be implicitly returned via the iterator. An
	// error-iterator will yield no key/value pairs and closing that iterator
	// will return that error.
	//
	// It is safe to modify the contents of the argument after Find returns.
	Find(key []byte, o *ReadOptions) Iterator

	// Close closes the DB. It may or may not close any underlying io.Reader
	// or io.Writer, depending on how the DB was created.
	//
	// It is not safe to close a DB until all outstanding iterators are closed.
	// It is valid to call Close multiple times. Other methods should not be
	// called after the DB has been closed.
	Close() error
}
