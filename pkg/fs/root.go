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

	"bazil.org/fuse"
	"bazil.org/fuse/fs"
	"camlistore.org/pkg/blob"
	"golang.org/x/net/context"
)

// root implements fuse.Node and is the typical root of a
// CamliFilesystem with a little hello message and the ability to
// search and browse static snapshots, etc.
type root struct {
	fs *CamliFileSystem

	mu     sync.Mutex // guards recent
	recent *recentDir
	roots  *rootsDir
	atDir  *atDir
}

var (
	_ fs.Node               = (*root)(nil)
	_ fs.HandleReadDirAller = (*root)(nil)
	_ fs.NodeStringLookuper = (*root)(nil)
)

func (n *root) Attr(ctx context.Context, a *fuse.Attr) error {
	a.Mode = os.ModeDir | 0700
	a.Uid = uint32(os.Getuid())
	a.Gid = uint32(os.Getgid())
	return nil
}

func (n *root) ReadDirAll(ctx context.Context) ([]fuse.Dirent, error) {
	return []fuse.Dirent{
		{Name: "WELCOME.txt"},
		{Name: "tag"},
		{Name: "date"},
		{Name: "recent"},
		{Name: "roots"},
		{Name: "at"},
		{Name: "sha1-xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"},
	}, nil
}

func (n *root) getRecentDir() *recentDir {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.recent == nil {
		n.recent = &recentDir{fs: n.fs}
	}
	return n.recent
}

func (n *root) getRootsDir() *rootsDir {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.roots == nil {
		n.roots = &rootsDir{fs: n.fs}
	}
	return n.roots
}

func (n *root) getAtDir() *atDir {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.atDir == nil {
		n.atDir = &atDir{fs: n.fs}
	}
	return n.atDir
}

func (n *root) Lookup(ctx context.Context, name string) (fs.Node, error) {
	log.Printf("root.Lookup(%s)", name)
	switch name {
	case ".quitquitquit":
		log.Fatalf("Shutting down due to root .quitquitquit lookup.")
	case "WELCOME.txt":
		return staticFileNode("Welcome to CamlistoreFS.\n\nFor now you can only cd into a sha1-xxxx directory, if you know the blobref of a directory or a file.\n"), nil
	case "recent":
		return n.getRecentDir(), nil
	case "tag", "date":
		return notImplementDirNode{}, nil
	case "at":
		return n.getAtDir(), nil
	case "roots":
		return n.getRootsDir(), nil
	case "sha1-xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx":
		return notImplementDirNode{}, nil
	case ".camli_fs_stats":
		return statsDir{}, nil
	case "mach_kernel", ".hidden", "._.":
		// Just quiet some log noise on OS X.
		return nil, fuse.ENOENT
	}

	if br, ok := blob.Parse(name); ok {
		log.Printf("Root lookup of blobref. %q => %v", name, br)
		return &node{fs: n.fs, blobref: br}, nil
	}
	log.Printf("Bogus root lookup of %q", name)
	return nil, fuse.ENOENT
}
