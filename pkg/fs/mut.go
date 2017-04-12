// +build linux darwin

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

package fs

import (
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/schema"
	"camlistore.org/pkg/search"

	"bazil.org/fuse"
	"bazil.org/fuse/fs"
	"go4.org/readerutil"
	"go4.org/syncutil"
	"golang.org/x/net/context"
)

// How often to refresh directory nodes by reading from the blobstore.
const populateInterval = 30 * time.Second

// How long an item that was created locally will be present
// regardless of its presence in the indexing server.
const deletionRefreshWindow = time.Minute

type nodeType int

const (
	fileType nodeType = iota
	dirType
	symlinkType
)

// mutDir is a mutable directory.
// Its br is the permanode with camliPath:entname attributes.
type mutDir struct {
	fs        *CamliFileSystem
	permanode blob.Ref
	parent    *mutDir // or nil, if the root within its roots.go root.
	name      string  // ent name (base name within parent)

	localCreateTime time.Time // time this node was created locally (iff it was)

	mu       sync.Mutex
	lastPop  time.Time
	children map[string]mutFileOrDir
	xattrs   map[string][]byte
	deleted  bool
}

var _ fs.Node = (*mutDir)(nil)
var _ fs.NodeAccesser = (*mutDir)(nil)
var _ fs.HandleReadDirAller = (*mutDir)(nil)
var _ fs.NodeStringLookuper = (*mutDir)(nil)
var _ fs.NodeGetxattrer = (*mutDir)(nil)
var _ fs.NodeListxattrer = (*mutDir)(nil)
var _ fs.NodeSetxattrer = (*mutDir)(nil)
var _ fs.NodeCreater = (*mutDir)(nil)
var _ fs.NodeMkdirer = (*mutDir)(nil)
var _ fs.NodeSymlinker = (*mutDir)(nil)
var _ fs.NodeRemover = (*mutDir)(nil)
var _ fs.NodeRenamer = (*mutDir)(nil)

func (m *mutDir) String() string {
	return fmt.Sprintf("&mutDir{%p name=%q perm:%v}", m, m.fullPath(), m.permanode)
}

// for debugging
func (n *mutDir) fullPath() string {
	if n == nil {
		return ""
	}
	return filepath.Join(n.parent.fullPath(), n.name)
}

func (n *mutDir) Attr(ctx context.Context, a *fuse.Attr) error {
	a.Inode = n.permanode.Sum64()
	a.Mode = os.ModeDir | 0700
	a.Uid = uint32(os.Getuid())
	a.Gid = uint32(os.Getgid())
	return nil
}

func (n *mutDir) Access(ctx context.Context, req *fuse.AccessRequest) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.deleted {
		return fuse.ENOENT
	}
	return nil
}

func (n *mutFile) Access(ctx context.Context, req *fuse.AccessRequest) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.deleted {
		return fuse.ENOENT
	}
	return nil
}

// populate hits the blobstore to populate map of child nodes.
func (n *mutDir) populate() error {
	n.mu.Lock()
	defer n.mu.Unlock()
	ctx := context.TODO()

	// Only re-populate if we haven't done so recently.
	now := time.Now()
	if n.lastPop.Add(populateInterval).After(now) {
		return nil
	}
	n.lastPop = now

	res, err := n.fs.client.Describe(ctx, &search.DescribeRequest{
		BlobRef: n.permanode,
		Depth:   3,
	})
	if err != nil {
		log.Println("mutDir.paths:", err)
		return nil
	}
	db := res.Meta[n.permanode.String()]
	if db == nil {
		return errors.New("dir blobref not described")
	}

	// Find all child permanodes and stick them in n.children
	if n.children == nil {
		n.children = make(map[string]mutFileOrDir)
	}
	currentChildren := map[string]bool{}
	for k, v := range db.Permanode.Attr {
		const p = "camliPath:"
		if !strings.HasPrefix(k, p) || len(v) < 1 {
			continue
		}
		name := k[len(p):]
		childRef := v[0]
		child := res.Meta[childRef]
		if child == nil {
			log.Printf("child not described: %v", childRef)
			continue
		}
		if child.Permanode == nil {
			log.Printf("invalid child, not a permanode: %v", childRef)
			continue
		}
		if target := child.Permanode.Attr.Get("camliSymlinkTarget"); target != "" {
			// This is a symlink.
			n.maybeAddChild(name, child.Permanode, &mutFile{
				fs:        n.fs,
				permanode: blob.ParseOrZero(childRef),
				parent:    n,
				name:      name,
				symLink:   true,
				target:    target,
			})
		} else if isDir(child.Permanode) {
			// This is a directory.
			n.maybeAddChild(name, child.Permanode, &mutDir{
				fs:        n.fs,
				permanode: blob.ParseOrZero(childRef),
				parent:    n,
				name:      name,
			})
		} else if contentRef := child.Permanode.Attr.Get("camliContent"); contentRef != "" {
			// This is a file.
			content := res.Meta[contentRef]
			if content == nil {
				log.Printf("child content not described: %v", childRef)
				continue
			}
			if content.CamliType != "file" {
				log.Printf("child not a file: %v", childRef)
				continue
			}
			if content.File == nil {
				log.Printf("camlitype \"file\" child %v has no described File member", childRef)
				continue
			}
			n.maybeAddChild(name, child.Permanode, &mutFile{
				fs:        n.fs,
				permanode: blob.ParseOrZero(childRef),
				parent:    n,
				name:      name,
				content:   blob.ParseOrZero(contentRef),
				size:      content.File.Size,
			})
		} else {
			// unhandled type...
			continue
		}
		currentChildren[name] = true
	}
	// Remove unreferenced children
	for name, oldchild := range n.children {
		if _, ok := currentChildren[name]; !ok {
			if oldchild.eligibleToDelete() {
				delete(n.children, name)
			}
		}
	}
	return nil
}

// maybeAddChild adds a child directory to this mutable directory
// unless it already has one with this name and permanode.
func (m *mutDir) maybeAddChild(name string, permanode *search.DescribedPermanode,
	child mutFileOrDir) {
	if current, ok := m.children[name]; !ok ||
		current.permanodeString() != child.permanodeString() {

		child.xattr().load(permanode)
		m.children[name] = child
	}
}

func isDir(d *search.DescribedPermanode) bool {
	// Explicit
	if d.Attr.Get("camliNodeType") == "directory" {
		return true
	}
	// Implied
	for k := range d.Attr {
		if strings.HasPrefix(k, "camliPath:") {
			return true
		}
	}
	return false
}

func (n *mutDir) ReadDirAll(ctx context.Context) ([]fuse.Dirent, error) {
	if err := n.populate(); err != nil {
		log.Println("populate:", err)
		return nil, fuse.EIO
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	var ents []fuse.Dirent
	for name, childNode := range n.children {
		var ino uint64
		switch v := childNode.(type) {
		case *mutDir:
			ino = v.permanode.Sum64()
		case *mutFile:
			ino = v.permanode.Sum64()
		default:
			log.Printf("mutDir.ReadDirAll: unknown child type %T", childNode)
		}

		// TODO: figure out what Dirent.Type means.
		// fuse.go says "Type uint32 // ?"
		dirent := fuse.Dirent{
			Name:  name,
			Inode: ino,
		}
		log.Printf("mutDir(%q) appending inode %x, %+v", n.fullPath(), dirent.Inode, dirent)
		ents = append(ents, dirent)
	}
	return ents, nil
}

func (n *mutDir) Lookup(ctx context.Context, name string) (ret fs.Node, err error) {
	defer func() {
		log.Printf("mutDir(%q).Lookup(%q) = %v, %v", n.fullPath(), name, ret, err)
	}()
	if err := n.populate(); err != nil {
		log.Println("populate:", err)
		return nil, fuse.EIO
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if n2 := n.children[name]; n2 != nil {
		return n2, nil
	}
	return nil, fuse.ENOENT
}

// Create of regular file. (not a dir)
//
// Flags are always 514:  O_CREAT is 0x200 | O_RDWR is 0x2.
// From fuse_vnops.c:
//    /* XXX: We /always/ creat() like this. Wish we were on Linux. */
//    foi->flags = O_CREAT | O_RDWR;
//
// 2013/07/21 05:26:35 <- &{Create [ID=0x3 Node=0x8 Uid=61652 Gid=5000 Pid=13115] "x" fl=514 mode=-rw-r--r-- fuse.Intr}
// 2013/07/21 05:26:36 -> 0x3 Create {LookupResponse:{Node:23 Generation:0 EntryValid:1m0s AttrValid:1m0s Attr:{Inode:15976986887557313215 Size:0 Blocks:0 Atime:2013-07-21 05:23:51.537251251 +1200 NZST Mtime:2013-07-21 05:23:51.537251251 +1200 NZST Ctime:2013-07-21 05:23:51.537251251 +1200 NZST Crtime:2013-07-21 05:23:51.537251251 +1200 NZST Mode:-rw------- Nlink:1 Uid:61652 Gid:5000 Rdev:0 Flags:0}} OpenResponse:{Handle:1 Flags:0}}
func (n *mutDir) Create(ctx context.Context, req *fuse.CreateRequest, res *fuse.CreateResponse) (fs.Node, fs.Handle, error) {
	child, err := n.creat(req.Name, fileType)
	if err != nil {
		log.Printf("mutDir.Create(%q): %v", req.Name, err)
		return nil, nil, fuse.EIO
	}

	// Create and return a file handle.
	h, ferr := child.(*mutFile).newHandle(nil)
	if ferr != nil {
		return nil, nil, ferr
	}

	return child, h, nil
}

func (n *mutDir) Mkdir(ctx context.Context, req *fuse.MkdirRequest) (fs.Node, error) {
	child, err := n.creat(req.Name, dirType)
	if err != nil {
		log.Printf("mutDir.Mkdir(%q): %v", req.Name, err)
		return nil, fuse.EIO
	}
	return child, nil
}

// &fuse.SymlinkRequest{Header:fuse.Header{Conn:(*fuse.Conn)(0xc210047180), ID:0x4, Node:0x8, Uid:0xf0d4, Gid:0x1388, Pid:0x7e88}, NewName:"some-link", Target:"../../some-target"}
func (n *mutDir) Symlink(ctx context.Context, req *fuse.SymlinkRequest) (fs.Node, error) {
	node, err := n.creat(req.NewName, symlinkType)
	if err != nil {
		log.Printf("mutDir.Symlink(%q): %v", req.NewName, err)
		return nil, fuse.EIO
	}
	mf := node.(*mutFile)
	mf.symLink = true
	mf.target = req.Target

	claim := schema.NewSetAttributeClaim(mf.permanode, "camliSymlinkTarget", req.Target)
	_, err = n.fs.client.UploadAndSignBlob(claim)
	if err != nil {
		log.Printf("mutDir.Symlink(%q) upload error: %v", req.NewName, err)
		return nil, fuse.EIO
	}

	return node, nil
}

func (n *mutDir) creat(name string, typ nodeType) (fs.Node, error) {
	// Create a Permanode for the file/directory.
	pr, err := n.fs.client.UploadNewPermanode()
	if err != nil {
		return nil, err
	}

	var grp syncutil.Group
	grp.Go(func() (err error) {
		// Add a camliPath:name attribute to the directory permanode.
		claim := schema.NewSetAttributeClaim(n.permanode, "camliPath:"+name, pr.BlobRef.String())
		_, err = n.fs.client.UploadAndSignBlob(claim)
		return
	})

	// Hide OS X Finder .DS_Store junk.  This is distinct from
	// extended attributes.
	if name == ".DS_Store" {
		grp.Go(func() (err error) {
			claim := schema.NewSetAttributeClaim(pr.BlobRef, "camliDefVis", "hide")
			_, err = n.fs.client.UploadAndSignBlob(claim)
			return
		})
	}

	if typ == dirType {
		grp.Go(func() (err error) {
			// Set a directory type on the permanode
			claim := schema.NewSetAttributeClaim(pr.BlobRef, "camliNodeType", "directory")
			_, err = n.fs.client.UploadAndSignBlob(claim)
			return
		})
		grp.Go(func() (err error) {
			// Set the permanode title to the directory name
			claim := schema.NewSetAttributeClaim(pr.BlobRef, "title", name)
			_, err = n.fs.client.UploadAndSignBlob(claim)
			return
		})
	}
	if err := grp.Err(); err != nil {
		return nil, err
	}

	// Add a child node to this node.
	var child mutFileOrDir
	switch typ {
	case dirType:
		child = &mutDir{
			fs:              n.fs,
			permanode:       pr.BlobRef,
			parent:          n,
			name:            name,
			xattrs:          map[string][]byte{},
			localCreateTime: time.Now(),
		}
	case fileType, symlinkType:
		child = &mutFile{
			fs:              n.fs,
			permanode:       pr.BlobRef,
			parent:          n,
			name:            name,
			xattrs:          map[string][]byte{},
			localCreateTime: time.Now(),
		}
	default:
		panic("bogus creat type")
	}
	n.mu.Lock()
	if n.children == nil {
		n.children = make(map[string]mutFileOrDir)
	}
	n.children[name] = child
	n.mu.Unlock()

	log.Printf("Created %v in %p", child, n)

	return child, nil
}

func (n *mutDir) Remove(ctx context.Context, req *fuse.RemoveRequest) error {
	// Remove the camliPath:name attribute from the directory permanode.
	claim := schema.NewDelAttributeClaim(n.permanode, "camliPath:"+req.Name, "")
	_, err := n.fs.client.UploadAndSignBlob(claim)
	if err != nil {
		log.Println("mutDir.Remove:", err)
		return fuse.EIO
	}
	// Remove child from map.
	n.mu.Lock()
	if n.children != nil {
		if removed, ok := n.children[req.Name]; ok {
			removed.invalidate()
			delete(n.children, req.Name)
			log.Printf("Removed %v from %p", removed, n)
		}
	}
	n.mu.Unlock()
	return nil
}

// &RenameRequest{Header:fuse.Header{Conn:(*fuse.Conn)(0xc210048180), ID:0x2, Node:0x8, Uid:0xf0d4, Gid:0x1388, Pid:0x5edb}, NewDir:0x8, OldName:"1", NewName:"2"}
func (n *mutDir) Rename(ctx context.Context, req *fuse.RenameRequest, newDir fs.Node) error {
	n2, ok := newDir.(*mutDir)
	if !ok {
		log.Printf("*mutDir newDir node isn't a *mutDir; is a %T; can't handle. returning EIO.", newDir)
		return fuse.EIO
	}

	var wg syncutil.Group
	wg.Go(n.populate)
	wg.Go(n2.populate)
	if err := wg.Err(); err != nil {
		log.Printf("*mutDir.Rename src dir populate = %v", err)
		return fuse.EIO
	}

	n.mu.Lock()
	target, ok := n.children[req.OldName]
	n.mu.Unlock()
	if !ok {
		log.Printf("*mutDir.Rename src name %q isn't known", req.OldName)
		return fuse.ENOENT
	}

	now := time.Now()

	// Add a camliPath:name attribute to the dest permanode before unlinking it from
	// the source.
	claim := schema.NewSetAttributeClaim(n2.permanode, "camliPath:"+req.NewName, target.permanodeString())
	claim.SetClaimDate(now)
	_, err := n.fs.client.UploadAndSignBlob(claim)
	if err != nil {
		log.Printf("Upload rename link error: %v", err)
		return fuse.EIO
	}

	var grp syncutil.Group
	// Unlink the dest permanode from the source.
	grp.Go(func() (err error) {
		delClaim := schema.NewDelAttributeClaim(n.permanode, "camliPath:"+req.OldName, "")
		delClaim.SetClaimDate(now)
		_, err = n.fs.client.UploadAndSignBlob(delClaim)
		return
	})
	// If target is a directory then update its title.
	if dir, ok := target.(*mutDir); ok {
		grp.Go(func() (err error) {
			claim := schema.NewSetAttributeClaim(dir.permanode, "title", req.NewName)
			_, err = n.fs.client.UploadAndSignBlob(claim)
			return
		})
	}
	if err := grp.Err(); err != nil {
		log.Printf("Upload rename unlink/title error: %v", err)
		return fuse.EIO
	}

	// TODO(bradfitz): this locking would be racy, if the kernel
	// doesn't do it properly. (It should) Let's just trust the
	// kernel for now. Later we can verify and remove this
	// comment.
	n.mu.Lock()
	if n.children[req.OldName] != target {
		panic("Race.")
	}
	delete(n.children, req.OldName)
	n.mu.Unlock()
	n2.mu.Lock()
	n2.children[req.NewName] = target
	n2.mu.Unlock()

	return nil
}

// mutFile is a mutable file, or symlink.
type mutFile struct {
	fs        *CamliFileSystem
	permanode blob.Ref
	parent    *mutDir
	name      string // ent name (base name within parent)

	localCreateTime time.Time // time this node was created locally (iff it was)

	mu           sync.Mutex // protects all following fields
	symLink      bool       // if true, is a symlink
	target       string     // if a symlink
	content      blob.Ref   // if a regular file
	size         int64
	mtime, atime time.Time // if zero, use serverStart
	xattrs       map[string][]byte
	deleted      bool
}

var (
	_ fs.Node            = (*mutFile)(nil)
	_ fs.NodeAccesser    = (*mutFile)(nil)
	_ fs.NodeGetxattrer  = (*mutFile)(nil)
	_ fs.NodeListxattrer = (*mutFile)(nil)
	_ fs.NodeSetxattrer  = (*mutFile)(nil)
	_ fs.NodeOpener      = (*mutFile)(nil)
	_ fs.NodeFsyncer     = (*mutFile)(nil)
	_ fs.NodeReadlinker  = (*mutFile)(nil)
	_ fs.NodeSetattrer   = (*mutFile)(nil)
)

func (m *mutFile) String() string {
	return fmt.Sprintf("&mutFile{%p name=%q perm:%v}", m, m.fullPath(), m.permanode)
}

// for debugging
func (n *mutFile) fullPath() string {
	if n == nil {
		return ""
	}
	return filepath.Join(n.parent.fullPath(), n.name)
}

func (n *mutFile) xattr() *xattr {
	return &xattr{"mutFile", n.fs, n.permanode, &n.mu, &n.xattrs}
}

func (n *mutDir) xattr() *xattr {
	return &xattr{"mutDir", n.fs, n.permanode, &n.mu, &n.xattrs}
}

func (n *mutDir) Removexattr(ctx context.Context, req *fuse.RemovexattrRequest) error {
	return n.xattr().remove(req)
}

func (n *mutDir) Setxattr(ctx context.Context, req *fuse.SetxattrRequest) error {
	return n.xattr().set(req)
}

func (n *mutDir) Getxattr(ctx context.Context, req *fuse.GetxattrRequest, res *fuse.GetxattrResponse) error {
	return n.xattr().get(req, res)
}

func (n *mutDir) Listxattr(ctx context.Context, req *fuse.ListxattrRequest, res *fuse.ListxattrResponse) error {
	return n.xattr().list(req, res)
}

func (n *mutFile) Getxattr(ctx context.Context, req *fuse.GetxattrRequest, res *fuse.GetxattrResponse) error {
	return n.xattr().get(req, res)
}

func (n *mutFile) Listxattr(ctx context.Context, req *fuse.ListxattrRequest, res *fuse.ListxattrResponse) error {
	return n.xattr().list(req, res)
}

func (n *mutFile) Removexattr(ctx context.Context, req *fuse.RemovexattrRequest) error {
	return n.xattr().remove(req)
}

func (n *mutFile) Setxattr(ctx context.Context, req *fuse.SetxattrRequest) error {
	return n.xattr().set(req)
}

func (n *mutFile) Attr(ctx context.Context, a *fuse.Attr) error {
	// TODO: don't grab n.mu three+ times in here.
	var mode os.FileMode = 0600 // writable

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

	a.Inode = inode
	a.Mode = mode
	a.Uid = uint32(os.Getuid())
	a.Gid = uint32(os.Getgid())
	a.Size = uint64(size)
	a.Blocks = blocks
	a.Mtime = n.modTime()
	a.Atime = n.accessTime()
	a.Ctime = serverStart
	a.Crtime = serverStart
	return nil
}

func (n *mutFile) accessTime() time.Time {
	n.mu.Lock()
	if !n.atime.IsZero() {
		defer n.mu.Unlock()
		return n.atime
	}
	n.mu.Unlock()
	return n.modTime()
}

func (n *mutFile) modTime() time.Time {
	n.mu.Lock()
	defer n.mu.Unlock()
	if !n.mtime.IsZero() {
		return n.mtime
	}
	return serverStart
}

func (n *mutFile) setContent(br blob.Ref, size int64) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.content = br
	n.size = size
	claim := schema.NewSetAttributeClaim(n.permanode, "camliContent", br.String())
	_, err := n.fs.client.UploadAndSignBlob(claim)
	return err
}

func (n *mutFile) setSizeAtLeast(size int64) {
	n.mu.Lock()
	defer n.mu.Unlock()
	log.Printf("mutFile.setSizeAtLeast(%d). old size = %d", size, n.size)
	if size > n.size {
		n.size = size
	}
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
func (n *mutFile) Open(ctx context.Context, req *fuse.OpenRequest, res *fuse.OpenResponse) (fs.Handle, error) {
	mutFileOpen.Incr()

	log.Printf("mutFile.Open: %v: content: %v dir=%v flags=%v", n.permanode, n.content, req.Dir, req.Flags)
	r, err := schema.NewFileReader(n.fs.fetcher, n.content)
	if err != nil {
		mutFileOpenError.Incr()
		log.Printf("mutFile.Open: %v", err)
		return nil, fuse.EIO
	}

	// Read-only.
	if !isWriteFlags(req.Flags) {
		mutFileOpenRO.Incr()
		log.Printf("mutFile.Open returning read-only file")
		n := &node{
			fs:      n.fs,
			blobref: n.content,
		}
		return &nodeReader{n: n, fr: r}, nil
	}

	mutFileOpenRW.Incr()
	log.Printf("mutFile.Open returning read-write filehandle")

	defer r.Close()
	return n.newHandle(r)
}

func (n *mutFile) Fsync(ctx context.Context, r *fuse.FsyncRequest) error {
	// TODO(adg): in the fuse package, plumb through fsync to mutFileHandle
	// in the same way we did Truncate.
	log.Printf("mutFile.Fsync: TODO")
	return nil
}

func (n *mutFile) Readlink(ctx context.Context, req *fuse.ReadlinkRequest) (string, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if !n.symLink {
		log.Printf("mutFile.Readlink on node that's not a symlink?")
		return "", fuse.EIO
	}
	return n.target, nil
}

func (n *mutFile) Setattr(ctx context.Context, req *fuse.SetattrRequest, res *fuse.SetattrResponse) error {
	log.Printf("mutFile.Setattr on %q: %#v", n.fullPath(), req)
	// 2013/07/17 19:43:41 mutFile.Setattr on "foo": &fuse.SetattrRequest{Header:fuse.Header{Conn:(*fuse.Conn)(0xc210047180), ID:0x3, Node:0x3d, Uid:0xf0d4, Gid:0x1388, Pid:0x75e8}, Valid:0x30, Handle:0x0, Size:0x0, Atime:time.Time{sec:63509651021, nsec:0x4aec6b8, loc:(*time.Location)(0x47f7600)}, Mtime:time.Time{sec:63509651021, nsec:0x4aec6b8, loc:(*time.Location)(0x47f7600)}, Mode:0x4000000, Uid:0x0, Gid:0x0, Bkuptime:time.Time{sec:62135596800, nsec:0x0, loc:(*time.Location)(0x47f7600)}, Chgtime:time.Time{sec:62135596800, nsec:0x0, loc:(*time.Location)(0x47f7600)}, Crtime:time.Time{sec:0, nsec:0x0, loc:(*time.Location)(nil)}, Flags:0x0}

	n.mu.Lock()
	if req.Valid&fuse.SetattrMtime != 0 {
		n.mtime = req.Mtime
	}
	if req.Valid&fuse.SetattrAtime != 0 {
		n.atime = req.Atime
	}
	if req.Valid&fuse.SetattrSize != 0 {
		// TODO(bradfitz): truncate?
		n.size = int64(req.Size)
	}
	n.mu.Unlock()

	n.Attr(ctx, &res.Attr)
	return nil
}

func (n *mutFile) newHandle(body io.Reader) (fs.Handle, error) {
	tmp, err := ioutil.TempFile("", "camli-")
	if err == nil && body != nil {
		_, err = io.Copy(tmp, body)
	}
	if err != nil {
		log.Printf("mutFile.newHandle: %v", err)
		if tmp != nil {
			tmp.Close()
			os.Remove(tmp.Name())
		}
		return nil, fuse.EIO
	}
	return &mutFileHandle{f: n, tmp: tmp}, nil
}

// mutFileHandle represents an open mutable file.
// It stores the file contents in a temporary file, and
// delegates reads and writes directly to the temporary file.
// When the handle is released, it writes the contents of the
// temporary file to the blobstore, and instructs the parent
// mutFile to update the file permanode.
type mutFileHandle struct {
	f   *mutFile
	tmp *os.File
}

var (
	_ fs.HandleReader   = (*mutFileHandle)(nil)
	_ fs.HandleWriter   = (*mutFileHandle)(nil)
	_ fs.HandleFlusher  = (*mutFileHandle)(nil)
	_ fs.HandleReleaser = (*mutFileHandle)(nil)
)

func (h *mutFileHandle) Read(ctx context.Context, req *fuse.ReadRequest, res *fuse.ReadResponse) error {
	if h.tmp == nil {
		log.Printf("Read called on camli mutFileHandle without a tempfile set")
		return fuse.EIO
	}

	buf := make([]byte, req.Size)
	n, err := h.tmp.ReadAt(buf, req.Offset)
	if err == io.EOF {
		err = nil
	}
	if err != nil {
		log.Printf("mutFileHandle.Read: %v", err)
		return fuse.EIO
	}
	res.Data = buf[:n]
	return nil
}

func (h *mutFileHandle) Write(ctx context.Context, req *fuse.WriteRequest, res *fuse.WriteResponse) error {
	if h.tmp == nil {
		log.Printf("Write called on camli mutFileHandle without a tempfile set")
		return fuse.EIO
	}

	n, err := h.tmp.WriteAt(req.Data, req.Offset)
	log.Printf("mutFileHandle.Write(%q, %d bytes at %d, flags %v) = %d, %v",
		h.f.fullPath(), len(req.Data), req.Offset, req.Flags, n, err)
	if err != nil {
		log.Println("mutFileHandle.Write:", err)
		return fuse.EIO
	}
	res.Size = n
	h.f.setSizeAtLeast(req.Offset + int64(n))
	return nil
}

// Flush is called to let the file system clean up any data buffers
// and to pass any errors in the process of closing a file to the user
// application.
//
// Flush *may* be called more than once in the case where a file is
// opened more than once, but it's not possible to detect from the
// call itself whether this is a final flush.
//
// This is generally the last opportunity to finalize data and the
// return value sets the return value of the Close that led to the
// calling of Flush.
//
// Note that this is distinct from Fsync -- which is a user-requested
// flush (fsync, etc...)
func (h *mutFileHandle) Flush(context.Context, *fuse.FlushRequest) error {
	if h.tmp == nil {
		log.Printf("Flush called on camli mutFileHandle without a tempfile set")
		return fuse.EIO
	}
	_, err := h.tmp.Seek(0, 0)
	if err != nil {
		log.Println("mutFileHandle.Flush:", err)
		return fuse.EIO
	}
	var n int64
	br, err := schema.WriteFileFromReader(h.f.fs.client, h.f.name, readerutil.CountingReader{Reader: h.tmp, N: &n})
	if err != nil {
		log.Println("mutFileHandle.Flush:", err)
		return fuse.EIO
	}
	err = h.f.setContent(br, n)
	if err != nil {
		log.Printf("mutFileHandle.Flush: %v", err)
		return fuse.EIO
	}

	return nil
}

// Release is called when a file handle is no longer needed.  This is
// called asynchronously after the last handle to a file is closed.
func (h *mutFileHandle) Release(ctx context.Context, req *fuse.ReleaseRequest) error {
	h.tmp.Close()
	os.Remove(h.tmp.Name())
	h.tmp = nil

	return nil
}

// mutFileOrDir is a *mutFile or *mutDir
type mutFileOrDir interface {
	fs.Node
	invalidate()
	permanodeString() string
	xattr() *xattr
	eligibleToDelete() bool
}

func (n *mutFile) permanodeString() string {
	return n.permanode.String()
}

func (n *mutDir) permanodeString() string {
	return n.permanode.String()
}

func (n *mutFile) invalidate() {
	n.mu.Lock()
	n.deleted = true
	n.mu.Unlock()
}

func (n *mutDir) invalidate() {
	n.mu.Lock()
	n.deleted = true
	n.mu.Unlock()
}

func (n *mutFile) eligibleToDelete() bool {
	return n.localCreateTime.Before(time.Now().Add(-deletionRefreshWindow))
}

func (n *mutDir) eligibleToDelete() bool {
	return n.localCreateTime.Before(time.Now().Add(-deletionRefreshWindow))
}
