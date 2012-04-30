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
	"encoding/json"
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

	blobToSchema *lru.Cache // ~map[blobstring]*schema.Superset
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

func NewCamliFileSystem(fetcher blobref.SeekFetcher) *CamliFileSystem {
	fs := newCamliFileSystem(fetcher)
	fs.root = &root{fs: fs} // root.go
	return fs
}

// NewRootedCamliFileSystem returns a CamliFileSystem with root as its
// base.
func NewRootedCamliFileSystem(fetcher blobref.SeekFetcher, root *blobref.BlobRef) (*CamliFileSystem, error) {
	fs := newCamliFileSystem(fetcher)

	ss, err := fs.fetchSchemaSuperset(root)
	if err != nil {
		return nil, err
	}
	n := &node{fs: fs, blobref: root, ss: ss}
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
	ss      *schema.Superset
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

func (n *node) schema() (*schema.Superset, error) {
	// TODO: use singleflight library here instead of a lock?
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.ss != nil {
		return n.ss, nil
	}
	ss, err := n.fs.fetchSchemaSuperset(n.blobref)
	if err == nil {
		n.ss = ss
		n.populateAttr()
	}
	return ss, err
}

func (n *node) Open(req *fuse.OpenRequest, res *fuse.OpenResponse, intr fuse.Intr) (fuse.Handle, fuse.Error) {
	log.Printf("CAMLI Open on %v: %#v", n.blobref, req)
	ss, err := n.schema()
	if err != nil {
		log.Printf("open of %v: %v", n.blobref, err)
		return nil, fuse.EIO
	}
	if ss.Type == "directory" {
		return n, nil
	}
	return &nodeReader{n: n, ss: ss}, nil
}

type nodeReader struct {
	n  *node
	ss *schema.Superset // not nil, always of type "file" or "bytes"
}

func (nr *nodeReader) Read(req *fuse.ReadRequest, res *fuse.ReadResponse, intr fuse.Intr) fuse.Error {
	log.Printf("CAMLI nodeReader READ on %v: %#v", nr.n.blobref, req)

	// TODO: this isn't incredibly efficient, creating a new
	// FileReader for each read chunk.  We could do better here
	// and re-use a locked pool of readers, trying to find the one
	// with a current offset <= req.Offset first.
	fr, err := nr.ss.NewFileReader(nr.n.fs.fetcher)
	if err != nil {
		panic(err)
	}
	fr.Skip(uint64(req.Offset))
	buf := make([]byte, req.Size)
	n, err := fr.Read(buf)
	if err != nil && err != io.EOF {
		log.Printf("camli read on %v at %d: %v", nr.n.blobref, req.Offset, err)
		return fuse.EIO
	}
	res.Data = buf[:n]
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
	setRef := blobref.Parse(ss.Entries)
	if setRef == nil {
		return nil, nil
	}
	log.Printf("fetching setref: %v...", setRef)
	setss, err := n.fs.fetchSchemaSuperset(setRef)
	if err != nil {
		log.Printf("fetching %v for readdir on %v: %v", setRef, n.blobref, err)
		return nil, fuse.EIO
	}
	if setss.Type != "static-set" {
		log.Printf("%v is not a static-set in readdir; is a %q", setRef, setss.Type)
		return nil, fuse.EIO
	}

	// res is the result of fetchSchemaSuperset.  the ssc slice of channels keeps them ordered
	// the same as they're listed in the schema's Members.
	type res struct {
		*blobref.BlobRef
		*schema.Superset
		error
	}
	var ssc []chan res
	for _, member := range setss.Members {
		memberRef := blobref.Parse(member)
		if memberRef == nil {
			log.Printf("invalid blobref of %q in static set %s", member, setRef)
			return nil, fuse.EIO
		}
		ch := make(chan res, 1)
		ssc = append(ssc, ch)
		// TODO: move the cmd/camput/chanworker.go into its own package, and use it here. only
		// have 10 or so of these loading at once.  for now we do them all. 
		go func() {
			mss, err := n.fs.fetchSchemaSuperset(memberRef)
			if err != nil {
				log.Printf("error reading entry %v in readdir: %v", memberRef, err)
			}
			ch <- res{memberRef, mss, err}
		}()
	}

	n.dirents = make([]fuse.Dirent, 0)
	for _, ch := range ssc {
		r := <-ch
		memberRef, mss, err := r.BlobRef, r.Superset, r.error
		if err != nil {
			return nil, fuse.EIO
		}
		if filename := mss.FileNameString(); filename != "" {
			n.addLookupEntry(filename, memberRef)
			n.dirents = append(n.dirents, fuse.Dirent{
				Name: mss.FileNameString(),
			})
		}
	}
	return n.dirents, nil
}

// populateAttr should only be called once n.ss is known to be set and
// non-nil
func (n *node) populateAttr() error {
	ss := n.ss

	n.attr.Mode = ss.FileMode()

	if n.fs.IgnoreOwners {
		n.attr.Uid = uint32(os.Getuid())
		n.attr.Gid = uint32(os.Getgid())
		executeBit := n.attr.Mode & 0100
		n.attr.Mode = (n.attr.Mode ^ n.attr.Mode.Perm()) & 0400 & executeBit
	} else {
		n.attr.Uid = uint32(ss.MapUid())
		n.attr.Gid = uint32(ss.MapGid())
	}

	// TODO: inode?

	n.attr.Mtime = ss.ModTime()

	switch ss.Type {
	case "file":
		n.attr.Size = ss.SumPartsSize()
		n.attr.Blocks = 0 // TODO: set?
	case "directory":
		// Nothing special? Just prevent default case.
	case "symlink":
		// Nothing special? Just prevent default case.
	default:
		log.Printf("unknown attr ss.Type %q in populateAttr", ss.Type)
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
func (fs *CamliFileSystem) fetchSchemaSuperset(br *blobref.BlobRef) (*schema.Superset, error) {
	blobStr := br.String()
	if ss, ok := fs.blobToSchema.Get(blobStr); ok {
		return ss.(*schema.Superset), nil
	}

	rsc, _, err := fs.fetcher.Fetch(br)
	if err != nil {
		return nil, err
	}
	defer rsc.Close()
	jd := json.NewDecoder(rsc)
	ss := new(schema.Superset)
	err = jd.Decode(ss)
	if err != nil {
		log.Printf("Error parsing %s as schema blob: %v", br, err)
		return nil, os.ErrInvalid
	}
	if ss.Type == "" {
		log.Printf("blob %s is JSON but lacks camliType", br)
		return nil, os.ErrInvalid
	}
	ss.BlobRef = br
	fs.blobToSchema.Add(blobStr, ss)
	return ss, nil
}
