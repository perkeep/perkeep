/*
Copyright 2022 The Perkeep Authors.

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

package main // import "perkeep.org/app/webdav"

import (
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"sync"
	"time"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/client"
	"perkeep.org/pkg/schema"
	"perkeep.org/pkg/search"
	"perkeep.org/pkg/types/camtypes"

	"golang.org/x/net/webdav"
)

type fs struct {
	root   *fsNode
	client *client.Client
}

func newFS(c *client.Client, br blob.Ref) (*fs, error) {
	return &fs{client: c, root: &fsNode{br: br, sub: make(map[string]*fsNode)}}, nil
}

var (
	_ webdav.FileSystem = (*fs)(nil)
)

// fs is read only
func (fs *fs) Mkdir(ctx context.Context, name string, perm os.FileMode) error {
	return os.ErrPermission
}

// fs is read only
func (fs *fs) RemoveAll(ctx context.Context, name string) error {
	return os.ErrPermission
}

// fs is read only
func (fs *fs) Rename(ctx context.Context, oldName, newName string) error {
	return os.ErrPermission
}

func (fs *fs) Stat(ctx context.Context, name string) (os.FileInfo, error) {
	f, err := fs.OpenFile(ctx, name, os.O_RDONLY, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	return f.Stat()
}

func (fs *fs) OpenFile(ctx context.Context, name string, flag int, perm os.FileMode) (webdav.File, error) {
	if flag != os.O_RDONLY {
		return nil, os.ErrPermission
	}
	parts := splitIntoParts(name)

	n := fs.root
	if err := fs.refresh(ctx, n); err != nil {
		return nil, fmt.Errorf("unable to refresh fs node: %w", err)
	}
	for i := range parts {
		c, ok := n.sub[parts[i]]
		if !ok {
			return nil, os.ErrNotExist
		}
		if err := fs.refresh(ctx, c); err != nil {
			return nil, fmt.Errorf("unable to refresh fs node: %w", err)
		}
		n = c
	}
	return fs.openFile(ctx, n)
}

func (fs *fs) openFile(ctx context.Context, n *fsNode) (*roFile, error) {
	if n.fi.IsDir() {
		dentries := make([]os.FileInfo, 0)
		for _, v := range n.sub {
			dentries = append(dentries, fileInfo{
				isDir:   v.fi.IsDir(),
				name:    v.fi.Name(),
				size:    v.fi.Size(),
				mode:    0400,
				modTime: v.fi.ModTime(),
			})
		}
		return &roFile{n: n, dentries: dentries}, nil
	} else {
		// static files or directories can use their blobref
		br := n.br
		// dynamic files or directories use their content blobref
		if n.cbr.Valid() {
			br = n.cbr
		}
		r, err := schema.NewFileReader(ctx, fs.client, br)
		if err != nil {
			return nil, fmt.Errorf("unable to open file to read: %w", err)
		}
		return &roFile{n: n, r: r}, nil
	}
}

func splitIntoParts(name string) []string {
	name = path.Clean(name)
	if name == "/" {
		return nil
	}
	s := make([]string, 0)
	for {
		dir, part := path.Split(name)
		if part == "." || part == "/" {
			break
		}
		s = append(s, part)
		name = path.Clean(dir)
	}
	for i, j := 0, len(s)-1; i < j; i, j = i+1, j-1 {
		s[i], s[j] = s[j], s[i]
	}
	return s
}

type fsNode struct {
	br  blob.Ref
	cbr blob.Ref

	mu  sync.Mutex
	fi  os.FileInfo
	sub map[string]*fsNode

	// cache invalidation data
	static        bool
	lastRefreshed time.Time
}

var refreshInterval = 1 * time.Minute

func (n *fsNode) needsRefreshLocked() bool {
	return !n.static && time.Now().After(n.lastRefreshed.Add(refreshInterval))
}

func (fs *fs) refresh(ctx context.Context, n *fsNode) error {
	n.mu.Lock()
	defer n.mu.Unlock()

	if !n.needsRefreshLocked() {
		return nil
	}

	des, err := fs.client.Describe(ctx, &search.DescribeRequest{BlobRef: n.br, Depth: 3})
	if err != nil {
		return fmt.Errorf("unable to describe blob ref %s: %w", n.br, err)
	}
	db := des.Meta.Get(n.br)

	fi, ok := fileInfoFromDescribedBlob(db, des.Meta)
	if !ok {
		return fmt.Errorf("unable to extract file info from described blob %s", n.br)
	}
	n.fi = fi

	// refresh child nodes
	switch db.CamliType {
	case schema.TypeFile:
		n.static = true
	case schema.TypeDirectory:
		n.static = true
		n.sub = make(map[string]*fsNode)
		for _, m := range db.DirMembers() {
			mfi, ok := fileInfoFromDescribedBlob(des.Meta.Get(m.BlobRef), des.Meta)
			if !ok {
				// TODO log + skip
				return fmt.Errorf("unable to extract file info directory child %s: %s", n.br, m.BlobRef)
			}
			n.sub[mfi.name] = &fsNode{br: m.BlobRef, fi: mfi}
		}
	case schema.TypePermanode:
		n.static = false
		if cbr, ok := db.ContentRef(); ok {
			cdb := des.Meta.Get(cbr)
			if cdb.CamliType != schema.TypeFile && cdb.CamliType != schema.TypeDirectory {
				return fmt.Errorf("only contentRef attributes of camliType 'file' or 'directory' are allowed: %s", n.br)
			}
			n.cbr = cbr
		}
		if db.Permanode.IsContainer() {
			sub := make(map[string]*fsNode, 0)
			for _, m := range db.Members() {
				mfi, ok := fileInfoFromDescribedBlob(des.Meta.Get(m.BlobRef), des.Meta)
				if !ok {
					// TODO log + skip
					return fmt.Errorf("unable to extract file info directory child %s: %s", n.br, m.BlobRef)
				}
				// TODO: camliPath:X where X might be different from "Title", names could collide
				sub[mfi.name] = &fsNode{br: m.BlobRef, fi: mfi}
			}
			for k, v := range sub {
				if c, ok := n.sub[k]; ok {
					if c.br == v.br {
						continue
					}
				}
				n.sub[k] = v
			}
			for k, v := range n.sub {
				if _, ok := sub[k]; !ok {
					sub[k] = v
				}
			}
		}
	}
	n.lastRefreshed = time.Now()
	return nil
}

// TODO: return error if we cannot calculate fileInfo
func fileInfoFromDescribedBlob(db *search.DescribedBlob, meta search.MetaMap) (fileInfo, bool) {
	switch db.CamliType {
	case schema.TypeFile:
		if db.File == nil {
			return fileInfo{}, false
		}
		return fileInfo{
			isDir:   false,
			name:    db.Title(),
			size:    db.File.Size,
			mode:    0400,
			modTime: modtimeFromFileInfo(db.File),
		}, true
	case schema.TypeDirectory:
		if db.Dir == nil {
			return fileInfo{}, false
		}
		return fileInfo{
			isDir:   true,
			name:    db.Title(),
			mode:    0400,
			modTime: modtimeFromFileInfo(db.Dir),
		}, true
	case schema.TypePermanode:
		if cbr, ok := db.ContentRef(); ok {
			cdb := meta.Get(cbr)
			if cdb.CamliType != schema.TypeFile && cdb.CamliType != schema.TypeDirectory {
				return fileInfo{}, false
			}
			return fileInfoFromDescribedBlob(cdb, meta)
		}
		if db.Permanode.IsContainer() {
			return fileInfo{
				isDir:   true,
				name:    db.Title(),
				mode:    0400,
				modTime: db.Permanode.ModTime,
			}, true
		}
	}
	return fileInfo{}, false
}

func modtimeFromFileInfo(fi *camtypes.FileInfo) time.Time {
	t := time.Now()
	if fi.Time != nil {
		t = fi.Time.Time()
	}
	if fi.ModTime != nil {
		t = fi.ModTime.Time()
	}
	return t
}

type roFile struct {
	n   *fsNode
	pos int

	// file
	r *schema.FileReader

	// dir
	dentries []os.FileInfo
}

var (
	_ webdav.File = (*roFile)(nil)
)

func (f *roFile) isDir() bool {
	return f.r == nil
}

func (f *roFile) Seek(offset int64, whence int) (int64, error) {
	npos := f.pos
	switch whence {
	case io.SeekStart:
		npos = int(offset)
	case io.SeekCurrent:
		npos += int(offset)
	case io.SeekEnd:
		npos = int(f.n.fi.Size()) + int(offset)
	default:
		npos = -1
	}
	if npos < 0 {
		return 0, os.ErrInvalid
	}
	f.pos = npos
	return int64(f.pos), nil
}

/*
TODO: this only works for static directories or files

func (f *roFile) ETag(ctx context.Context) (string, error) {
	return f.n.br.Digest(), nil
}
*/

func (f *roFile) Read(p []byte) (int, error) {
	if f.isDir() {
		return 0, os.ErrInvalid
	}
	n, err := f.r.ReadAt(p, int64(f.pos))
	f.pos += n
	return n, err
}

func (f *roFile) Write(p []byte) (int, error) {
	return 0, os.ErrPermission
}

func (f *roFile) Close() error {
	if f.isDir() {
		return nil
	}
	return f.r.Close()
}

func (f *roFile) Readdir(count int) ([]os.FileInfo, error) {
	if !f.isDir() {
		return nil, os.ErrInvalid
	}
	old := f.pos
	if old >= len(f.dentries) {
		if count > 0 {
			return nil, io.EOF
		}
		return nil, nil
	}
	if count > 0 {
		f.pos += count
		if f.pos > len(f.dentries) {
			f.pos = len(f.dentries)
		}
	} else {
		f.pos = len(f.dentries)
		old = 0
	}
	return f.dentries[old:f.pos], nil
}

func (f *roFile) Stat() (os.FileInfo, error) {
	return f.n.fi, nil
}

type fileInfo struct {
	isDir bool

	name    string
	size    int64
	mode    os.FileMode
	modTime time.Time
}

func (fi fileInfo) IsDir() bool {
	return fi.isDir
}

func (fi fileInfo) Name() string {
	return fi.name
}

func (fi fileInfo) Size() int64 {
	return fi.size
}

func (fi fileInfo) Mode() os.FileMode {
	return fi.mode
}

func (fi fileInfo) ModTime() time.Time {
	return fi.modTime
}

func (fi fileInfo) Sys() interface{} {
	return nil
}
