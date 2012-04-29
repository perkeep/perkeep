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

	"camlistore.org/third_party/code.google.com/p/rsc/fuse"
)

// root implements fuse.Node and is the typical root of a
// CamliFilesystem with a little hello message and the ability to
// search and browse static snapshots, etc.
type root struct {
	fs *CamliFileSystem
}

func (n *root) Attr() fuse.Attr {
	return fuse.Attr{
		Mode: os.ModeDir | 0755,
		Uid:  uint32(os.Getuid()),
		Gid:  uint32(os.Getgid()),
	}
}

func (n *root) ReadDir(intr fuse.Intr) ([]fuse.Dirent, fuse.Error) {
	return []fuse.Dirent{
		{Name: "WELCOME.txt"},
		{Name: "tag"},
		{Name: "date"},
		{Name: "sha1-xxx...."},
	}, nil
}

func (n *root) Lookup(name string, intr fuse.Intr) (fuse.Node, fuse.Error) {
	if name == ".quitquitquit" {
		log.Fatalf("Shutting down due to root .quitquitquit lookup.")
	}

	br := blobref.Parse(name)
	log.Printf("Root lookup of %q = %v", name, br)
	if br != nil {
		return &node{fs: n.fs, blobref: br}, nil
	}
	return nil, fuse.ENOENT
}
