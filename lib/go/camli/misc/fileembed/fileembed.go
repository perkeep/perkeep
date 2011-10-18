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
	"fmt"
	"http"
	"io/ioutil"
	"os"
	"path/filepath"
	"sync"
)

var binaryModTime = statBinaryModTime()

type Files struct {
	// Optional environment variable key to override
	OverrideEnv string

	// Optional fallback directory to check, if not in memory.
	DirFallback string

	// SlurpToMemory controls whether on first access the file is
	// slurped into memory.  It's intended for use with DirFallback.
	SlurpToMemory bool

	lk   sync.Mutex
	file map[string]string
}

// Add adds a file to the file set.
func (f *Files) Add(filename, body string) {
	f.lk.Lock()
	defer f.lk.Unlock()
	f.add(filename, body)
}

// f.lk must be locked
func (f *Files) add(filename, body string) {
	if f.file == nil {
		f.file = make(map[string]string)
	}
	f.file[filename] = body
}

func (f *Files) Open(filename string) (http.File, os.Error) {
	if e := f.OverrideEnv; e != "" && os.Getenv(e) != "" {
		return os.Open(filepath.Join(os.Getenv(e), filename))
	}
	f.lk.Lock()
	defer f.lk.Unlock()
	s, ok := f.file[filename]
	if !ok {
		return f.openFallback(filename)
	}
	return &file{name: filename, s: s}, nil
}

// f.lk is held
func (f *Files) openFallback(filename string) (http.File, os.Error) {
	if f.DirFallback == "" {
		return nil, os.ENOENT
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
		s := string(bs)
		f.add(filename, s)
		return &file{name: filename, s: s}, nil
	}
	return of, nil
}

type file struct {
	name string
	s    string

	off    int64
	closed bool
}

func (f *file) Close() os.Error {
	if f.closed {
		return os.EINVAL
	}
	f.closed = true
	return nil
}

func (f *file) Read(p []byte) (n int, err os.Error) {
	if f.off >= int64(len(f.s)) {
		return 0, os.EOF
	}
	n = copy(p, f.s[f.off:])
	f.off += int64(n)
	return
}

func (f *file) Readdir(int) ([]os.FileInfo, os.Error) {
	return nil, os.ENOTDIR
}

func (f *file) Seek(offset int64, whence int) (int64, os.Error) {
	switch whence {
	case os.SEEK_SET:
		f.off = offset
	case os.SEEK_CUR:
		f.off += offset
	case os.SEEK_END:
		f.off = int64(len(f.s)) + offset
	default:
		return 0, os.EINVAL
	}
	if f.off < 0 {
		f.off = 0
	}
	return f.off, nil
}

func (f *file) Stat() (*os.FileInfo, os.Error) {
	// Break dependency on syscall module for App Engine.
	const syscall_S_IFREG = 0x8000
	fi := &os.FileInfo{
		Mode:     0444 | syscall_S_IFREG,
		Name:     f.name,
		Size:     int64(len(f.s)),
		Atime_ns: binaryModTime,
		Mtime_ns: binaryModTime,
		Ctime_ns: binaryModTime,
	}
	return fi, nil
}

func statBinaryModTime() int64 {
	fi, err := os.Stat(os.Args[0])
	if err != nil {
		panic(fmt.Sprintf("Failed to stat binary %q: %v", os.Args[0], err))
	}
	return fi.Mtime_ns
}
