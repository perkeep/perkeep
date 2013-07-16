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
	parent    *mutDir
	name      string // ent name (base name within parent)

	mu       sync.Mutex
	lastPop  time.Time
	children map[string]fuse.Node
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
	for name := range n.children {
		ents = append(ents, fuse.Dirent{
			Name: name,
		})
	}
	return ents, nil
}

func (n *mutDir) Lookup(name string, intr fuse.Intr) (fuse.Node, fuse.Error) {
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

func (n *mutDir) Create(req *fuse.CreateRequest, res *fuse.CreateResponse, intr fuse.Intr) (fuse.Node, fuse.Handle, fuse.Error) {
	child, err := n.creat(req.Name, false)
	if err != nil {
		log.Printf("mutDir.Mkdir(%q): %v", req.Name, err)
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

	mu      sync.Mutex
	content *blobref.BlobRef
	size    int64
}

func (n *mutFile) Attr() fuse.Attr {
	return fuse.Attr{
		Mode: 0600, // writable!
		Uid:  uint32(os.Getuid()),
		Gid:  uint32(os.Getgid()),
		Size: uint64(n.size),
		// TODO(adg): use the real stuff here
		Mtime:  serverStart,
		Ctime:  serverStart,
		Crtime: serverStart,
	}
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

func (n *mutFile) Open(req *fuse.OpenRequest, res *fuse.OpenResponse, intr fuse.Intr) (fuse.Handle, fuse.Error) {
	log.Printf("mutFile.Open: %v: content: %v", n.permanode, n.content)
	r, err := schema.NewFileReader(n.fs.fetcher, n.content)
	if err != nil {
		log.Printf("mutFile.Open: %v", err)
		return nil, fuse.EIO
	}
	defer r.Close()
	return n.newHandle(r)
}

func (n *mutFile) Fsync(r *fuse.FsyncRequest, intr fuse.Intr) fuse.Error {
	// TODO(adg): in the fuse package, plumb through fsync to mutFileHandle
	// in the same way we did Truncate.
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
	written bool
}

func (h *mutFileHandle) Read(req *fuse.ReadRequest, res *fuse.ReadResponse, intr fuse.Intr) fuse.Error {
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
	h.written = true
	n, err := h.tmp.WriteAt(req.Data, req.Offset)
	if err != nil {
		log.Println("mutFileHandle.Write:", err)
		return fuse.EIO
	}
	res.Size = n
	return nil
}

func (h *mutFileHandle) Release(req *fuse.ReleaseRequest, intr fuse.Intr) fuse.Error {
	if h.written {
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
	}
	h.tmp.Close()
	os.Remove(h.tmp.Name())
	return nil
}

func (h *mutFileHandle) Truncate(size uint64, intr fuse.Intr) fuse.Error {
	h.written = true
	if err := h.tmp.Truncate(int64(size)); err != nil {
		log.Println("mutFileHandle.Truncate:", err)
		return fuse.EIO
	}
	return nil
}
