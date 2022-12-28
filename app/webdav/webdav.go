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
	"strings"
	"sync"
	"time"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/client"
	"perkeep.org/pkg/schema"
	"perkeep.org/pkg/schema/nodeattr"
	"perkeep.org/pkg/search"
	"perkeep.org/pkg/types/camtypes"

	"golang.org/x/net/webdav"
)

type webdavFs struct {
	root   *webdavFsNode
	client *client.Client
}

func newWebDavFS(c *client.Client, br blob.Ref) (*webdavFs, error) {
	return &webdavFs{client: c, root: &webdavFsNode{br: br, sub: make(map[string]*webdavFsNode)}}, nil
}

var (
	_ webdav.FileSystem = (*webdavFs)(nil)
)

// fs is read only
func (fs *webdavFs) Mkdir(ctx context.Context, name string, perm os.FileMode) error {
	return os.ErrPermission
}

// fs is read only
func (fs *webdavFs) RemoveAll(ctx context.Context, name string) error {
	return os.ErrPermission
}

// fs is read only
func (fs *webdavFs) Rename(ctx context.Context, oldName, newName string) error {
	return os.ErrPermission
}

func (fs *webdavFs) Stat(ctx context.Context, name string) (os.FileInfo, error) {
	f, err := fs.OpenFile(ctx, name, os.O_RDONLY, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	return f.Stat()
}

func (fs *webdavFs) OpenFile(ctx context.Context, name string, flag int, perm os.FileMode) (webdav.File, error) {
	if flag != os.O_RDONLY {
		return nil, os.ErrPermission
	}
	parts := splitIntoParts(name)

	n := fs.root
	if err := fs.refreshRoot(ctx, n); err != nil {
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

func (fs *webdavFs) openFile(ctx context.Context, n *webdavFsNode) (*webdavFsFile, error) {
	if n.fi.IsDir() {
		dentries := make([]os.FileInfo, 0)
		for _, v := range n.sub {
			if err := fs.refresh(ctx, v); err != nil {
				return nil, fmt.Errorf("unable to refresh fs node: %w", err)
			}
			dentries = append(dentries, webdavFsFileInfo{
				isDir:   v.fi.IsDir(),
				name:    v.fi.Name(),
				size:    v.fi.Size(),
				mode:    0400,
				modTime: v.fi.ModTime(),
			})
		}
		return &webdavFsFile{n: n, dentries: dentries}, nil
	} else {
		r, err := schema.NewFileReader(ctx, fs.client, n.br)
		if err != nil {
			return nil, fmt.Errorf("unable to open file to read: %w", err)
		}
		return &webdavFsFile{n: n, r: r}, nil
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

type webdavFsNode struct {
	br blob.Ref

	mu  sync.Mutex
	fi  os.FileInfo
	sub map[string]*webdavFsNode

	// cache invalidation data
	static        bool
	lastRefreshed time.Time
}

var refreshInterval = 1 * time.Minute

func needsRefresh(n *webdavFsNode) bool {
	return !n.static && time.Now().After(n.lastRefreshed.Add(refreshInterval))
}

func (fs *webdavFs) refreshRoot(ctx context.Context, n *webdavFsNode) error {
	n.mu.Lock()
	defer n.mu.Unlock()

	if !needsRefresh(n) {
		return nil
	}

	des, err := fs.client.Describe(ctx, &search.DescribeRequest{BlobRef: n.br, Depth: 3})
	if err != nil {
		return fmt.Errorf("unable to describe blob ref %s: %w", n.br, err)
	}
	db := des.Meta.Get(n.br)

	if db.CamliType != schema.TypePermanode {
		return fmt.Errorf("root %s should be a permanode", n.br)
	}

	sub := make(map[string]*webdavFsNode, 0)
	for k := range db.Permanode.Attr {
		if !strings.HasPrefix(k, nodeattr.CamliPathColon) {
			continue
		}
		cb := blob.ParseOrZero(db.Permanode.Attr.Get(k))
		if !cb.Valid() {
			continue
		}
		name := strings.TrimPrefix(k, nodeattr.CamliPathColon)
		dbm := des.Meta.Get(cb)
		sub[name] = &webdavFsNode{br: dbm.BlobRef}
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

	n.static = false
	n.sub = sub
	n.fi = webdavFsFileInfo{
		isDir:   true,
		name:    "/",
		mode:    0400,
		size:    int64(len(n.sub)),
		modTime: time.Now(),
	}
	n.lastRefreshed = time.Now()
	return nil
}

func (fs *webdavFs) refresh(ctx context.Context, n *webdavFsNode) error {
	n.mu.Lock()
	defer n.mu.Unlock()

	if !needsRefresh(n) {
		return nil
	}

	des, err := fs.client.Describe(ctx, &search.DescribeRequest{BlobRef: n.br, Depth: 3})
	if err != nil {
		return fmt.Errorf("unable to describe blob ref %s: %w", n.br, err)
	}
	db := des.Meta.Get(n.br)

	switch db.CamliType {
	case schema.TypePermanode:
		// TODO
		return fmt.Errorf("unable to refresh permanodes %s: %w", n.br, err)
	case schema.TypeFile:
		n.static = true
		n.fi = webdavFsFileInfo{
			isDir:   false,
			name:    db.File.FileName,
			size:    db.File.Size,
			mode:    0400,
			modTime: modtimeFromFileInfo(db.File),
		}
	case schema.TypeDirectory:
		n.static = true
		n.fi = webdavFsFileInfo{
			isDir:   true,
			name:    db.Dir.FileName,
			size:    db.Dir.Size,
			mode:    0400,
			modTime: modtimeFromFileInfo(db.Dir),
		}
		n.sub = make(map[string]*webdavFsNode)
		for _, m := range db.DirMembers() {
			dmc := des.Meta.Get(m.BlobRef)
			var fi *camtypes.FileInfo
			if dmc.File != nil {
				fi = dmc.File
			} else if dmc.Dir != nil {
				fi = dmc.Dir
			}
			if fi == nil {
				continue
			}
			n.sub[fi.FileName] = &webdavFsNode{br: m.BlobRef}
		}
	}
	n.lastRefreshed = time.Now()
	return nil
}

type webdavFsFile struct {
	n   *webdavFsNode
	pos int

	// file
	r *schema.FileReader

	// dir
	dentries []os.FileInfo
}

var (
	_ webdav.File = (*webdavFsFile)(nil)
)

func (f *webdavFsFile) isDir() bool {
	return f.r == nil
}

func (f *webdavFsFile) Seek(offset int64, whence int) (int64, error) {
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

func (f *webdavFsFile) ETag(ctx context.Context) (string, error) {
	return f.n.br.Digest(), nil
}

func (f *webdavFsFile) Read(p []byte) (int, error) {
	if f.isDir() {
		return 0, os.ErrInvalid
	}
	n, err := f.r.ReadAt(p, int64(f.pos))
	f.pos += n
	return n, err
}

func (f *webdavFsFile) Write(p []byte) (int, error) {
	return 0, os.ErrPermission
}

func (f *webdavFsFile) Close() error {
	if f.isDir() {
		return nil
	}
	return f.r.Close()
}

func (f *webdavFsFile) Readdir(count int) ([]os.FileInfo, error) {
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

func (f *webdavFsFile) Stat() (os.FileInfo, error) {
	return f.n.fi, nil
}

type webdavFsFileInfo struct {
	isDir bool

	name    string
	size    int64
	mode    os.FileMode
	modTime time.Time
}

func (fi webdavFsFileInfo) IsDir() bool {
	return fi.isDir
}

func (fi webdavFsFileInfo) Name() string {
	return fi.name
}

func (fi webdavFsFileInfo) Size() int64 {
	return fi.size
}

func (fi webdavFsFileInfo) Mode() os.FileMode {
	return fi.mode
}

func (fi webdavFsFileInfo) ModTime() time.Time {
	return fi.modTime
}

func (fi webdavFsFileInfo) Sys() interface{} {
	return nil
}
