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

package fileembed

import (
	"errors"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type Files struct {
	// Optional environment variable key to override
	OverrideEnv string

	// Optional fallback directory to check, if not in memory.
	DirFallback string

	// SlurpToMemory controls whether on first access the file is
	// slurped into memory.  It's intended for use with DirFallback.
	SlurpToMemory bool

	lk   sync.Mutex
	file map[string]*staticFile
}

type staticFile struct {
	name     string
	contents string
	modtime  time.Time
}

// Add adds a file to the file set.
func (f *Files) Add(filename, contents string, modtime time.Time) {
	f.lk.Lock()
	defer f.lk.Unlock()
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

func (f *Files) Open(filename string) (http.File, error) {
	if e := f.OverrideEnv; e != "" && os.Getenv(e) != "" {
		return os.Open(filepath.Join(os.Getenv(e), filename))
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

		s := string(bs)
		sf := &staticFile{
			name:     filename,
			contents: s,
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
		f.off = int64(len(f.sf.contents)) + offset
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
