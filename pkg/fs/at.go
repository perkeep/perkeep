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

	"camlistore.org/third_party/bazil.org/fuse"
	fusefs "camlistore.org/third_party/bazil.org/fuse/fs"
)

type atDir struct {
	noXattr
	fs *CamliFileSystem
}

func (n *atDir) Attr() fuse.Attr {
	return fuse.Attr{
		Mode: os.ModeDir | 0500,
		Uid:  uint32(os.Getuid()),
		Gid:  uint32(os.Getgid()),
	}
}

func (n *atDir) ReadDir(intr fusefs.Intr) ([]fuse.Dirent, fuse.Error) {
	return []fuse.Dirent{
		{Name: "README.txt"},
	}, nil
}

const atReadme = `You are now in the "at" filesystem, where you can look into the past.

Locations in the top-level of this directory are dynamically created
as you request them.  A dynamic directory is designated by a
timestamp.  Once you enter a directory, you'll have a read-only view
of all of the roots that existed as of the specified time.

Example:

If you had a root called "importantstuff" and a file in it called
"todo.txt", you can look at the contents of that file as it existed
back before Christmas like this (from the location you mounted
camlistore):

    cat at/2013-12-24/importantstuff/todo.txt

If you cd into "at/2013-12-24/importantstuff" you can also see all the
files that you deleted since (but none that were created after).

Timestamps are specified in UTC unless otherwise specified, and may be
in any of the following forms:

With Nanosecond Granularity

* 2012-08-28T21:24:35.37465188Z - RFC3339 (this is the canonical format)
* 1346189075374651880 - nanoseconds since 1970-1-1

With Millisecond Granularity

* 1346189075374 - milliseconds since 1970-1-1, common in java

With Second Granularity

* 1346189075 - seconds since 1970-1-1, common in unix
* 2012-08-28T21:24:35Z - RFC3339
* 2012-08-28T21:24:35-08:00 - RFC3339 with numeric timezone
* Tue, 28 Aug 2012 21:24:35 +0000 - RFC1123 + numeric timezone
* Tue, 28 Aug 2012 21:24:35 UTC RFC1123
* Tue Aug 28 21:24:35 UTC 2012 - Unix date
* Tue Aug 28 21:24:35 2012 - ansi C timestamp
* Tue Aug 28 21:24:35 +0000 2012 - ruby datestamp

With More Coarse Granularities

* 2012-08-28T21:24 (This will be considered the same as 2012-08-28T21:24:00Z)
* 2012-08-28T21    (This will be considered the same as 2012-08-28T21:00:00Z)
* 2012-08-28       (This will be considered the same as 2012-08-28T00:00:00Z)
* 2012-08          (This will be considered the same as 2012-08-01T00:00:00Z)
* 2012             (This will be considered the same as 2012-01-01T00:00:00Z)
`

func (n *atDir) Lookup(name string, intr fusefs.Intr) (fusefs.Node, fuse.Error) {
	log.Printf("fs.atDir: Lookup(%q)", name)

	if name == "README.txt" {
		return staticFileNode(atReadme), nil
	}

	asOf, err := parseTime(name)
	if err != nil {
		log.Printf("Can't parse time: %v", err)
		return nil, fuse.ENOENT
	}

	return &rootsDir{fs: n.fs, at: asOf}, nil
}
