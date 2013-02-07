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
	"os"

	//"camlistore.org/pkg/blobref"

	"camlistore.org/third_party/code.google.com/p/rsc/fuse"
)

// recentDir implements fuse.Node and is a directory of recent
// permanodes' files, for permanodes with a camliContent pointing to a
// "file".
type recentDir struct {
	fs *CamliFileSystem
}

func (n *recentDir) Attr() fuse.Attr {
	return fuse.Attr{
		Mode: os.ModeDir | 0700,
		Uid:  uint32(os.Getuid()),
		Gid:  uint32(os.Getgid()),
	}
}

func (n *recentDir) ReadDir(intr fuse.Intr) ([]fuse.Dirent, fuse.Error) {
	var ents []fuse.Dirent
	ents = append(ents, fuse.Dirent{Name: "TODO"})
	// TODO: ...
	return ents, nil
}

func (n *recentDir) Lookup(name string, intr fuse.Intr) (fuse.Node, fuse.Error) {
	return nil, fuse.ENOENT
}
