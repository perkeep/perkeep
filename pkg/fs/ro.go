//go:build linux || darwin
// +build linux darwin

/*
Copyright 2013 The Perkeep Authors

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

package fs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"bazil.org/fuse"
	"bazil.org/fuse/fs"
	"go4.org/types"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/schema"
	"perkeep.org/pkg/search"
)

// roDir is a read-only directory.
// Its permanode is the permanode with camliPath:entname attributes.
type roDir struct {
	fs        *CamliFileSystem
	permanode blob.Ref
	parent    *roDir // or nil, if the root within its roots.go root.
	name      string // ent name (base name within parent)
	at        time.Time

	mu       sync.Mutex
	children map[string]roFileOrDir
	xattrs   map[string][]byte
}

var _ fs.Node = (*roDir)(nil)
var _ fs.HandleReadDirAller = (*roDir)(nil)
var _ fs.NodeGetxattrer = (*roDir)(nil)
var _ fs.NodeListxattrer = (*roDir)(nil)

func newRODir(fs *CamliFileSystem, permanode blob.Ref, name string, at time.Time) *roDir {
	return &roDir{
		fs:        fs,
		permanode: permanode,
		name:      name,
		at:        at,
	}
}

// for debugging
func (n *roDir) fullPath() string {
	if n == nil {
		return ""
	}
	return filepath.Join(n.parent.fullPath(), n.name)
}

func (n *roDir) Attr(ctx context.Context, a *fuse.Attr) error {
	*a = fuse.Attr{
		Inode: n.permanode.Sum64(),
		Mode:  os.ModeDir | 0500,
		Uid:   uint32(os.Getuid()),
		Gid:   uint32(os.Getgid()),
	}
	return nil
}

// populate hits the blobstore to populate map of child nodes.
func (n *roDir) populate() error {
	n.mu.Lock()
	defer n.mu.Unlock()
	ctx := context.TODO()
	// Things never change here, so if we've ever populated, we're
	// populated.
	if n.children != nil {
		return nil
	}

	Logger.Printf("roDir.populate(%q) - Sending request At %v", n.fullPath(), n.at)

	res, err := n.fs.client.Describe(ctx, &search.DescribeRequest{
		BlobRef: n.permanode,
		Depth:   3,
		At:      types.Time3339(n.at),
	})
	if err != nil {
		Logger.Println("roDir.paths:", err)
		return nil
	}
	db := res.Meta[n.permanode.String()]
	if db == nil {
		return errors.New("dir blobref not described")
	}

	// Find all child permanodes and stick them in n.children
	n.children = make(map[string]roFileOrDir)
	for k, v := range db.Permanode.Attr {
		const p = "camliPath:"
		if !strings.HasPrefix(k, p) || len(v) < 1 {
			continue
		}
		name := k[len(p):]
		childRef := v[0]
		child := res.Meta[childRef]
		if child == nil {
			Logger.Printf("child not described: %v", childRef)
			continue
		}
		if target := child.Permanode.Attr.Get("camliSymlinkTarget"); target != "" {
			// This is a symlink.
			n.children[name] = &roFile{
				fs:        n.fs,
				permanode: blob.ParseOrZero(childRef),
				parent:    n,
				name:      name,
				symLink:   true,
				target:    target,
			}
		} else if isDir(child.Permanode) {
			// This is a directory.
			n.children[name] = &roDir{
				fs:        n.fs,
				permanode: blob.ParseOrZero(childRef),
				parent:    n,
				name:      name,
				at:        n.at,
			}
		} else if contentRef := child.Permanode.Attr.Get("camliContent"); contentRef != "" {
			// This is a file.
			content := res.Meta[contentRef]
			if content == nil {
				Logger.Printf("child content not described: %v", childRef)
				continue
			}
			if content.CamliType != "file" {
				Logger.Printf("child not a file: %v", childRef)
				continue
			}
			n.children[name] = &roFile{
				fs:        n.fs,
				permanode: blob.ParseOrZero(childRef),
				parent:    n,
				name:      name,
				content:   blob.ParseOrZero(contentRef),
				size:      content.File.Size,
			}
		} else {
			// unknown type
			continue
		}
		n.children[name].xattr().load(child.Permanode)
	}
	return nil
}

func (n *roDir) ReadDirAll(ctx context.Context) ([]fuse.Dirent, error) {
	if err := n.populate(); err != nil {
		Logger.Println("populate:", err)
		return nil, fuse.EIO
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	var ents []fuse.Dirent
	for name, childNode := range n.children {
		var ino uint64
		switch v := childNode.(type) {
		case *roDir:
			ino = v.permanode.Sum64()
		case *roFile:
			ino = v.permanode.Sum64()
		default:
			Logger.Printf("roDir.ReadDirAll: unknown child type %T", childNode)
		}

		// TODO: figure out what Dirent.Type means.
		// fuse.go says "Type uint32 // ?"
		dirent := fuse.Dirent{
			Name:  name,
			Inode: ino,
		}
		Logger.Printf("roDir(%q) appending inode %x, %+v", n.fullPath(), dirent.Inode, dirent)
		ents = append(ents, dirent)
	}
	return ents, nil
}

var _ fs.NodeStringLookuper = (*roDir)(nil)

func (n *roDir) Lookup(ctx context.Context, name string) (ret fs.Node, err error) {
	defer func() {
		Logger.Printf("roDir(%q).Lookup(%q) = %#v, %v", n.fullPath(), name, ret, err)
	}()
	if err := n.populate(); err != nil {
		Logger.Println("populate:", err)
		return nil, fuse.EIO
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if n2 := n.children[name]; n2 != nil {
		return n2, nil
	}
	return nil, fuse.ENOENT
}

// roFile is a read-only file, or symlink.
type roFile struct {
	fs        *CamliFileSystem
	permanode blob.Ref
	parent    *roDir
	name      string // ent name (base name within parent)

	mu           sync.Mutex // protects all following fields
	symLink      bool       // if true, is a symlink
	target       string     // if a symlink
	content      blob.Ref   // if a regular file
	size         int64
	mtime, atime time.Time // if zero, use serverStart
	xattrs       map[string][]byte
}

var _ fs.Node = (*roFile)(nil)
var _ fs.NodeGetxattrer = (*roFile)(nil)
var _ fs.NodeListxattrer = (*roFile)(nil)
var _ fs.NodeSetxattrer = (*roFile)(nil)
var _ fs.NodeRemovexattrer = (*roFile)(nil)
var _ fs.NodeOpener = (*roFile)(nil)
var _ fs.NodeFsyncer = (*roFile)(nil)
var _ fs.NodeReadlinker = (*roFile)(nil)

func (n *roDir) Getxattr(ctx context.Context, req *fuse.GetxattrRequest, res *fuse.GetxattrResponse) error {
	return n.xattr().get(req, res)
}

func (n *roDir) Listxattr(ctx context.Context, req *fuse.ListxattrRequest, res *fuse.ListxattrResponse) error {
	return n.xattr().list(req, res)
}

func (n *roFile) Getxattr(ctx context.Context, req *fuse.GetxattrRequest, res *fuse.GetxattrResponse) error {
	return n.xattr().get(req, res)
}

func (n *roFile) Listxattr(ctx context.Context, req *fuse.ListxattrRequest, res *fuse.ListxattrResponse) error {
	return n.xattr().list(req, res)
}

func (n *roFile) Removexattr(ctx context.Context, req *fuse.RemovexattrRequest) error {
	return fuse.EPERM
}

func (n *roFile) Setxattr(ctx context.Context, req *fuse.SetxattrRequest) error {
	return fuse.EPERM
}

// for debugging
func (n *roFile) fullPath() string {
	if n == nil {
		return ""
	}
	return filepath.Join(n.parent.fullPath(), n.name)
}

func (n *roFile) Attr(ctx context.Context, a *fuse.Attr) error {
	// TODO: don't grab n.mu three+ times in here.
	var mode os.FileMode = 0400 // read-only

	n.mu.Lock()
	size := n.size
	var blocks uint64
	if size > 0 {
		blocks = uint64(size)/512 + 1
	}
	inode := n.permanode.Sum64()
	if n.symLink {
		mode |= os.ModeSymlink
	}
	n.mu.Unlock()

	*a = fuse.Attr{
		Inode:  inode,
		Mode:   mode,
		Uid:    uint32(os.Getuid()),
		Gid:    uint32(os.Getgid()),
		Size:   uint64(size),
		Blocks: blocks,
		Mtime:  n.modTime(),
		Atime:  n.accessTime(),
		Ctime:  serverStart,
		Crtime: serverStart,
	}
	return nil
}

func (n *roFile) accessTime() time.Time {
	n.mu.Lock()
	if !n.atime.IsZero() {
		defer n.mu.Unlock()
		return n.atime
	}
	n.mu.Unlock()
	return n.modTime()
}

func (n *roFile) modTime() time.Time {
	n.mu.Lock()
	defer n.mu.Unlock()
	if !n.mtime.IsZero() {
		return n.mtime
	}
	return serverStart
}

// Empirically:
//  open for read:   req.Flags == 0
//  open for append: req.Flags == 1
//  open for write:  req.Flags == 1
//  open for read/write (+<)   == 2 (bitmask? of?)
//
// open flags are O_WRONLY (1), O_RDONLY (0), or O_RDWR (2). and also
// bitmaks of O_SYMLINK (0x200000) maybe. (from
// fuse_filehandle_xlate_to_oflags in macosx/kext/fuse_file.h)
func (n *roFile) Open(ctx context.Context, req *fuse.OpenRequest, res *fuse.OpenResponse) (fs.Handle, error) {
	roFileOpen.Incr()

	if isWriteFlags(req.Flags) {
		return nil, fuse.EPERM
	}

	Logger.Printf("roFile.Open: %v: content: %v dir=%v flags=%v", n.permanode, n.content, req.Dir, req.Flags)
	r, err := schema.NewFileReader(ctx, n.fs.fetcher, n.content)
	if err != nil {
		roFileOpenError.Incr()
		Logger.Printf("roFile.Open: %v", err)
		return nil, fuse.EIO
	}

	// Turn off the OpenDirectIO bit (on by default in rsc fuse server.go),
	// else append operations don't work for some reason.
	res.Flags &= ^fuse.OpenDirectIO

	// Read-only.
	nod := &node{
		fs:      n.fs,
		blobref: n.content,
	}
	return &nodeReader{n: nod, fr: r}, nil
}

func (n *roFile) Fsync(ctx context.Context, r *fuse.FsyncRequest) error {
	// noop
	return nil
}

func (n *roFile) Readlink(ctx context.Context, req *fuse.ReadlinkRequest) (string, error) {
	Logger.Printf("roFile.Readlink(%q)", n.fullPath())
	n.mu.Lock()
	defer n.mu.Unlock()
	if !n.symLink {
		Logger.Printf("roFile.Readlink on node that's not a symlink?")
		return "", fuse.EIO
	}
	return n.target, nil
}

// roFileOrDir is a *roFile or *roDir
type roFileOrDir interface {
	fs.Node
	permanodeString() string
	xattr() *xattr
}

func (n *roFile) permanodeString() string {
	return n.permanode.String()
}

func (n *roDir) permanodeString() string {
	return n.permanode.String()
}

func (n *roFile) xattr() *xattr {
	return &xattr{"roFile", n.fs, n.permanode, &n.mu, &n.xattrs}
}

func (n *roDir) xattr() *xattr {
	return &xattr{"roDir", n.fs, n.permanode, &n.mu, &n.xattrs}
}
