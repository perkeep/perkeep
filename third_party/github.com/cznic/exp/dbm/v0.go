// Copyright 2014 The dbm Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package dbm

import (
	"os"

	"camlistore.org/third_party/github.com/cznic/exp/lldb"
)

func open00(name string, in *DB) (db *DB, err error) {
	db = in
	if db.alloc, err = lldb.NewAllocator(lldb.NewInnerFiler(db.filer, 16), &lldb.Options{}); err != nil {
		return nil, &os.PathError{Op: "dbm.Open", Path: name, Err: err}
	}

	db.alloc.Compress = compress
	db.emptySize = 128

	return db, db.boot()
}
