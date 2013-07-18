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
	"log"
	"os"
	"sync"
	"time"

	"camlistore.org/pkg/blobref"
	"camlistore.org/pkg/schema"
	"camlistore.org/pkg/search"
	"camlistore.org/third_party/code.google.com/p/rsc/fuse"
)

const refreshTime = 1 * time.Minute

type rootsDir struct {
	fs *CamliFileSystem

	mu        sync.Mutex // guards following
	lastQuery time.Time
	m         map[string]*blobref.BlobRef // ent name => permanode
}

func (n *rootsDir) Attr() fuse.Attr {
	return fuse.Attr{
		Mode: os.ModeDir | 0700,
		Uid:  uint32(os.Getuid()),
		Gid:  uint32(os.Getgid()),
	}
}

func (n *rootsDir) ReadDir(intr fuse.Intr) ([]fuse.Dirent, fuse.Error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if err := n.condRefresh(); err != nil {
		return nil, fuse.EIO
	}
	var ents []fuse.Dirent
	for name := range n.m {
		ents = append(ents, fuse.Dirent{Name: name})
	}
	return ents, nil
}

func (n *rootsDir) Lookup(name string, intr fuse.Intr) (fuse.Node, fuse.Error) {
	log.Printf("fs.roots: Lookup(%q)", name)
	n.mu.Lock()
	defer n.mu.Unlock()
	if err := n.condRefresh(); err != nil {
		return nil, err
	}
	br := n.m[name]
	if br == nil {
		return nil, fuse.ENOENT
	}
	nod := &mutDir{
		fs:        n.fs,
		permanode: br,
		name:      name,
	}
	return nod, nil
}

// requires n.mu is held
func (n *rootsDir) condRefresh() fuse.Error {
	if n.lastQuery.After(time.Now().Add(-refreshTime)) {
		return nil
	}
	log.Printf("fs.roots: querying")

	req := &search.WithAttrRequest{N: 100, Attr: "camliRoot"}
	wres, err := n.fs.client.GetPermanodesWithAttr(req)
	if err != nil {
		log.Printf("fs.recent: GetRecentPermanodes error in ReadDir: %v", err)
		return fuse.EIO
	}

	dr := &search.DescribeRequest{
		Depth: 1,
	}
	for _, wi := range wres.WithAttr {
		dr.BlobRefs = append(dr.BlobRefs, wi.Permanode)
	}
	dres, err := n.fs.client.Describe(dr)
	if err != nil {
		log.Printf("Describe failure: %v", err)
		return fuse.EIO
	}

	n.m = make(map[string]*blobref.BlobRef)

	for _, wi := range wres.WithAttr {
		pn := wi.Permanode
		db := dres.Meta[pn.String()]
		if db != nil && db.Permanode != nil {
			name := db.Permanode.Attr.Get("camliRoot")
			if name != "" {
				n.m[name] = pn
			}
		}
	}
	n.lastQuery = time.Now()
	return nil
}

func (n *rootsDir) Mkdir(req *fuse.MkdirRequest, intr fuse.Intr) (fuse.Node, fuse.Error) {
	name := req.Name

	// Create a Permanode for the root.
	pr, err := n.fs.client.UploadNewPermanode()
	if err != nil {
		log.Printf("rootsDir.Create(%q): %v", name, err)
		return nil, fuse.EIO
	}

	// Add a camliRoot attribute to the root permanode.
	claim := schema.NewSetAttributeClaim(pr.BlobRef, "camliRoot", name)
	_, err = n.fs.client.UploadAndSignBlob(claim)
	if err != nil {
		log.Printf("rootsDir.Create(%q): %v", name, err)
		return nil, fuse.EIO
	}

	nod := &mutDir{
		fs:        n.fs,
		permanode: pr.BlobRef,
		name:      name,
	}
	n.mu.Lock()
	n.m[name] = pr.BlobRef
	n.mu.Unlock()

	return nod, nil
}
