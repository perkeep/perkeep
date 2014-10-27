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
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/search"

	"camlistore.org/third_party/bazil.org/fuse"
	"camlistore.org/third_party/bazil.org/fuse/fs"
)

// recentDir implements fuse.Node and is a directory of recent
// permanodes' files, for permanodes with a camliContent pointing to a
// "file".
type recentDir struct {
	noXattr
	fs *CamliFileSystem

	mu          sync.Mutex
	ents        map[string]*search.DescribedBlob // filename to blob meta
	modTime     map[string]time.Time             // filename to permanode modtime
	lastReaddir time.Time
	lastNames   []string
}

func (n *recentDir) Attr() fuse.Attr {
	return fuse.Attr{
		Mode: os.ModeDir | 0500,
		Uid:  uint32(os.Getuid()),
		Gid:  uint32(os.Getgid()),
	}
}

const recentSearchInterval = 10 * time.Second

func (n *recentDir) ReadDir(intr fs.Intr) ([]fuse.Dirent, fuse.Error) {
	var ents []fuse.Dirent

	n.mu.Lock()
	defer n.mu.Unlock()
	if n.lastReaddir.After(time.Now().Add(-recentSearchInterval)) {
		log.Printf("fs.recent: ReadDir from cache")
		for _, name := range n.lastNames {
			ents = append(ents, fuse.Dirent{Name: name})
		}
		return ents, nil
	}

	log.Printf("fs.recent: ReadDir, doing search")

	n.ents = make(map[string]*search.DescribedBlob)
	n.modTime = make(map[string]time.Time)

	req := &search.RecentRequest{N: 100}
	res, err := n.fs.client.GetRecentPermanodes(req)
	if err != nil {
		log.Printf("fs.recent: GetRecentPermanodes error in ReadDir: %v", err)
		return nil, fuse.EIO
	}

	n.lastNames = nil
	for _, ri := range res.Recent {
		modTime := ri.ModTime.Time()
		meta := res.Meta.Get(ri.BlobRef)
		if meta == nil || meta.Permanode == nil {
			continue
		}
		cc, ok := blob.Parse(meta.Permanode.Attr.Get("camliContent"))
		if !ok {
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
			if mt := ccMeta.File.Time; !mt.IsZero() {
				modTime = mt.Time()
			}
		case ccMeta.Dir != nil:
			name = ccMeta.Dir.FileName
		default:
			continue
		}
		if name == "" || n.ents[name] != nil {
			ext := filepath.Ext(name)
			if ext == "" && ccMeta.File != nil && strings.HasSuffix(ccMeta.File.MIMEType, "image/jpeg") {
				ext = ".jpg"
			}
			name = strings.TrimPrefix(ccMeta.BlobRef.String(), "sha1-")[:10] + ext
			if n.ents[name] != nil {
				continue
			}
		}
		n.ents[name] = ccMeta
		n.modTime[name] = modTime
		log.Printf("fs.recent: name %q = %v (at %v -> %v)", name, ccMeta.BlobRef, ri.ModTime.Time(), modTime)
		n.lastNames = append(n.lastNames, name)
		ents = append(ents, fuse.Dirent{
			Name: name,
		})
	}
	log.Printf("fs.recent returning %d entries", len(ents))
	n.lastReaddir = time.Now()
	return ents, nil
}

func (n *recentDir) Lookup(name string, intr fs.Intr) (fs.Node, fuse.Error) {
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
