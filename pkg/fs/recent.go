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
	"log"
	"os"
	"path"
	"sync"
	"time"

	"camlistore.org/pkg/blobref"
	"camlistore.org/pkg/search"

	"camlistore.org/third_party/code.google.com/p/rsc/fuse"
)

// recentDir implements fuse.Node and is a directory of recent
// permanodes' files, for permanodes with a camliContent pointing to a
// "file".
type recentDir struct {
	fs *CamliFileSystem

	mu      sync.Mutex
	ents    map[string]*search.DescribedBlob // filename to blob meta
	modTime map[string]time.Time             // filename to permanode modtime
}

func (n *recentDir) Attr() fuse.Attr {
	return fuse.Attr{
		Mode: os.ModeDir | 0700,
		Uid:  uint32(os.Getuid()),
		Gid:  uint32(os.Getgid()),
	}
}

func (n *recentDir) ReadDir(intr fuse.Intr) ([]fuse.Dirent, fuse.Error) {
	log.Printf("fs.recent: ReadDir / searching")
	n.mu.Lock()
	defer n.mu.Unlock()

	n.ents = make(map[string]*search.DescribedBlob)
	n.modTime = make(map[string]time.Time)

	req := &search.RecentRequest{N: 100}
	res, err := n.fs.client.GetRecentPermanodes(req)
	if err != nil {
		log.Printf("fs.recent: GetRecentPermanodes error in ReadDir: %v", err)
		return nil, fuse.EIO
	}

	var ents []fuse.Dirent
	for _, ri := range res.Recent {
		meta := res.Meta.Get(ri.BlobRef)
		if meta == nil || meta.Permanode == nil {
			continue
		}
		cc := blobref.Parse(meta.Permanode.Attr.Get("camliContent"))
		if cc == nil {
			continue
		}
		ccMeta := res.Meta.Get(cc)
		if ccMeta == nil {
			continue
		}
		var name string
		switch {
		case ccMeta.File != nil:
			name = ccMeta.File.FileName
		case ccMeta.Dir != nil:
			name = ccMeta.Dir.FileName
		default:
			continue
		}
		if name == "" || n.ents[name] != nil {
			name = ccMeta.BlobRef.String() + path.Ext(name)
			if n.ents[name] != nil {
				continue
			}
		}
		n.ents[name] = ccMeta
		n.modTime[name] = ri.ModTime.Time()
		log.Printf("fs.recent: name %q = %v (at %v)", name, ccMeta.BlobRef, ri.ModTime.Time())
		ents = append(ents, fuse.Dirent{
			Name: name,
		})
	}
	log.Printf("fs.recent returning %d entries", len(ents))
	return ents, nil
}

func (n *recentDir) Lookup(name string, intr fuse.Intr) (fuse.Node, fuse.Error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.ents == nil {
		// Odd case: a Lookup before a Readdir. Force a readdir to
		// seed our map. Mostly hit just during development.
		n.mu.Unlock() // release, since ReadDir will acquire
		n.ReadDir(intr)
		n.mu.Lock()
	}
	db := n.ents[name]
	log.Printf("fs.recent: Lookup(%q) = %v", name, db)
	if db == nil {
		return nil, fuse.ENOENT
	}
	nod := &node{
		fs:           n.fs,
		blobref:      db.BlobRef,
		pnodeModTime: n.modTime[name],
	}
	return nod, nil
}
