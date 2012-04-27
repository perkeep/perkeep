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

var errNotDir = fuse.Errno(syscall.ENOTDIR)

type CamliFileSystem struct {
	fetcher blobref.SeekFetcher
	root    *blobref.BlobRef

	blobToSchema *lru.Cache // ~map[blobstring]*schema.Superset
	nameToBlob   *lru.Cache // ~map[string]*blobref.BlobRef
	nameToAttr   *lru.Cache // ~map[string]*fuse.Attr
}

type CamliFile struct {
	fs   *CamliFileSystem
	blob *blobref.BlobRef
	ss   *schema.Superset

	size uint64 // memoized
}


var _ fuse.FS = (*CamliFileSystem)(nil)

func NewCamliFileSystem(fetcher blobref.SeekFetcher, root *blobref.BlobRef) *CamliFileSystem {
	return &CamliFileSystem{
		fetcher:      fetcher,
		blobToSchema: lru.New(1024), // arbitrary; TODO: tunable/smarter?
		root:         root,
		nameToBlob:   lru.New(1024), // arbitrary: TODO: tunable/smarter?
		nameToAttr:   lru.New(1024), // arbitrary: TODO: tunable/smarter?
	}
}

type node struct {
	fs      *CamliFileSystem
	blobref *blobref.BlobRef
	attr     fuse.Attr
}

func (n *node) Attr() (attr fuse.Attr) {
	return n.attr
}

func (n *node) populateAttr(ss *schema.Superset) error {
	// TODO: common stuff:
	// Uid uint32
	// Gid uint32
	// Mtime time.Time
	// inode?

	switch ss.Type {
	case "directory":
		n.attr.Mode = os.ModeDir
	case "file":
		n.attr.Size = ss.SumPartsSize()
		n.attr.Blocks = 0 // TODO: set?
		n.attr.Mtime = time.Time{} // TODO: set, for sure.
		n.attr.Mode = 0
	default:
		err := fmt.Errorf("unknown attr type %q in populateAttr", ss.Type)
		log.Print(err.Error())
		return err
	}
	return nil
}

func (fs *CamliFileSystem) Root() (fuse.Node, fuse.Error) {
	ss, err := fs.fetchSchemaSuperset(fs.root)
	if err != nil {
		// TODO: map these to fuse.Error better
		log.Printf("Error fetching root: %v", err)
		return nil, fuse.EIO
	}
	n := &node{fs: fs, blobref: fs.root}
	n.populateAttr(ss)
	return n, nil
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

func (file *CamliFile) GetReader() (io.ReadCloser, error) {
	return file.ss.NewFileReader(file.fs.fetcher)
}
