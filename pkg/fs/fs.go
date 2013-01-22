/*
Copyright 2011 Google Inc.

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
	"bytes"
	"fmt"
	"io"
	"log"
	"os"
	"sync"
	"syscall"
	"time"

	"camlistore.org/pkg/blobref"
	"camlistore.org/pkg/lru"
	"camlistore.org/pkg/schema"

	"camlistore.org/third_party/code.google.com/p/rsc/fuse"
)

var _ = fmt.Println
var _ = log.Println
var _ = bytes.NewReader

var serverStart = time.Now()

var errNotDir = fuse.Errno(syscall.ENOTDIR)

type CamliFileSystem struct {
	fetcher blobref.SeekFetcher
	root    fuse.Node

	// IgnoreOwners, if true, collapses all file ownership to the
	// uid/gid running the fuse filesystem, and sets all the
	// permissions to 0600/0700.
	IgnoreOwners bool

	blobToSchema *lru.Cache // ~map[blobstring]*schema.Blob
	nameToBlob   *lru.Cache // ~map[string]*blobref.BlobRef
	nameToAttr   *lru.Cache // ~map[string]*fuse.Attr
}

var _ fuse.FS = (*CamliFileSystem)(nil)

func newCamliFileSystem(fetcher blobref.SeekFetcher) *CamliFileSystem {
	return &CamliFileSystem{
		fetcher:      fetcher,
		blobToSchema: lru.New(1024), // arbitrary; TODO: tunable/smarter?
		nameToBlob:   lru.New(1024), // arbitrary: TODO: tunable/smarter?
		nameToAttr:   lru.New(1024), // arbitrary: TODO: tunable/smarter?
	}
}

// NewCamliFileSystem returns a filesystem with a generic base, from which users
// can navigate by blobref, tag, date, etc.
func NewCamliFileSystem(fetcher blobref.SeekFetcher) *CamliFileSystem {
	fs := newCamliFileSystem(fetcher)
	fs.root = &root{fs: fs} // root.go
	return fs
}

// NewRootedCamliFileSystem returns a CamliFileSystem with root as its
// base.
func NewRootedCamliFileSystem(fetcher blobref.SeekFetcher, root *blobref.BlobRef) (*CamliFileSystem, error) {
	fs := newCamliFileSystem(fetcher)

	blob, err := fs.fetchSchemaMeta(root)
	if err != nil {
		return nil, err
	}
	if blob.Type() != "directory" {
		return nil, fmt.Errorf("Blobref must be of a directory, got a %v", blob.Type())
	}
	n := &node{fs: fs, blobref: root, meta: blob}
	n.populateAttr()
	fs.root = n
	return fs, nil
}

// node implements fuse.Node with a read-only Camli "file" or
// "directory" blob.
type node struct {
	fs      *CamliFileSystem
	blobref *blobref.BlobRef

	dmu     sync.Mutex    // guards dirents. acquire before mu.
	dirents []fuse.Dirent // nil until populated once

	mu      sync.Mutex // guards rest
	attr    fuse.Attr
	meta    *schema.Blob
	lookMap map[string]*blobref.BlobRef
}

func (n *node) Attr() (attr fuse.Attr) {
	_, err := n.schema()
	if err != nil {
		// Hm, can't return it. Just log it I guess.
		log.Printf("error fetching schema superset for %v: %v", n.blobref, err)
	}
	return n.attr
}

func (n *node) addLookupEntry(name string, ref *blobref.BlobRef) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.lookMap == nil {
		n.lookMap = make(map[string]*blobref.BlobRef)
	}
	n.lookMap[name] = ref
}

func (n *node) Lookup(name string, intr fuse.Intr) (fuse.Node, fuse.Error) {
	if name == ".quitquitquit" {
		// TODO: only in dev mode
		log.Fatalf("Shutting down due to .quitquitquit lookup.")
	}

	// If we haven't done Readdir yet (dirents isn't set), then force a Readdir
	// call to populate lookMap.
	n.dmu.Lock()
	loaded := n.dirents != nil
	n.dmu.Unlock()
	if !loaded {
		n.ReadDir(nil)
	}

	n.mu.Lock()
	defer n.mu.Unlock()
	ref, ok := n.lookMap[name]
	if !ok {
		return nil, fuse.ENOENT
	}
	return &node{fs: n.fs, blobref: ref}, nil
}

func (n *node) schema() (*schema.Blob, error) {
	// TODO: use singleflight library here instead of a lock?
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.meta != nil {
		return n.meta, nil
	}
	blob, err := n.fs.fetchSchemaMeta(n.blobref)
	if err == nil {
		n.meta = blob
		n.populateAttr()
	}
	return blob, err
}

func (n *node) Open(req *fuse.OpenRequest, res *fuse.OpenResponse, intr fuse.Intr) (fuse.Handle, fuse.Error) {
	log.Printf("CAMLI Open on %v: %#v", n.blobref, req)
	ss, err := n.schema()
	if err != nil {
		log.Printf("open of %v: %v", n.blobref, err)
		return nil, fuse.EIO
	}
	if ss.Type() == "directory" {
		return n, nil
	}
	fr, err := ss.NewFileReader(n.fs.fetcher)
	if err != nil {
		// Will only happen if ss.Type != "file" or "bytes"
		log.Printf("NewFileReader(%s) = %v", n.blobref, err)
		return nil, fuse.EIO
	}
	return &nodeReader{n: n, fr: fr}, nil
}

type nodeReader struct {
	n  *node
	fr *schema.FileReader
}

func (nr *nodeReader) Read(req *fuse.ReadRequest, res *fuse.ReadResponse, intr fuse.Intr) fuse.Error {
	log.Printf("CAMLI nodeReader READ on %v: %#v", nr.n.blobref, req)
	if req.Offset >= nr.fr.Size() {
		return nil
	}
	size := req.Size
	if int64(size) + req.Offset >= nr.fr.Size() {
		size -= int((int64(size) + req.Offset) - nr.fr.Size())
	}
	buf := make([]byte, size)
	n, err := nr.fr.ReadAt(buf, req.Offset)
	if err == io.EOF {
		err = nil
	}
	if err != nil {
		log.Printf("camli read on %v at %d: %v", nr.n.blobref, req.Offset, err)
		return fuse.EIO
	}
	res.Data = buf[:n]
	return nil
}

func (nr *nodeReader) Release(req *fuse.ReleaseRequest, intr fuse.Intr) fuse.Error {
	log.Printf("CAMLI nodeReader RELEASE on %v", nr.n.blobref)
	nr.fr.Close()
	return nil
}

func (n *node) ReadDir(intr fuse.Intr) ([]fuse.Dirent, fuse.Error) {
	log.Printf("CAMLI ReadDir on %v", n.blobref)
	n.dmu.Lock()
	defer n.dmu.Unlock()
	if n.dirents != nil {
		return n.dirents, nil
	}

	ss, err := n.schema()
	if err != nil {
		return nil, fuse.EIO
	}
	setRef := ss.DirectoryEntries()
	if setRef == nil {
		return nil, nil
	}
	log.Printf("fetching setref: %v...", setRef)
	setss, err := n.fs.fetchSchemaMeta(setRef)
	if err != nil {
		log.Printf("fetching %v for readdir on %v: %v", setRef, n.blobref, err)
		return nil, fuse.EIO
	}
	if setss.Type() != "static-set" {
		log.Printf("%v is not a static-set in readdir; is a %q", setRef, setss.Type())
		return nil, fuse.EIO
	}

	// TODO(bradfitz): push down information to the fetcher
	// (cachingfetcher -> remote client http) that we're going to load a
	// bunch, so the HTTP client (if not using SPDY) can do discovery
	// and see if the server supports a batch handler, then get them all
	// in one round-trip, rather than attacking the server with hundreds
	// of parallel TLS setups.

	// res is the result of fetchSchemaMeta.  the ssc slice of channels keeps them ordered
	// the same as they're listed in the schema's Members.
	type res struct {
		*blobref.BlobRef
		*schema.Blob
		error
	}
	var ssc []chan res
	for _, memberRef := range setss.StaticSetMembers() {
		ch := make(chan res, 1)
		ssc = append(ssc, ch)
		// TODO: move the cmd/camput/chanworker.go into its own package, and use it here. only
		// have 10 or so of these loading at once.  for now we do them all. 
		go func() {
			mss, err := n.fs.fetchSchemaMeta(memberRef)
			if err != nil {
				log.Printf("error reading entry %v in readdir: %v", memberRef, err)
			}
			ch <- res{memberRef, mss, err}
		}()
	}

	n.dirents = make([]fuse.Dirent, 0)
	for i, ch := range ssc {
		log.Printf("CAMLI dir %v set %v, waiting on entry %d/%d", n.blobref, setRef, i+1, len(ssc))
		r := <-ch
		memberRef, mss, err := r.BlobRef, r.Blob, r.error
		if err != nil {
			return nil, fuse.EIO
		}
		if filename := mss.FileName(); filename != "" {
			n.addLookupEntry(filename, memberRef)
			n.dirents = append(n.dirents, fuse.Dirent{
				Name: mss.FileName(),
			})
		}
	}
	return n.dirents, nil
}

// populateAttr should only be called once n.ss is known to be set and
// non-nil
func (n *node) populateAttr() error {
	meta := n.meta

	n.attr.Mode = meta.FileMode()

	if n.fs.IgnoreOwners {
		n.attr.Uid = uint32(os.Getuid())
		n.attr.Gid = uint32(os.Getgid())
		executeBit := n.attr.Mode & 0100
		n.attr.Mode = (n.attr.Mode ^ n.attr.Mode.Perm()) & 0400 & executeBit
	} else {
		n.attr.Uid = uint32(meta.MapUid())
		n.attr.Gid = uint32(meta.MapGid())
	}

	// TODO: inode?

	n.attr.Mtime = meta.ModTime()

	switch meta.Type() {
	case "file":
		n.attr.Size = uint64(meta.PartsSize())
		n.attr.Blocks = 0 // TODO: set?
	case "directory":
		// Nothing special? Just prevent default case.
	case "symlink":
		// Nothing special? Just prevent default case.
	default:
		log.Printf("unknown attr ss.Type %q in populateAttr", meta.Type())
	}
	return nil
}

func (fs *CamliFileSystem) Root() (fuse.Node, fuse.Error) {
	return fs.root, nil
}

func (fs *CamliFileSystem) Statfs(req *fuse.StatfsRequest, res *fuse.StatfsResponse, intr fuse.Intr) fuse.Error {
	log.Printf("CAMLI StatFS")
	// Make some stuff up, just to see if it makes "lsof" happy.
	res.Blocks = 1 << 35
	res.Bfree = 1 << 34
	res.Files = 1 << 29
	res.Ffree = 1 << 28
	res.Namelen = 2048
	res.Bsize = 1024
	return nil
}

// Errors returned are:
//    os.ErrNotExist -- blob not found
//    os.ErrInvalid -- not JSON or a camli schema blob
func (fs *CamliFileSystem) fetchSchemaMeta(br *blobref.BlobRef) (*schema.Blob, error) {
	blobStr := br.String()
	if blob, ok := fs.blobToSchema.Get(blobStr); ok {
		return blob.(*schema.Blob), nil
	}

	rsc, _, err := fs.fetcher.Fetch(br)
	if err != nil {
		return nil, err
	}
	defer rsc.Close()
	blob, err := schema.BlobFromReader(br, rsc)
	if err != nil {
		log.Printf("Error parsing %s as schema blob: %v", br, err)
		return nil, os.ErrInvalid
	}
	if blob.Type() == "" {
		log.Printf("blob %s is JSON but lacks camliType", br)
		return nil, os.ErrInvalid
	}
	fs.blobToSchema.Add(blobStr, blob)
	return blob, nil
}
