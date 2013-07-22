// +build linux darwin

/*
Copyright 2012 Google Inc.

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
	"io"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"camlistore.org/pkg/blobref"
	"camlistore.org/pkg/readerutil"
	"camlistore.org/pkg/schema"
	"camlistore.org/pkg/search"

	"camlistore.org/third_party/code.google.com/p/rsc/fuse"
)

// How often to refresh directory nodes by reading from the blobstore.
const populateInterval = 30 * time.Second

// mutDir is a mutable directory.
// Its br is the permanode with camliPath:entname attributes.
type mutDir struct {
	fs        *CamliFileSystem
	permanode *blobref.BlobRef
	parent    *mutDir // or nil, if the root within its roots.go root.
	name      string  // ent name (base name within parent)

	mu       sync.Mutex
	lastPop  time.Time
	children map[string]fuse.Node
}

// for debugging
func (n *mutDir) fullPath() string {
	if n == nil {
		return ""
	}
	return filepath.Join(n.parent.fullPath(), n.name)
}

func (n *mutDir) Attr() fuse.Attr {
	return fuse.Attr{
		Mode: os.ModeDir | 0700,
		Uid:  uint32(os.Getuid()),
		Gid:  uint32(os.Getgid()),
	}
}

// populate hits the blobstore to populate map of child nodes.
func (n *mutDir) populate() error {
	n.mu.Lock()
	defer n.mu.Unlock()

	// Only re-populate if we haven't done so recently.
	now := time.Now()
	if n.lastPop.Add(populateInterval).After(now) {
		return nil
	}
	n.lastPop = now

	res, err := n.fs.client.Describe(&search.DescribeRequest{
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
		n.children = make(map[string]fuse.Node)
	}
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
		if contentRef := child.Permanode.Attr.Get("camliContent"); contentRef != "" {
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
			n.children[name] = &mutFile{
				fs:        n.fs,
				permanode: blobref.Parse(childRef),
				parent:    n,
				name:      name,
				content:   blobref.Parse(contentRef),
				size:      content.File.Size,
			}
			continue
		}
		// This is a directory.
		n.children[name] = &mutDir{
			fs:        n.fs,
			permanode: blobref.Parse(childRef),
			parent:    n,
			name:      name,
		}
	}
	return nil
}

func (n *mutDir) ReadDir(intr fuse.Intr) ([]fuse.Dirent, fuse.Error) {
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
			ino = v.permanode.AsUint64()
		case *mutFile:
			ino = v.permanode.AsUint64()
		default:
			log.Printf("mutDir.ReadDir: unknown child type %T", childNode)
		}

		// TODO: figure out what Dirent.Type means.
		// fuse.go says "Type uint32 // ?"
		dirent := fuse.Dirent{
			Name: name,
			Inode: ino,
		}
		log.Printf("mutDir(%q) appending inode %x, %+v", n.fullPath(), dirent.Inode, dirent)
		ents = append(ents, dirent)
	}
	return ents, nil
}

func (n *mutDir) Lookup(name string, intr fuse.Intr) (ret fuse.Node, err fuse.Error) {
	defer func() {
		log.Printf("mutDir(%q).Lookup(%q) = %#v, %v", n.fullPath(), name, ret, err)
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
// TOOD(bradfitz): what are the two bits in fl=514? what are CreateRequest.Flags?
//
// 2013/07/21 05:26:35 <- &{Create [ID=0x3 Node=0x8 Uid=61652 Gid=5000 Pid=13115] "x" fl=514 mode=-rw-r--r-- fuse.Intr}
// 2013/07/21 05:26:36 -> 0x3 Create {LookupResponse:{Node:23 Generation:0 EntryValid:1m0s AttrValid:1m0s Attr:{Inode:15976986887557313215 Size:0 Blocks:0 Atime:2013-07-21 05:23:51.537251251 +1200 NZST Mtime:2013-07-21 05:23:51.537251251 +1200 NZST Ctime:2013-07-21 05:23:51.537251251 +1200 NZST Crtime:2013-07-21 05:23:51.537251251 +1200 NZST Mode:-rw------- Nlink:1 Uid:61652 Gid:5000 Rdev:0 Flags:0}} OpenResponse:{Handle:1 Flags:OpenDirectIO}}
func (n *mutDir) Create(req *fuse.CreateRequest, res *fuse.CreateResponse, intr fuse.Intr) (fuse.Node, fuse.Handle, fuse.Error) {
	child, err := n.creat(req.Name, false)
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

func (n *mutDir) Mkdir(req *fuse.MkdirRequest, intr fuse.Intr) (fuse.Node, fuse.Error) {
	child, err := n.creat(req.Name, true)
	if err != nil {
		log.Printf("mutDir.Mkdir(%q): %v", req.Name, err)
		return nil, fuse.EIO
	}
	return child, nil
}

func (n *mutDir) creat(name string, isDir bool) (fuse.Node, error) {
	// Create a Permanode for the file/directory.
	pr, err := n.fs.client.UploadNewPermanode()
	if err != nil {
		return nil, err
	}

	// Add a camliPath:name attribute to the directory permanode.
	claim := schema.NewSetAttributeClaim(n.permanode, "camliPath:"+name, pr.BlobRef.String())
	_, err = n.fs.client.UploadAndSignBlob(claim)
	if err != nil {
		return nil, err
	}

	// Add a child node to this node.
	var child fuse.Node
	if isDir {
		child = &mutDir{
			fs:        n.fs,
			permanode: pr.BlobRef,
			parent:    n,
			name:      name,
		}
	} else {
		child = &mutFile{
			fs:        n.fs,
			permanode: pr.BlobRef,
			parent:    n,
			name:      name,
		}
	}
	n.mu.Lock()
	if n.children == nil {
		n.children = make(map[string]fuse.Node)
	}
	n.children[name] = child
	n.mu.Unlock()

	return child, nil
}

func (n *mutDir) Remove(req *fuse.RemoveRequest, intr fuse.Intr) fuse.Error {
	// Remove the camliPath:name attribute from the directory permanode.
	claim := schema.NewDelAttributeClaim(n.permanode, "camliPath:"+req.Name)
	_, err := n.fs.client.UploadAndSignBlob(claim)
	if err != nil {
		log.Println("mutDir.Create:", err)
		return fuse.EIO
	}
	// Remove child from map.
	n.mu.Lock()
	if n.children != nil {
		delete(n.children, req.Name)
	}
	n.mu.Unlock()
	return nil
}

// mutFile is a mutable file.
type mutFile struct {
	fs        *CamliFileSystem
	permanode *blobref.BlobRef
	parent    *mutDir
	name      string // ent name (base name within parent)

	mu           sync.Mutex
	content      *blobref.BlobRef
	size         int64
	mtime, atime time.Time // if zero, use serverStart
}

// for debugging
func (n *mutFile) fullPath() string {
	if n == nil {
		return ""
	}
	return filepath.Join(n.parent.fullPath(), n.name)
}

func (n *mutFile) Attr() fuse.Attr {
	// TODO: don't grab n.mu three+ times in here.

	n.mu.Lock()
	size := n.size
	var blocks uint64
	if size > 0 {
		blocks = uint64(size)/512 + 1
	}
	inode := n.permanode.AsUint64()
	n.mu.Unlock()

	return fuse.Attr{
		Inode:  inode,
		Mode:   0600, // writable!
		Uid:    uint32(os.Getuid()),
		Gid:    uint32(os.Getgid()),
		Size:   uint64(size),
		Blocks: blocks,
		Mtime:  n.modTime(),
		Atime:  n.accessTime(),
		Ctime:  serverStart,
		Crtime: serverStart,
	}
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

func (n *mutFile) setContent(br *blobref.BlobRef, size int64) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.content = br
	n.size = size
	claim := schema.NewSetAttributeClaim(n.permanode, "camliContent", br.String())
	_, err := n.fs.client.UploadAndSignBlob(claim)
	return err
}

// Empirically:
//  open for read:   req.Flags == 0
//  open for append: req.Flags == 1
//  open for write:  req.Flags == 1
//  open for read/write (+<)   == 2 (bitmask? of?)
// TODO(bradfitz): look this up, once I have connectivity.
func (n *mutFile) Open(req *fuse.OpenRequest, res *fuse.OpenResponse, intr fuse.Intr) (fuse.Handle, fuse.Error) {
	log.Printf("mutFile.Open: %v: content: %v dir=%v flags=%v mode=%v", n.permanode, n.content, req.Dir, req.Flags, req.Mode)
	r, err := schema.NewFileReader(n.fs.fetcher, n.content)
	if err != nil {
		log.Printf("mutFile.Open: %v", err)
		return nil, fuse.EIO
	}

	// Read-only.
	if req.Flags == 0 {
		log.Printf("mutFile.Open returning read-only file")
		n := &node{
			fs:      n.fs,
			blobref: n.content,
		}
		return &nodeReader{n: n, fr: r}, nil
	}

	log.Printf("mutFile.Open returning read-write filehandle")

	// Turn off the OpenDirectIO bit (on by default in rsc fuse server.go),
	// else append operations don't work for some reason.
	// TODO(bradfitz): also do tihs in Create? CreateResponse.OpenResponse.Flags.
	res.Flags &= ^fuse.OpenDirectIO

	defer r.Close()
	return n.newHandle(r)
}

func (n *mutFile) Fsync(r *fuse.FsyncRequest, intr fuse.Intr) fuse.Error {
	// TODO(adg): in the fuse package, plumb through fsync to mutFileHandle
	// in the same way we did Truncate.
	log.Printf("mutFile.Fsync: TODO")
	return nil
}

func (n *mutFile) Setattr(req *fuse.SetattrRequest, res *fuse.SetattrResponse, intr fuse.Intr) fuse.Error {
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

	res.AttrValid = 1 * time.Minute
	res.Attr = n.Attr()
	return nil
}

func (n *mutFile) newHandle(body io.Reader) (fuse.Handle, fuse.Error) {
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
	f       *mutFile
	tmp     *os.File
}

func (h *mutFileHandle) Read(req *fuse.ReadRequest, res *fuse.ReadResponse, intr fuse.Intr) fuse.Error {
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

func (h *mutFileHandle) Write(req *fuse.WriteRequest, res *fuse.WriteResponse, intr fuse.Intr) fuse.Error {
	if h.tmp == nil {
		log.Printf("Write called on camli mutFileHandle without a tempfile set")
		return fuse.EIO
	}

	n, err := h.tmp.WriteAt(req.Data, req.Offset)
	log.Printf("mutFileHandle.Write(%q, at %d, flags %v, %q) = %d, %v", h.f.fullPath(), req.Offset, req.Flags, req.Data, n, err)
	if err != nil {
		log.Println("mutFileHandle.Write:", err)
		return fuse.EIO
	}
	res.Size = n
	return nil
}

func (h *mutFileHandle) Release(req *fuse.ReleaseRequest, intr fuse.Intr) fuse.Error {
	if h.tmp == nil {
		log.Printf("Release called on camli mutFileHandle without a tempfile set")
		return fuse.EIO
	}
	log.Printf("mutFileHandle release.")
	_, err := h.tmp.Seek(0, 0)
	if err != nil {
		log.Println("mutFileHandle.Release:", err)
		return fuse.EIO
	}
	var n int64
	br, err := schema.WriteFileFromReader(h.f.fs.client, h.f.name, readerutil.CountingReader{Reader: h.tmp, N: &n})
	if err != nil {
		log.Println("mutFileHandle.Release:", err)
		return fuse.EIO
	}
	h.f.setContent(br, n)

	h.tmp.Close()
	os.Remove(h.tmp.Name())
	h.tmp = nil

	return nil
}

func (h *mutFileHandle) Truncate(size uint64, intr fuse.Intr) fuse.Error {
	if h.tmp == nil {
		log.Printf("Truncate called on camli mutFileHandle without a tempfile set")
		return fuse.EIO
	}

	log.Printf("mutFileHandle.Truncate(%q) to size %d", h.f.fullPath(), size)
	if err := h.tmp.Truncate(int64(size)); err != nil {
		log.Println("mutFileHandle.Truncate:", err)
		return fuse.EIO
	}
	return nil
}
