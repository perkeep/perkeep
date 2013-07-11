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

	"camlistore.org/pkg/blobref"
	"camlistore.org/pkg/search"

	"camlistore.org/third_party/code.google.com/p/rsc/fuse"
)

type rootsDir struct {
	fs *CamliFileSystem
}

func (n *rootsDir) Attr() fuse.Attr {
	return fuse.Attr{
		Mode: os.ModeDir | 0700,
		Uid:  uint32(os.Getuid()),
		Gid:  uint32(os.Getgid()),
	}
}

func (n *rootsDir) ReadDir(intr fuse.Intr) ([]fuse.Dirent, fuse.Error) {
	log.Printf("fs.roots: ReadDir / searching")

	req := &search.WithAttrRequest{N: 100, Attr: "camliRoot"}
	res, err := n.fs.client.GetPermanodesWithAttr(req)
	if err != nil {
		log.Printf("fs.recent: GetRecentPermanodes error in ReadDir: %v", err)
		return nil, fuse.EIO
	}

	var ents []fuse.Dirent
	for _, wi := range res.WithAttr {
		// TODO(adg): do a describe on the permanode so we can figure out its camliRoot value
		// eg: "camliRoot": ["dev-pics-root"] becomes ent name "dev-pics-root"
		ents = append(ents, fuse.Dirent{
			Name: wi.Permanode.String(),
		})
	}
	log.Printf("fs.recent returning %d entries", len(ents))
	return ents, nil
}

func (n *rootsDir) Lookup(name string, intr fuse.Intr) (fuse.Node, fuse.Error) {
	log.Printf("fs.roots: Lookup(%q)", name)
	br := blobref.Parse(name)
	if br == nil {
		return nil, fuse.ENOENT
	}
	nod := &mutDir{
		fs: n.fs,
		br: br,
	}
	return nod, nil
}
