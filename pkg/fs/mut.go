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
	"log"
	"os"
	"strings"
	"sync"

	"camlistore.org/pkg/blobref"
	"camlistore.org/pkg/search"

	"camlistore.org/third_party/code.google.com/p/rsc/fuse"
)

// mutDir is a mutable directory.
// Its br is the permanode with camliPath:entname attributes.
type mutDir struct {
	fs   *CamliFileSystem
	br   *blobref.BlobRef // root permanode
	d    *mutDir          // parent directory
	name string           // ent name (base name within d)

	mu    sync.Mutex
	dirs  map[string]*mutDir
	files map[string]*mutFile
}

func (n *mutDir) Attr() fuse.Attr {
	return fuse.Attr{
		Mode: os.ModeDir | 0700,
		Uid:  uint32(os.Getuid()),
		Gid:  uint32(os.Getgid()),
	}
}

func (n *mutDir) populate() error {
	// TODO(adg): cache this intelligently

	res, err := n.fs.client.Describe(&search.DescribeRequest{
		BlobRef: n.br,
		Depth:   3,
	})
	if err != nil {
		log.Println("mutDir.paths:", err)
		return nil
	}
	db := res.Meta[n.br.String()]
	if db == nil {
		return errors.New("dir blobref not described")
	}
	n.mu.Lock()
	defer n.mu.Unlock()

	// Find all child permanodes, and stick them in n.dirs or n.files
	for k, v := range db.Permanode.Attr {
		const p = "camliPath:"
		if !strings.HasPrefix(k, p) || len(v) < 1 {
			continue
		}
		name := k[len(p):]
		childRef := v[0]
		child := res.Meta[childRef]
		if child == nil {
			log.Println("child not described: %v", childRef)
			continue
		}
		if contentRef := child.Permanode.Attr.Get("camliContent"); contentRef != "" {
			// This is a file.
			content := res.Meta[contentRef]
			if content == nil {
				log.Println("child content not described: %v", childRef)
				continue
			}
			if content.CamliType != "file" {
				log.Println("child not a file: %v", childRef)
				continue
			}
			if n.files == nil {
				n.files = make(map[string]*mutFile)
			}
			n.files[name] = &mutFile{
				node: node{
					fs:      n.fs,
					blobref: blobref.Parse(contentRef),
				},
				permanode: blobref.Parse(childRef),
				d:         n,
				name:      name,
			}
			continue
		}
		// This is a directory.
		if n.dirs == nil {
			n.dirs = make(map[string]*mutDir)
		}
		n.dirs[name] = &mutDir{
			fs:   n.fs,
			br:   blobref.Parse(childRef),
			d:    n,
			name: name,
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
	for name := range n.dirs {
		ents = append(ents, fuse.Dirent{
			Name: name,
		})
	}
	for name := range n.files {
		ents = append(ents, fuse.Dirent{
			Name: name,
		})
	}
	return ents, nil
}

func (n *mutDir) Lookup(name string, intr fuse.Intr) (fuse.Node, fuse.Error) {
	log.Printf("fs.mutDir: Lookup(%q)", name)
	if err := n.populate(); err != nil {
		log.Println("populate:", err)
		return nil, fuse.EIO
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if d := n.dirs[name]; d != nil {
		return d, nil
	}
	if f := n.files[name]; f != nil {
		return f, nil
	}
	return nil, fuse.ENOENT
}

type mutFile struct {
	permanode *blobref.BlobRef
	d         *mutDir // parent directory
	name      string  // ent name (base name within d)

	node // read-only file node
}
