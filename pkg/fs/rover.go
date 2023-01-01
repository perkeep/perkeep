//go:build linux || darwin
// +build linux darwin

/*
Copyright 2014 The Perkeep Authors

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
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"bazil.org/fuse"
	"bazil.org/fuse/fs"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/schema"
	"perkeep.org/pkg/search"
)

// roVersionsDir is a fuse directory that represents
// the current state of a permanode directory.
// Unlike roDir, a file within roVersionsDir
// is presented as a directory (roFileVersionsDir) containing
// the different versions of the file (roFileVersion).
// It is read-only.
// Its permanode is the permanode with camliPath:entname attributes.
//
//	TODO: There might be a way to reuse roDir
type roVersionsDir struct {
	fs        *CamliFileSystem
	permanode blob.Ref
	parent    *roVersionsDir // or nil, if the parent is versionsDir
	name      string         // ent name (base name within parent)

	mu       sync.Mutex
	children map[string]roFileOrDir
	xattrs   map[string][]byte
}

func newROVersionsDir(fs *CamliFileSystem, permanode blob.Ref, name string) *roVersionsDir {
	return &roVersionsDir{
		fs:        fs,
		permanode: permanode,
		name:      name,
	}
}

// for debugging
func (n *roVersionsDir) fullPath() string {
	if n == nil {
		return ""
	}
	return filepath.Join(n.parent.fullPath(), n.name)
}

func (n *roVersionsDir) Attr(ctx context.Context, a *fuse.Attr) error {
	a.Mode = os.ModeDir | 0500
	a.Uid = uint32(os.Getuid())
	a.Gid = uint32(os.Getgid())
	a.Inode = n.permanode.Sum64()
	return nil
}

// populate hits the blobstore to populate map of child nodes.
func (n *roVersionsDir) populate(ctx context.Context) error {
	n.mu.Lock()
	defer n.mu.Unlock()

	// Things never change here, so if we've ever populated, we're
	// populated.
	if n.children != nil {
		return nil
	}

	Logger.Printf("roVersionsDir.populate(%q)", n.fullPath())

	res, err := n.fs.client.Describe(ctx, &search.DescribeRequest{
		BlobRef: n.permanode,
		Depth:   3,
	})
	if err != nil {
		Logger.Println("roVersionsDir.paths:", err)
		return fmt.Errorf("error while describing permanode: %w", err)
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
		if child.Permanode == nil {
			Logger.Printf("child Permanode is nil: %v", childRef)
			continue
		}
		if target := child.Permanode.Attr.Get("camliSymlinkTarget"); target != "" {
			// This is a symlink.
			n.children[name] = &roFileVersionsDir{
				fs:        n.fs,
				permanode: blob.ParseOrZero(childRef),
				parent:    n,
				name:      name,
				symLink:   true,
				target:    target,
			}
		} else if isDir(child.Permanode) {
			// This is a directory.
			n.children[name] = &roVersionsDir{
				fs:        n.fs,
				permanode: blob.ParseOrZero(childRef),
				parent:    n,
				name:      name,
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
			n.children[name] = &roFileVersionsDir{
				fs:        n.fs,
				permanode: blob.ParseOrZero(childRef),
				parent:    n,
				name:      name,
			}
		} else {
			// unknown type
			continue
		}
		n.children[name].xattr().load(child.Permanode)
	}
	return nil
}

func (n *roVersionsDir) ReadDir(ctx context.Context) ([]fuse.Dirent, error) {
	if err := n.populate(ctx); err != nil {
		Logger.Println("populate:", err)
		return nil, handleEIOorEINTR(err)
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	var ents []fuse.Dirent
	for name, childNode := range n.children {
		var ino uint64
		switch v := childNode.(type) {
		case *roVersionsDir:
			ino = v.permanode.Sum64()
		case *roFileVersion:
			ino = v.permanode.Sum64()
		default:
			Logger.Printf("roVersionsDir.ReadDir: unknown child type %T", childNode)
		}

		// TODO: figure out what Dirent.Type means.
		// fuse.go says "Type uint32 // ?"
		dirent := fuse.Dirent{
			Name:  name,
			Inode: ino,
		}
		Logger.Printf("roVersionsDir(%q) appending inode %x, %+v", n.fullPath(), dirent.Inode, dirent)
		ents = append(ents, dirent)
	}
	return ents, nil
}

func (n *roVersionsDir) Lookup(ctx context.Context, name string) (ret fs.Node, err error) {
	defer func() {
		Logger.Printf("roVersionsDir(%q).Lookup(%q) = %#v, %v", n.fullPath(), name, ret, err)
	}()
	if err := n.populate(ctx); err != nil {
		Logger.Println("populate:", err)
		return nil, handleEIOorEINTR(err)
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if n2 := n.children[name]; n2 != nil {
		return n2, nil
	}
	return nil, fuse.ENOENT
}

// roFileVersionsDir is a fuse directory that represents
// a permandode file. It contains the different versions
// of the file (roFileVersion).
// It is read-only.
type roFileVersionsDir struct {
	fs        *CamliFileSystem
	permanode blob.Ref
	parent    *roVersionsDir
	name      string // ent name (base name within parent)

	symLink bool   // if true, is a symlink
	target  string // if a symlink

	mu       sync.Mutex
	children map[string]roFileOrDir
	xattrs   map[string][]byte
}

// for debugging
func (n *roFileVersionsDir) fullPath() string {
	if n == nil {
		return ""
	}
	return filepath.Join(n.parent.fullPath(), n.name)
}

func (n *roFileVersionsDir) Attr(ctx context.Context, a *fuse.Attr) error {
	a.Inode = n.permanode.Sum64()
	a.Mode = os.ModeDir | 0500
	a.Uid = uint32(os.Getuid())
	a.Gid = uint32(os.Getgid())
	return nil
}

// populate hits the blobstore to populate map of child nodes.
func (n *roFileVersionsDir) populate(ctx context.Context) error {
	n.mu.Lock()
	defer n.mu.Unlock()

	// Things never change here, so if we've ever populated, we're
	// populated.
	if n.children != nil {
		return nil
	}

	Logger.Printf("roFileVersionsDir.populate(%q)", n.fullPath())
	res, err := n.fs.client.GetClaims(ctx, &search.ClaimsRequest{Permanode: n.permanode, AttrFilter: "camliContent"})
	if err != nil {
		return fmt.Errorf("error while getting claims: %w", err)
	}

	n.children = make(map[string]roFileOrDir)
	for _, cl := range res.Claims {
		pn, ok := blob.Parse(cl.Value)
		if !ok {
			return errors.New("invalid blobref")
		}
		res, err := n.fs.client.Describe(ctx, &search.DescribeRequest{
			BlobRef: pn, // this is camliContent
			Depth:   1,
			At:      cl.Date,
		})
		if err != nil {
			return fmt.Errorf("blobref not described: %w", err)
		}
		db := res.Meta[cl.Value]
		if db == nil {
			return errors.New("blobref not described")
		}
		name := cl.Date.String()
		n.children[name] = &roFileVersion{
			fs:        n.fs,
			permanode: n.permanode,
			parent:    n,
			name:      name,
			content:   db.BlobRef,
			size:      db.File.Size,
			mtime:     cl.Date.Time(),
		}

	}
	return nil
}

func (n *roFileVersionsDir) ReadDir(ctx context.Context) ([]fuse.Dirent, error) {
	if err := n.populate(ctx); err != nil {
		Logger.Println("populate:", err)
		return nil, handleEIOorEINTR(err)
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
			Logger.Printf("roFileVersionsDir.ReadDir: unknown child type %T", childNode)
		}

		// TODO: figure out what Dirent.Type means.
		// fuse.go says "Type uint32 // ?"
		dirent := fuse.Dirent{
			Name:  name,
			Inode: ino,
		}
		Logger.Printf("roFileVersionsDir(%q) appending inode %x, %+v", n.fullPath(), dirent.Inode, dirent)
		ents = append(ents, dirent)
	}
	return ents, nil
}

func (n *roFileVersionsDir) Lookup(ctx context.Context, name string) (ret fs.Node, err error) {
	defer func() {
		Logger.Printf("roFileVersionsDir(%q).Lookup(%q) = %#v, %v", n.fullPath(), name, ret, err)
	}()
	if err := n.populate(ctx); err != nil {
		Logger.Println("populate:", err)
		return nil, handleEIOorEINTR(err)
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if n2 := n.children[name]; n2 != nil {
		return n2, nil
	}
	return nil, fuse.ENOENT
}

// roFileVersion is a fuse file that represents
// a permanode file at a specific point in time.
// It is read-only.
type roFileVersion struct {
	fs        *CamliFileSystem
	permanode blob.Ref
	parent    *roFileVersionsDir
	name      string // ent name (base name within parent)

	mu           sync.Mutex // protects all following fields
	symLink      bool       // if true, is a symlink
	content      blob.Ref   // if a regular file
	size         int64
	mtime, atime time.Time // if zero, use serverStart
	xattrs       map[string][]byte
}

func (n *roFileVersion) Open(ctx context.Context, req *fuse.OpenRequest, res *fuse.OpenResponse) (fs.Handle, error) {
	roFileOpen.Incr()

	if isWriteFlags(req.Flags) {
		return nil, fuse.EPERM
	}

	Logger.Printf("roFile.Open: %v: content: %v dir=%v flags=%v", n.permanode, n.content, req.Dir, req.Flags)
	r, err := schema.NewFileReader(ctx, n.fs.fetcher, n.content)
	if err != nil {
		roFileOpenError.Incr()
		Logger.Printf("roFile.Open: %v", err)
		return nil, handleEIOorEINTR(err)
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

func (n *roVersionsDir) Getxattr(ctx context.Context, req *fuse.GetxattrRequest, res *fuse.GetxattrResponse) error {
	return n.xattr().get(req, res)
}

func (n *roVersionsDir) Listxattr(ctx context.Context, req *fuse.ListxattrRequest, res *fuse.ListxattrResponse) error {
	return n.xattr().list(req, res)
}

func (n *roFileVersion) Getxattr(ctx context.Context, req *fuse.GetxattrRequest, res *fuse.GetxattrResponse) error {
	return n.xattr().get(req, res)
}

func (n *roFileVersion) Listxattr(ctx context.Context, req *fuse.ListxattrRequest, res *fuse.ListxattrResponse) error {
	return n.xattr().list(req, res)
}

func (n *roFileVersion) Removexattr(ctx context.Context, req *fuse.RemovexattrRequest) error {
	return fuse.EPERM
}

func (n *roFileVersion) Setxattr(ctx context.Context, req *fuse.SetxattrRequest) error {
	return fuse.EPERM
}

func (n *roFileVersion) Attr(ctx context.Context, a *fuse.Attr) error {
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

func (n *roFileVersion) accessTime() time.Time {
	n.mu.Lock()
	if !n.atime.IsZero() {
		defer n.mu.Unlock()
		return n.atime
	}
	n.mu.Unlock()
	return n.modTime()
}

func (n *roFileVersion) modTime() time.Time {
	n.mu.Lock()
	defer n.mu.Unlock()
	if !n.mtime.IsZero() {
		return n.mtime
	}
	return serverStart
}

func (n *roFileVersion) Fsync(ctx context.Context, r *fuse.FsyncRequest) error {
	// noop
	return nil
}

func (n *roFileVersion) permanodeString() string {
	return n.permanode.String()
}

func (n *roFileVersionsDir) permanodeString() string {
	return n.permanode.String()
}

func (n *roVersionsDir) permanodeString() string {
	return n.permanode.String()
}

func (n *roFileVersion) xattr() *xattr {
	return &xattr{"roFileVersion", n.fs, n.permanode, &n.mu, &n.xattrs}
}

func (n *roFileVersionsDir) xattr() *xattr {
	return &xattr{"roFileVersionsDir", n.fs, n.permanode, &n.mu, &n.xattrs}
}

func (n *roVersionsDir) xattr() *xattr {
	return &xattr{"roVersionsDir", n.fs, n.permanode, &n.mu, &n.xattrs}
}
