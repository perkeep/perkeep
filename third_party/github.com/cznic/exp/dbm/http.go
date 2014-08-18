// Copyright 2014 The dbm Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// http VFS support

package dbm

import (
	"fmt"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"time"
)

// HttpDir returns an object implementing http.FileSystem using the DB file
// system restricted to a specific directory tree.
//
// 'root' must be an absolute path beginning with '/'.
func (db *DB) HttpDir(root string) http.FileSystem {
	dir := filepath.Clean(root)
	if dir == "." {
		dir = "/"
	}
	if dir[0] != '/' {
		return &httpFileSystem{err: fmt.Errorf("HttpDir: invalid root %q", dir)}
	}

	return &httpFileSystem{db: db, root: dir}
}

type httpFileSystem struct {
	db   *DB
	err  error
	root string
}

// Implements http.FileSystem
func (fs *httpFileSystem) Open(name string) (r http.File, err error) {
	if err = fs.err; err != nil {
		return
	}

	var f File
	if f, err = fs.db.File(filepath.Join(fs.root, path.Clean("/"+name))); err != nil {
		return
	}

	return &httpFile{f: f}, nil
}

type httpFile struct {
	closed bool
	f      File
	fp     int64
}

// Implements http.File
func (f *httpFile) Close() error {
	f.closed = true
	return nil
}

// Implements http.File
func (f *httpFile) Stat() (os.FileInfo, error) {
	return f, nil
}

// Implements http.File
func (f *httpFile) Readdir(count int) ([]os.FileInfo, error) {
	panic("TODO")
}

// Implements http.File
func (f *httpFile) Read(b []byte) (n int, err error) {
	n, err = f.f.ReadAt(b, f.fp)
	f.fp += int64(n)
	return
}

// Implements http.File
func (f *httpFile) Seek(offset int64, whence int) (int64, error) {
	panic("TODO")
}

// Implements os.FileInfo
func (f *httpFile) Name() string {
	return f.f.Name()
}

// Implements os.FileInfo
func (f *httpFile) Size() int64 {
	sz, err := f.f.Size()
	if err != nil {
		return 0
	}

	return sz
}

// Implements os.FileInfo
func (f *httpFile) Mode() os.FileMode {
	panic("TODO")
}

// Implements os.FileInfo
func (f *httpFile) ModTime() time.Time {
	return time.Now()
}

// Implements os.FileInfo
func (f *httpFile) IsDir() bool {
	return false
}

// Implements os.FileInfo
func (f *httpFile) Sys() interface{} {
	panic("TODO")
}
