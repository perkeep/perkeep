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
	"bytes"
	"fmt"
	"os"
	"strconv"

	"camlistore.org/pkg/types"

	"camlistore.org/third_party/bazil.org/fuse"
	"camlistore.org/third_party/bazil.org/fuse/fs"
)

// If TrackStats is true, statistics are kept on operations.
var TrackStats bool

func init() {
	TrackStats, _ = strconv.ParseBool(os.Getenv("CAMLI_TRACK_FS_STATS"))
}

var (
	mutFileOpen      = newStat("mutfile-open")
	mutFileOpenError = newStat("mutfile-open-error")
	mutFileOpenRO    = newStat("mutfile-open-ro")
	mutFileOpenRW    = newStat("mutfile-open-rw")
	roFileOpen       = newStat("rofile-open")
	roFileOpenError  = newStat("rofile-open-error")
)

var statByName = map[string]*stat{}

func newStat(name string) *stat {
	if statByName[name] != nil {
		panic("duplicate registraton of " + name)
	}
	s := &stat{name: name}
	statByName[name] = s
	return s
}

// A stat is a wrapper around an atomic int64, as is a fuse.Node
// exporting that data as a decimal.
type stat struct {
	n    types.AtomicInt64
	name string
}

func (s *stat) Incr() {
	if TrackStats {
		s.n.Add(1)
	}
}

func (s *stat) content() []byte {
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "%d", s.n.Get())
	buf.WriteByte('\n')
	return buf.Bytes()
}

func (s *stat) Attr() fuse.Attr {
	return fuse.Attr{
		Mode:   0400,
		Uid:    uint32(os.Getuid()),
		Gid:    uint32(os.Getgid()),
		Size:   uint64(len(s.content())),
		Mtime:  serverStart,
		Ctime:  serverStart,
		Crtime: serverStart,
	}
}

func (s *stat) Open(req *fuse.OpenRequest, res *fuse.OpenResponse, intr fs.Intr) (fs.Handle, fuse.Error) {
	// Set DirectIO to keep this file from being cached in OS X's kernel.
	res.Flags |= fuse.OpenDirectIO
	return s, nil
}

func (s *stat) Read(req *fuse.ReadRequest, res *fuse.ReadResponse, intr fs.Intr) fuse.Error {
	c := s.content()
	if req.Offset > int64(len(c)) {
		return nil
	}
	c = c[req.Offset:]
	size := req.Size
	if size > len(c) {
		size = len(c)
	}
	res.Data = make([]byte, size)
	copy(res.Data, c)
	return nil
}

// A statsDir FUSE directory node is returned by root.go, by opening
// ".camli_fs_stats" in the root directory.
type statsDir struct{}

func (statsDir) Attr() fuse.Attr {
	return fuse.Attr{
		Mode: os.ModeDir | 0700,
		Uid:  uint32(os.Getuid()),
		Gid:  uint32(os.Getgid()),
	}
}

func (statsDir) ReadDir(intr fs.Intr) (ents []fuse.Dirent, err fuse.Error) {
	for k := range statByName {
		ents = append(ents, fuse.Dirent{Name: k})
	}
	return
}

func (statsDir) Lookup(req *fuse.LookupRequest, res *fuse.LookupResponse, intr fs.Intr) (fs.Node, fuse.Error) {
	name := req.Name
	s, ok := statByName[name]
	if !ok {
		return nil, fuse.ENOENT
	}
	return s, nil
}
