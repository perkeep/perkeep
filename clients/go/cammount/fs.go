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

package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	"camli/blobref"
	"camli/client"
	"camli/third_party/github.com/hanwen/go-fuse/fuse"
)

var _ = fmt.Println
var _ = log.Println

type CamliFileSystem struct {
	client *client.Client
	root   *blobref.BlobRef
}

func NewCamliFileSystem(client *client.Client, root *blobref.BlobRef) *CamliFileSystem {
	return &CamliFileSystem{client: client, root: root}
}

// Where name == "" for root,
// Returns nil on failure
func (fs *CamliFileSystem) blobRefFromName(name string) *blobref.BlobRef {
	parts := filepath.SplitList(name)
	if name == "" || len(parts) == 0 {
		return fs.root
	}
	// TODO: walk
	return nil
}

func (fs *CamliFileSystem) Mount(connector *fuse.PathFileSystemConnector) fuse.Status {
	log.Printf("cammount: Mount")
	return fuse.OK
}

func (fs *CamliFileSystem) Unmount() {
	log.Printf("cammount: Unmount.")
}

func (fs *CamliFileSystem) GetAttr(name string) (*fuse.Attr, fuse.Status) {
	blobref := fs.blobRefFromName(name)
	if blobref == nil {
		return nil, fuse.ENOENT
	}
	log.Printf("cammount: GetAttr(%q)", name)
	out := new(fuse.Attr)
	var fi os.FileInfo
	// TODO
	fuse.CopyFileInfo(&fi, out)
	return out, fuse.OK
}

func (fs *CamliFileSystem) Access(name string, mode uint32) fuse.Status {
	log.Printf("cammount: Access(%q, %d)", name, mode)
	return fuse.OK
}

func (fs *CamliFileSystem) Open(name string, flags uint32) (file fuse.RawFuseFile, code fuse.Status) {
	log.Printf("cammount: Open(%q, %d)", name, flags)
	// TODO
	return nil, fuse.EACCES
}

func (fs *CamliFileSystem) OpenDir(name string) (stream chan fuse.DirEntry, code fuse.Status) {
	log.Printf("cammount: OpenDir(%q)", name)
	// TODO
	return nil, fuse.EACCES
}

func (fs *CamliFileSystem) Readlink(name string) (string, fuse.Status) {
	log.Printf("cammount: Readlink(%q)", name)
	// TODO
	return "", fuse.EACCES
}

// *************************************************************************
// EACCESS stuff

func (fs *CamliFileSystem) Chmod(name string, mode uint32) fuse.Status {
	return fuse.EACCES
}

func (fs *CamliFileSystem) Chown(name string, uid uint32, gid uint32) fuse.Status {
	return fuse.EACCES
}

func (fs *CamliFileSystem) Create(name string, flags uint32, mode uint32) (file fuse.RawFuseFile, code fuse.Status) {
	code = fuse.EACCES
	return
}

func (fs *CamliFileSystem) Link(oldName string, newName string) fuse.Status {
	return fuse.EACCES
}

func (fs *CamliFileSystem) Mkdir(name string, mode uint32) fuse.Status {
	return fuse.EACCES
}

func (fs *CamliFileSystem) Mknod(name string, mode uint32, dev uint32) fuse.Status {
	return fuse.EACCES
}

func (fs *CamliFileSystem) Rename(oldName string, newName string) (code fuse.Status) {
	return fuse.EACCES
}

func (fs *CamliFileSystem) Rmdir(name string) fuse.Status {
	return fuse.EACCES
}

func (fs *CamliFileSystem) Symlink(value string, linkName string) fuse.Status {
	return fuse.EACCES
}

func (fs *CamliFileSystem) Truncate(name string, offset uint64) fuse.Status {
	return fuse.EACCES
}

func (fs *CamliFileSystem) Unlink(name string) fuse.Status {
	return fuse.EACCES
}

func (fs *CamliFileSystem) Utimens(name string, AtimeNs uint64, CtimeNs uint64) fuse.Status {
	return fuse.EACCES
}
