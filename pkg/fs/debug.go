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
	"sync/atomic"

	"bazil.org/fuse"
	"bazil.org/fuse/fs"
	"golang.org/x/net/context"
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

// TODO: https://github.com/camlistore/camlistore/issues/679

type atomicInt64 struct {
	v int64
}

func (a *atomicInt64) Get() int64 {
	return atomic.LoadInt64(&a.v)
}

func (a *atomicInt64) Set(v int64) {
	atomic.StoreInt64(&a.v, v)
}

func (a *atomicInt64) Add(delta int64) int64 {
	return atomic.AddInt64(&a.v, delta)
}

// A stat is a wrapper around an atomic int64, as is a fuse.Node
// exporting that data as a decimal.
type stat struct {
	n    atomicInt64
	name string
}

var (
	_ fs.Node         = (*stat)(nil)
	_ fs.NodeOpener   = (*stat)(nil)
	_ fs.HandleReader = (*stat)(nil)
)

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

func (s *stat) Attr(ctx context.Context, a *fuse.Attr) error {
	a.Mode = 0400
	a.Uid = uint32(os.Getuid())
	a.Gid = uint32(os.Getgid())
	a.Size = uint64(len(s.content()))
	a.Mtime = serverStart
	a.Ctime = serverStart
	a.Crtime = serverStart
	return nil
}

func (s *stat) Open(ctx context.Context, req *fuse.OpenRequest, res *fuse.OpenResponse) (fs.Handle, error) {
	// Set DirectIO to keep this file from being cached in OS X's kernel.
	res.Flags |= fuse.OpenDirectIO
	return s, nil
}

func (s *stat) Read(ctx context.Context, req *fuse.ReadRequest, res *fuse.ReadResponse) error {
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

var (
	_ fs.Node                = statsDir{}
	_ fs.NodeRequestLookuper = statsDir{}
	_ fs.HandleReadDirAller  = statsDir{}
)

func (statsDir) Attr(ctx context.Context, a *fuse.Attr) error {
	a.Mode = os.ModeDir | 0700
	a.Uid = uint32(os.Getuid())
	a.Gid = uint32(os.Getgid())
	return nil
}

func (statsDir) ReadDirAll(ctx context.Context) (ents []fuse.Dirent, err error) {
	for k := range statByName {
		ents = append(ents, fuse.Dirent{Name: k})
	}
	return
}

func (statsDir) Lookup(ctx context.Context, req *fuse.LookupRequest, res *fuse.LookupResponse) (fs.Node, error) {
	name := req.Name
	s, ok := statByName[name]
	if !ok {
		return nil, fuse.ENOENT
	}
	return s, nil
}
