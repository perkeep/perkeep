/*
Copyright 2013 Google Inc.

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

package closure

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"path"
	"strings"
	"sync"
	"time"
)

// ZipData is either the empty string (when compiling with "go get",
// or the devcam server), or is initialized to a base64-encoded zip file
// of the Closure library (when using make.go, which puts an extra
// file in this package containing an init function to set ZipData).
var ZipData string
var ZipModTime time.Time

func FileSystem() (http.FileSystem, error) {
	if ZipData == "" {
		return nil, os.ErrNotExist
	}
	zr, err := zip.NewReader(strings.NewReader(ZipData), int64(len(ZipData)))
	if err != nil {
		return nil, err
	}
	m := make(map[string]*fileInfo)
	for _, zf := range zr.File {
		if !strings.HasPrefix(zf.Name, "closure/") {
			continue
		}
		fi, err := newFileInfo(zf)
		if err != nil {
			return nil, fmt.Errorf("Error reading zip file %q: %v", zf.Name, err)
		}
		m[strings.TrimPrefix(zf.Name, "closure")] = fi
	}
	return &fs{zr, m}, nil

}

type fs struct {
	zr *zip.Reader
	m  map[string]*fileInfo // keyed by what Open gets. see Open's comment.
}

var nopCloser = ioutil.NopCloser(nil)

// Open is called with names like "/goog/base.js", but the zip contains Files named like "closure/goog/base.js".
func (s *fs) Open(name string) (http.File, error) {
	fi, ok := s.m[name]
	if !ok {
		return nil, os.ErrNotExist
	}
	return &file{fileInfo: fi}, nil
}

// a file is an http.File, wrapping a *fileInfo with a lazily-constructed SectionReader.
type file struct {
	*fileInfo
	once sync.Once // for making the SectionReader
	sr   *io.SectionReader
}

func (f *file) Read(p []byte) (n int, err error) {
	f.once.Do(f.initReader)
	return f.sr.Read(p)
}

func (f *file) Seek(offset int64, whence int) (ret int64, err error) {
	f.once.Do(f.initReader)
	return f.sr.Seek(offset, whence)
}

func (f *file) initReader() {
	f.sr = io.NewSectionReader(f.fileInfo.ra, 0, f.Size())
}

func newFileInfo(zf *zip.File) (*fileInfo, error) {
	rc, err := zf.Open()
	if err != nil {
		return nil, err
	}
	all, err := ioutil.ReadAll(rc)
	if err != nil {
		return nil, err
	}
	rc.Close()
	return &fileInfo{
		fullName: zf.Name,
		regdata:  all,
		Closer:   nopCloser,
		ra:       bytes.NewReader(all),
	}, nil
}

type fileInfo struct {
	fullName string
	regdata  []byte      // non-nil if regular file
	ra       io.ReaderAt // over regdata
	io.Closer
}

func (f *fileInfo) IsDir() bool                { return f.regdata == nil }
func (f *fileInfo) Size() int64                { return int64(len(f.regdata)) }
func (f *fileInfo) ModTime() time.Time         { return ZipModTime }
func (f *fileInfo) Name() string               { return path.Base(f.fullName) }
func (f *fileInfo) Stat() (os.FileInfo, error) { return f, nil }
func (f *fileInfo) Sys() interface{}           { return nil }

func (f *fileInfo) Readdir(count int) ([]os.FileInfo, error) {
	// TODO: implement.
	return nil, errors.New("TODO")
}

func (f *fileInfo) Mode() os.FileMode {
	if f.IsDir() {
		return 0755 | os.ModeDir
	}
	return 0644
}
