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
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path"
	"strings"
	"time"

	"camlistore.org/pkg/googlestorage"
	"camlistore.org/pkg/wkfs"
	"camlistore.org/third_party/github.com/bradfitz/gce"
)

// Max size for all files read or written. This filesystem is only
// supposed to be for configuration data only, so this is very
// generous.
const maxSize = 1 << 20

func init() {
	if !gce.OnGCE() {
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
	panic(fmt.Sprintf("OpenFile not implemented for %q flag=%d, perm=%d", name, flag, perm))
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
