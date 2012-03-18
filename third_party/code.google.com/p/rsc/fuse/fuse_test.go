// Copyright 2012 The Go Authors.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package fuse

import (
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"runtime"
	"testing"
	"time"
)

var fuseRun = flag.String("fuserun", "", "which fuse test to run. runs all if empty.")

// umount tries its best to unmount dir.
func umount(dir string) {
	err := exec.Command("umount", dir).Run()
	if err != nil && runtime.GOOS == "linux" {
		exec.Command("/bin/fusermount", "-u", dir).Run()
	}
}

func TestFuse(t *testing.T) {
	Debugf = log.Printf
	dir, err := ioutil.TempDir("", "fusetest")
	if err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(dir, 0777)

	c, err := Mount(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer umount(dir)

	go func() {
		err := c.Serve(testFS{})
		if err != nil {
			fmt.Println("SERVE ERROR: %v\n", err)
		}
	}()

	// TODO: remove hard-coded 1 second here. try repeated from short
	// to increasingly long timeouts here, waiting for a good Stat.
	time.Sleep(1 * time.Second)
	probeEntry := *fuseRun
	if probeEntry == "" {
		probeEntry = fuseTests[0].name
	}
	_, err = os.Stat(dir + "/" + probeEntry)
	if err != nil {
		t.Fatalf("mount did not work")
		return
	}

	for _, tt := range fuseTests {
		if *fuseRun == "" || *fuseRun == tt.name {
			t.Logf("running %T", tt.node)
			tt.node.test(dir+"/"+tt.name, t)
		}
	}
}

var fuseTests = []struct {
	name string
	node interface {
		Node
		test(string, *testing.T)
	}
}{
	{"readAll", readAll{}},
	{"readAll1", &readAll1{}},
	{"write", &write{}},
	{"writeAll", &writeAll{}},
	{"writeAll2", &writeAll2{}},
	{"release", &release{}},
	{"mkdir1", &mkdir1{}},
	{"create1", &create1{}},
	{"create2", &create2{}},
	{"symlink1", &symlink1{}},
}

// TO TEST:
//	Statfs
//	Lookup(*LookupRequest, *LookupResponse)
//	Getattr(*GetattrRequest, *GetattrResponse)
//	Attr with explicit inode
//	Setattr(*SetattrRequest, *SetattrResponse)
//	Access(*AccessRequest)
//	Open(*OpenRequest, *OpenResponse)
//	Getxattr, Setxattr, Listxattr, Removexattr
//	Write(*WriteRequest, *WriteResponse)
//	Flush(*FlushRequest, *FlushResponse)

// Test Read calling ReadAll.

type readAll struct{ file }

const hi = "hello, world"

func (readAll) ReadAll(intr Intr) ([]byte, Error) {
	return []byte(hi), nil
}

func (readAll) test(path string, t *testing.T) {
	data, err := ioutil.ReadFile(path)
	if err != nil {
		t.Errorf("readAll: %v", err)
		return
	}
	if string(data) != hi {
		t.Errorf("readAll = %q, want %q", data, hi)
	}
}

// Test Read.

type readAll1 struct{ file }

func (readAll1) Read(req *ReadRequest, resp *ReadResponse, intr Intr) Error {
	HandleRead(req, resp, []byte(hi))
	return nil
}

func (readAll1) test(path string, t *testing.T) {
	readAll{}.test(path, t)
}

// Test Write calling basic Write.

type write struct {
	file
	data []byte
}

func (w *write) Write(req *WriteRequest, resp *WriteResponse, intr Intr) Error {
	w.data = append(w.data, req.Data...)
	resp.Size = len(req.Data)
	return nil
}

func (w *write) test(path string, t *testing.T) {
	log.Printf("pre-write Create")
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	log.Printf("pre-write Write")
	n, err := f.Write([]byte(hi))
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if n != len(hi) {
		t.Fatalf("short write; n=%d; hi=%d", n, len(hi))
	}
	log.Printf("pre-write Close")
	err = f.Close()
	if err != nil {
		t.Fatalf("Close: %v", err)
	}
	log.Printf("post-write Close")
	if string(w.data) != hi {
		t.Errorf("writeAll = %q, want %q", w.data, hi)
	}
}

// Test Write calling WriteAll.

type writeAll struct {
	file
	data []byte
}

func (w *writeAll) WriteAll(data []byte, intr Intr) Error {
	w.data = data
	return nil
}

func (w *writeAll) test(path string, t *testing.T) {
	err := ioutil.WriteFile(path, []byte(hi), 0666)
	if err != nil {
		t.Fatalf("WriteFile: %v", err)
		return
	}
	if string(w.data) != hi {
		t.Errorf("writeAll = %q, want %q", w.data, hi)
	}
}

// Test Write calling Setattr+Write+Flush.

type writeAll2 struct {
	file
	data    []byte
	setattr bool
	flush   bool
}

func (w *writeAll2) Setattr(req *SetattrRequest, resp *SetattrResponse, intr Intr) Error {
	w.setattr = true
	return nil
}

func (w *writeAll2) Flush(req *FlushRequest, intr Intr) Error {
	w.flush = true
	return nil
}

func (w *writeAll2) Write(req *WriteRequest, resp *WriteResponse, intr Intr) Error {
	w.data = append(w.data, req.Data...)
	resp.Size = len(req.Data)
	return nil
}

func (w *writeAll2) test(path string, t *testing.T) {
	err := ioutil.WriteFile(path, []byte(hi), 0666)
	if err != nil {
		t.Errorf("WriteFile: %v", err)
		return
	}
	if !w.setattr || string(w.data) != hi || !w.flush {
		t.Errorf("writeAll = %v, %q, %v, want %v, %q, %v", w.setattr, string(w.data), w.flush, true, hi, true)
	}
}

// Test Mkdir.

type mkdir1 struct {
	dir
	name string
}

func (f *mkdir1) Mkdir(req *MkdirRequest, intr Intr) (Node, Error) {
	f.name = req.Name
	return &mkdir1{}, nil
}

func (f *mkdir1) test(path string, t *testing.T) {
	f.name = ""
	err := os.Mkdir(path+"/foo", 0777)
	if err != nil {
		t.Error(err)
		return
	}
	if f.name != "foo" {
		t.Error(err)
		return
	}
}

// Test Create

type create1 struct {
	dir
	name string
	f    *writeAll
}

func (f *create1) Create(req *CreateRequest, resp *CreateResponse, intr Intr) (Node, Handle, Error) {
	f.name = req.Name
	f.f = &writeAll{}
	return f.f, f.f, nil
}

func (f *create1) test(path string, t *testing.T) {
	f.name = ""
	ff, err := os.Create(path + "/foo")
	if err != nil {
		t.Errorf("create1 WriteFile: %v", err)
		return
	}
	ff.Close()
	if f.name != "foo" {
		t.Errorf("create1 name=%q want foo", f.name)
	}
}

// Test Create + WriteAll + Remove

type create2 struct {
	dir
	name      string
	f         *writeAll
	fooExists bool
}

func (f *create2) Create(req *CreateRequest, resp *CreateResponse, intr Intr) (Node, Handle, Error) {
	f.name = req.Name
	f.f = &writeAll{}
	return f.f, f.f, nil
}

func (f *create2) Lookup(name string, intr Intr) (Node, Error) {
	if f.fooExists && name == "foo" {
		return file{}, nil
	}
	return nil, ENOENT
}

func (f *create2) Remove(r *RemoveRequest, intr Intr) Error {
	if f.fooExists && r.Name == "foo" && !r.Dir {
		f.fooExists = false
		return nil
	}
	return ENOENT
}

func (f *create2) test(path string, t *testing.T) {
	f.name = ""
	err := ioutil.WriteFile(path+"/foo", []byte(hi), 0666)
	if err != nil {
		t.Fatalf("create2 WriteFile: %v", err)
	}
	if string(f.f.data) != hi {
		t.Fatalf("create2 writeAll = %q, want %q", f.f.data, hi)
	}

	f.fooExists = true
	log.Printf("pre-Remove")
	err = os.Remove(path + "/foo")
	if err != nil {
		t.Fatalf("Remove: %v", err)
	}
	err = os.Remove(path + "/foo")
	if err == nil {
		t.Fatalf("second Remove = nil; want some error")
	}
}

// Test symlink + readlink

type symlink1 struct {
	dir
	newName, target string
}

func (f *symlink1) Symlink(req *SymlinkRequest, intr Intr) (Node, Error) {
	f.newName = req.NewName
	f.target = req.Target
	return symlink{target: req.Target}, nil
}

func (f *symlink1) test(path string, t *testing.T) {
	const target = "/some-target"

	err := os.Symlink(target, path+"/symlink.file")
	if err != nil {
		t.Errorf("os.Symlink: %v", err)
		return
	}

	if f.newName != "symlink.file" {
		t.Errorf("symlink newName = %q; want %q", f.newName, "symlink.file")
	}
	if f.target != target {
		t.Errorf("symlink target = %q; want %q", f.target, target)
	}

	gotName, err := os.Readlink(path + "/symlink.file")
	if err != nil {
		t.Errorf("os.Readlink: %v", err)
		return
	}
	if gotName != target {
		t.Errorf("os.Readlink = %q; want %q", gotName, target)
	}
}

// Test Release.

type release struct {
	file
	did bool
}

func (r *release) Release(*ReleaseRequest, Intr) Error {
	r.did = true
	return nil
}

func (r *release) test(path string, t *testing.T) {
	r.did = false
	f, err := os.Open(path)
	if err != nil {
		t.Error(err)
		return
	}
	f.Close()
	time.Sleep(1 * time.Second)
	if !r.did {
		t.Error("Close did not Release")
	}
}

type file struct{}
type dir struct{}
type symlink struct {
	target string
}

func (f file) Attr() Attr    { return Attr{Mode: 0666} }
func (f dir) Attr() Attr     { return Attr{Mode: os.ModeDir | 0777} }
func (f symlink) Attr() Attr { return Attr{Mode: os.ModeSymlink | 0666} }

func (f symlink) Readlink(*ReadlinkRequest, Intr) (string, Error) {
	return f.target, nil
}

type testFS struct{}

func (testFS) Root() (Node, Error) {
	return testFS{}, nil
}

func (testFS) Attr() Attr {
	return Attr{Mode: os.ModeDir | 0555}
}

func (testFS) Lookup(name string, intr Intr) (Node, Error) {
	for _, tt := range fuseTests {
		if tt.name == name {
			return tt.node, nil
		}
	}
	return nil, ENOENT
}

func (testFS) ReadDir(intr Intr) ([]Dirent, Error) {
	var dirs []Dirent
	for _, tt := range fuseTests {
		if *fuseRun == "" || *fuseRun == tt.name {
			log.Printf("Readdir; adding %q", tt.name)
			dirs = append(dirs, Dirent{Name: tt.name})
		}
	}
	return dirs, nil
}
