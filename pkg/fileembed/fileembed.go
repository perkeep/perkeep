/*
Copyright 2011 Google Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

     http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// Package fileembed provides access to static data resources (images,
// HTML, css, etc) embedded into the binary with genfileembed.
//
// Most of the package contains internal details used by genfileembed.
// Normal applications will simply make a global Files variable.
package fileembed

import (
	"compress/zlib"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Files contains all the embedded resources.
type Files struct {
	// Optional environment variable key to override
	OverrideEnv string

	// Optional fallback directory to check, if not in memory.
	DirFallback string

	// SlurpToMemory controls whether on first access the file is
	// slurped into memory.  It's intended for use with DirFallback.
	SlurpToMemory bool

	// Listable controls whether requests for the http file "/" return
	// a directory of available files. Must be set to true for
	// http.FileServer to correctly handle requests for index.html.
	Listable bool

	lk   sync.Mutex
	file map[string]*staticFile
}

type staticFile struct {
	name     string
	contents []byte
	modtime  time.Time
}

type Opener interface {
	Open() (io.Reader, error)
}

type String string

func (s String) Open() (io.Reader, error) {
	return strings.NewReader(string(s)), nil
}

// ZlibCompressed is used to store a compressed file.
type ZlibCompressed string

func (zb ZlibCompressed) Open() (io.Reader, error) {
	rz, err := zlib.NewReader(strings.NewReader(string(zb)))
	if err != nil {
		return nil, fmt.Errorf("Could not open ZlibCompressed: %v", err)
	}
	return rz, nil
}

// ZlibCompressedBase64 is used to store a compressed file.
// Unlike ZlibCompressed, the string is base64 encoded,
// in standard base64 encoding.
type ZlibCompressedBase64 string

func (zb ZlibCompressedBase64) Open() (io.Reader, error) {
	rz, err := zlib.NewReader(base64.NewDecoder(base64.StdEncoding, strings.NewReader(string(zb))))
	if err != nil {
		return nil, fmt.Errorf("Could not open ZlibCompressedBase64: %v", err)
	}
	return rz, nil
}

// Multi concatenates multiple Openers into one, like io.MultiReader.
func Multi(openers ...Opener) Opener {
	return multi(openers)
}

type multi []Opener

func (m multi) Open() (io.Reader, error) {
	rs := make([]io.Reader, 0, len(m))
	for _, o := range m {
		r, err := o.Open()
		if err != nil {
			return nil, err
		}
		rs = append(rs, r)
	}
	return io.MultiReader(rs...), nil
}

// Add adds a file to the file set.
func (f *Files) Add(filename string, size int64, modtime time.Time, o Opener) {
	f.lk.Lock()
	defer f.lk.Unlock()

	r, err := o.Open()
	if err != nil {
		log.Printf("Could not add file %v: %v", filename, err)
		return
	}
	contents, err := ioutil.ReadAll(r)
	if err != nil {
		log.Printf("Could not read contents of file %v: %v", filename, err)
		return
	}

	f.add(filename, &staticFile{
		name:     filename,
		contents: contents,
		modtime:  modtime,
	})
}

// f.lk must be locked
func (f *Files) add(filename string, sf *staticFile) {
	if f.file == nil {
		f.file = make(map[string]*staticFile)
	}
	f.file[filename] = sf
}

var _ http.FileSystem = (*Files)(nil)

func (f *Files) Open(filename string) (hf http.File, err error) {
	// don't bother locking f.lk here, because Listable will normally be set on initialization
	if filename == "/" && f.Listable {
		return openDir(f)
	}
	filename = strings.TrimLeft(filename, "/")
	if e := f.OverrideEnv; e != "" && os.Getenv(e) != "" {
		diskPath := filepath.Join(os.Getenv(e), filename)
		return os.Open(diskPath)
	}
	f.lk.Lock()
	defer f.lk.Unlock()
	sf, ok := f.file[filename]
	if !ok {
		return f.openFallback(filename)
	}
	return &fileHandle{sf: sf}, nil
}

// f.lk is held
func (f *Files) openFallback(filename string) (http.File, error) {
	if f.DirFallback == "" {
		return nil, os.ErrNotExist
	}
	of, err := os.Open(filepath.Join(f.DirFallback, filename))
	switch {
	case err != nil:
		return nil, err
	case f.SlurpToMemory:
		defer of.Close()
		bs, err := ioutil.ReadAll(of)
		if err != nil {
			return nil, err
		}
		fi, err := of.Stat()

		sf := &staticFile{
			name:     filename,
			contents: bs,
			modtime:  fi.ModTime(),
		}
		f.add(filename, sf)
		return &fileHandle{sf: sf}, nil
	}
	return of, nil
}

type fileHandle struct {
	sf     *staticFile
	off    int64
	closed bool
}

var _ http.File = (*fileHandle)(nil)

func (f *fileHandle) Close() error {
	if f.closed {
		return os.ErrInvalid
	}
	f.closed = true
	return nil
}

func (f *fileHandle) Read(p []byte) (n int, err error) {
	if f.off >= int64(len(f.sf.contents)) {
		return 0, io.EOF
	}
	n = copy(p, f.sf.contents[f.off:])
	f.off += int64(n)
	return
}

func (f *fileHandle) Readdir(int) ([]os.FileInfo, error) {
	return nil, errors.New("not directory")
}

func (f *fileHandle) Seek(offset int64, whence int) (int64, error) {
	switch whence {
	case os.SEEK_SET:
		f.off = offset
	case os.SEEK_CUR:
		f.off += offset
	case os.SEEK_END:
		f.off = f.sf.Size() + offset
	default:
		return 0, os.ErrInvalid
	}
	if f.off < 0 {
		f.off = 0
	}
	return f.off, nil
}

func (f *fileHandle) Stat() (os.FileInfo, error) {
	return f.sf, nil
}

var _ os.FileInfo = (*staticFile)(nil)

func (f *staticFile) Name() string       { return f.name }
func (f *staticFile) Size() int64        { return int64(len(f.contents)) }
func (f *staticFile) Mode() os.FileMode  { return 0444 }
func (f *staticFile) ModTime() time.Time { return f.modtime }
func (f *staticFile) IsDir() bool        { return false }
func (f *staticFile) Sys() interface{}   { return nil }

func openDir(f *Files) (hf http.File, err error) {
	f.lk.Lock()
	defer f.lk.Unlock()

	allFiles := make([]os.FileInfo, 0, len(f.file))
	var dirModtime time.Time

	for filename, sfile := range f.file {
		if strings.Contains(filename, "/") {
			continue // skip child directories; we only support readdir on the rootdir for now
		}
		allFiles = append(allFiles, sfile)
		// a directory's modtime is the maximum contained modtime
		if sfile.modtime.After(dirModtime) {
			dirModtime = sfile.modtime
		}
	}

	return &dirHandle{
		sd:    &staticDir{name: "/", modtime: dirModtime},
		files: allFiles,
	}, nil
}

type dirHandle struct {
	sd    *staticDir
	files []os.FileInfo
	off   int
}

func (d *dirHandle) Readdir(n int) ([]os.FileInfo, error) {
	if n <= 0 {
		return d.files, nil
	}
	if d.off >= len(d.files) {
		return []os.FileInfo{}, io.EOF
	}

	if d.off+n > len(d.files) {
		n = len(d.files) - d.off
	}
	matches := d.files[d.off : d.off+n]
	d.off += n

	var err error
	if d.off > len(d.files) {
		err = io.EOF
	}

	return matches, err
}

func (d *dirHandle) Close() error                   { return nil }
func (d *dirHandle) Read(p []byte) (int, error)     { return 0, errors.New("not file") }
func (d *dirHandle) Seek(int64, int) (int64, error) { return 0, os.ErrInvalid }
func (d *dirHandle) Stat() (os.FileInfo, error)     { return d.sd, nil }

type staticDir struct {
	name    string
	modtime time.Time
}

func (d *staticDir) Name() string       { return d.name }
func (d *staticDir) Size() int64        { return 0 }
func (d *staticDir) Mode() os.FileMode  { return 0444 | os.ModeDir }
func (d *staticDir) ModTime() time.Time { return d.modtime }
func (d *staticDir) IsDir() bool        { return true }
func (d *staticDir) Sys() interface{}   { return nil }

// JoinStrings joins returns the concatentation of ss.
func JoinStrings(ss ...string) string {
	return strings.Join(ss, "")
}
