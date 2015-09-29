/*
Copyright 2014 The Camlistore Authors

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

// Package gcs registers a Google Cloud Storage filesystem at the
// well-known /gcs/ filesystem path if the current machine is running
// on Google Compute Engine.
package gcs

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path"
	"strings"
	"sync"
	"time"

	"camlistore.org/pkg/googlestorage"
	"camlistore.org/pkg/wkfs"
	"google.golang.org/cloud/compute/metadata"
)

// Max size for all files read or written. This filesystem is only
// supposed to be for configuration data only, so this is very
// generous.
const maxSize = 1 << 20

func init() {
	if !metadata.OnGCE() {
		return
	}
	client, err := googlestorage.NewServiceClient()
	wkfs.RegisterFS("/gcs/", &gcsFS{client, err})
}

type gcsFS struct {
	client *googlestorage.Client
	err    error // sticky error
}

func (fs *gcsFS) parseName(name string) (bucket, key string, err error) {
	if fs.err != nil {
		return "", "", fs.err
	}
	name = strings.TrimPrefix(name, "/gcs/")
	i := strings.Index(name, "/")
	if i < 0 {
		return name, "", nil
	}
	return name[:i], name[i+1:], nil
}

func (fs *gcsFS) Open(name string) (wkfs.File, error) {
	bucket, key, err := fs.parseName(name)
	if err != nil {
		return nil, fs.err
	}
	rc, size, err := fs.client.GetObject(&googlestorage.Object{
		Bucket: bucket,
		Key:    key,
	})
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	if size > maxSize {
		return nil, fmt.Errorf("file %s too large (%d bytes) for /gcs/ filesystem", name, size)
	}
	slurp, err := ioutil.ReadAll(io.LimitReader(rc, size))
	if err != nil {
		return nil, err
	}
	return &file{
		name:   name,
		Reader: bytes.NewReader(slurp),
	}, nil
}

func (fs *gcsFS) Stat(name string) (os.FileInfo, error) { return fs.Lstat(name) }
func (fs *gcsFS) Lstat(name string) (os.FileInfo, error) {
	bucket, key, err := fs.parseName(name)
	if err != nil {
		return nil, err
	}
	size, exists, err := fs.client.StatObject(&googlestorage.Object{
		Bucket: bucket,
		Key:    key,
	})
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, os.ErrNotExist
	}
	return &statInfo{
		name: name,
		size: size,
	}, nil
}

func (fs *gcsFS) MkdirAll(path string, perm os.FileMode) error { return nil }

func (fs *gcsFS) OpenFile(name string, flag int, perm os.FileMode) (wkfs.FileWriter, error) {
	bucket, key, err := fs.parseName(name)
	if err != nil {
		return nil, err
	}
	switch flag {
	case os.O_WRONLY | os.O_CREATE | os.O_EXCL:
	case os.O_WRONLY | os.O_CREATE | os.O_TRUNC:
	default:
		return nil, fmt.Errorf("Unsupported OpenFlag flag mode %d on Google Cloud Storage", flag)
	}
	if flag&os.O_EXCL != 0 {
		if _, err := fs.Stat(name); err == nil {
			return nil, os.ErrExist
		}
	}
	return &fileWriter{
		fs:     fs,
		name:   name,
		bucket: bucket,
		key:    key,
		flag:   flag,
		perm:   perm,
	}, nil
}

type fileWriter struct {
	fs                *gcsFS
	name, bucket, key string
	flag              int
	perm              os.FileMode

	buf bytes.Buffer

	mu     sync.Mutex
	closed bool
}

func (w *fileWriter) Write(p []byte) (n int, err error) {
	if len(p)+w.buf.Len() > maxSize {
		return 0, &os.PathError{
			Op:   "Write",
			Path: w.name,
			Err:  errors.New("file too large"),
		}
	}
	return w.buf.Write(p)
}

func (w *fileWriter) Close() (err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return nil
	}
	w.closed = true
	return w.fs.client.PutObject(&googlestorage.Object{
		Bucket: w.bucket,
		Key:    w.key,
	}, ioutil.NopCloser(bytes.NewReader(w.buf.Bytes())))
}

type statInfo struct {
	name    string
	size    int64
	isDir   bool
	modtime time.Time
}

func (si *statInfo) IsDir() bool        { return si.isDir }
func (si *statInfo) ModTime() time.Time { return si.modtime }
func (si *statInfo) Mode() os.FileMode  { return 0644 }
func (si *statInfo) Name() string       { return path.Base(si.name) }
func (si *statInfo) Size() int64        { return si.size }
func (si *statInfo) Sys() interface{}   { return nil }

type file struct {
	name string
	*bytes.Reader
}

func (*file) Close() error   { return nil }
func (f *file) Name() string { return path.Base(f.name) }
func (f *file) Stat() (os.FileInfo, error) {
	panic("Stat not implemented on /gcs/ files yet")
}
