package fuse

import (
	"bytes"
	"exec"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"rand"
	"strings"
	"syscall"
	"testing"
)

var _ = strings.Join
var _ = log.Println

////////////////
// state for our testcase, mostly constants

const contents string = "ABC"
const mode uint32 = 0757

type testCase struct {
	tmpDir string
	orig   string
	mnt    string

	mountFile   string
	mountSubdir string
	origFile    string
	origSubdir  string
	tester      *testing.T
	state       *MountState
	pathFs      *PathNodeFs
	connector   *FileSystemConnector
}

const testTtl = 0.1

// Create and mount filesystem.
func NewTestCase(t *testing.T) *testCase {
	me := &testCase{}
	me.tester = t
	paranoia = true

	// Make sure system setting does not affect test.
	syscall.Umask(0)

	const name string = "hello.txt"
	const subdir string = "subdir"

	var err os.Error
	me.tmpDir, err = ioutil.TempDir("", "go-fuse")
	CheckSuccess(err)
	me.orig = me.tmpDir + "/orig"
	me.mnt = me.tmpDir + "/mnt"

	os.Mkdir(me.orig, 0700)
	os.Mkdir(me.mnt, 0700)

	me.mountFile = filepath.Join(me.mnt, name)
	me.mountSubdir = filepath.Join(me.mnt, subdir)
	me.origFile = filepath.Join(me.orig, name)
	me.origSubdir = filepath.Join(me.orig, subdir)

	var pfs FileSystem
	pfs = NewLoopbackFileSystem(me.orig)
	pfs = NewLockingFileSystem(pfs)

	var rfs RawFileSystem
	me.pathFs = NewPathNodeFs(pfs, &PathNodeFsOptions{
		ClientInodes: true})
	me.connector = NewFileSystemConnector(me.pathFs,
		&FileSystemOptions{
			EntryTimeout:    testTtl,
			AttrTimeout:     testTtl,
			NegativeTimeout: 0.0,
		})
	rfs = me.connector
	rfs = NewLockingRawFileSystem(rfs)

	me.connector.Debug = VerboseTest()
	me.state = NewMountState(rfs)
	me.state.Mount(me.mnt, nil)

	me.state.Debug = VerboseTest()

	// Unthreaded, but in background.
	go me.state.Loop()
	return me
}

// Unmount and del.
func (me *testCase) Cleanup() {
	err := me.state.Unmount()
	CheckSuccess(err)
	os.RemoveAll(me.tmpDir)
}

func (me *testCase) rootNode() *Inode {
	return me.pathFs.Root().Inode()
}

////////////////
// Tests.

func TestOpenUnreadable(t *testing.T) {
	ts := NewTestCase(t)
	defer ts.Cleanup()
	_, err := os.Open(ts.mnt + "/doesnotexist")
	if err == nil {
		t.Errorf("open non-existent should raise error")
	}
}

func TestTouch(t *testing.T) {
	ts := NewTestCase(t)
	defer ts.Cleanup()

	err := ioutil.WriteFile(ts.origFile, []byte(contents), 0700)
	CheckSuccess(err)
	err = os.Chtimes(ts.mountFile, 42e9, 43e9)
	CheckSuccess(err)
	fi, err := os.Lstat(ts.mountFile)
	CheckSuccess(err)
	if fi.Atime_ns != 42e9 || fi.Mtime_ns != 43e9 {
		t.Errorf("Got wrong timestamps %v", fi)
	}
}

func (me *testCase) TestReadThrough(t *testing.T) {
	ts := NewTestCase(t)
	defer ts.Cleanup()

	err := ioutil.WriteFile(ts.origFile, []byte(contents), 0700)
	CheckSuccess(err)

	err = os.Chmod(ts.mountFile, mode)
	CheckSuccess(err)

	fi, err := os.Lstat(ts.mountFile)
	CheckSuccess(err)
	if (fi.Mode & 0777) != mode {
		t.Errorf("Wrong mode %o != %o", fi.Mode, mode)
	}

	// Open (for read), read.
	f, err := os.Open(ts.mountFile)
	CheckSuccess(err)
	defer f.Close()

	var buf [1024]byte
	slice := buf[:]
	n, err := f.Read(slice)

	if len(slice[:n]) != len(contents) {
		t.Errorf("Content error %v", slice)
	}
}

func TestRemove(t *testing.T) {
	me := NewTestCase(t)
	defer me.Cleanup()

	err := ioutil.WriteFile(me.origFile, []byte(contents), 0700)
	CheckSuccess(err)

	err = os.Remove(me.mountFile)
	CheckSuccess(err)
	_, err = os.Lstat(me.origFile)
	if err == nil {
		t.Errorf("Lstat() after delete should have generated error.")
	}
}

func TestWriteThrough(t *testing.T) {
	me := NewTestCase(t)
	defer me.Cleanup()

	// Create (for write), write.
	f, err := os.OpenFile(me.mountFile, os.O_WRONLY|os.O_CREATE, 0644)
	CheckSuccess(err)
	defer f.Close()

	n, err := f.WriteString(contents)
	CheckSuccess(err)
	if n != len(contents) {
		t.Errorf("Write mismatch: %v of %v", n, len(contents))
	}

	fi, err := os.Lstat(me.origFile)
	if fi.Mode&0777 != 0644 {
		t.Errorf("create mode error %o", fi.Mode&0777)
	}

	f, err = os.Open(me.origFile)
	CheckSuccess(err)
	defer f.Close()

	var buf [1024]byte
	slice := buf[:]
	n, err = f.Read(slice)
	CheckSuccess(err)
	if string(slice[:n]) != contents {
		t.Errorf("write contents error. Got: %v, expect: %v", string(slice[:n]), contents)
	}
}

func TestMkdirRmdir(t *testing.T) {
	me := NewTestCase(t)
	defer me.Cleanup()

	// Mkdir/Rmdir.
	err := os.Mkdir(me.mountSubdir, 0777)
	CheckSuccess(err)
	fi, err := os.Lstat(me.origSubdir)
	if !fi.IsDirectory() {
		t.Errorf("Not a directory: %o", fi.Mode)
	}

	err = os.Remove(me.mountSubdir)
	CheckSuccess(err)
}

func TestLinkCreate(t *testing.T) {
	me := NewTestCase(t)
	defer me.Cleanup()

	err := ioutil.WriteFile(me.origFile, []byte(contents), 0700)
	CheckSuccess(err)
	err = os.Mkdir(me.origSubdir, 0777)
	CheckSuccess(err)

	// Link.
	mountSubfile := filepath.Join(me.mountSubdir, "subfile")
	err = os.Link(me.mountFile, mountSubfile)
	CheckSuccess(err)

	subfi, err := os.Lstat(mountSubfile)
	CheckSuccess(err)
	fi, err := os.Lstat(me.mountFile)
	CheckSuccess(err)

	if fi.Nlink != 2 {
		t.Errorf("Expect 2 links: %v", fi)
	}
	if fi.Ino != subfi.Ino {
		t.Errorf("Link succeeded, but inode numbers different: %v %v", fi.Ino, subfi.Ino)
	}
	readback, err := ioutil.ReadFile(mountSubfile)
	CheckSuccess(err)

	if string(readback) != contents {
		t.Errorf("Content error: got %q want %q", string(readback), contents)
	}

	err = os.Remove(me.mountFile)
	CheckSuccess(err)

	_, err = ioutil.ReadFile(mountSubfile)
	CheckSuccess(err)
}

// Deal correctly with hard links implied by matching client inode
// numbers.
func TestLinkExisting(t *testing.T) {
	me := NewTestCase(t)
	defer me.Cleanup()

	c := "hello"

	err := ioutil.WriteFile(me.orig+"/file1", []byte(c), 0644)
	CheckSuccess(err)
	err = os.Link(me.orig+"/file1", me.orig+"/file2")
	CheckSuccess(err)

	f1, err := os.Lstat(me.mnt + "/file1")
	CheckSuccess(err)
	f2, err := os.Lstat(me.mnt + "/file2")
	CheckSuccess(err)
	if f1.Ino != f2.Ino {
		t.Errorf("linked files should have identical inodes %v %v", f1.Ino, f2.Ino)
	}

	c1, err := ioutil.ReadFile(me.mnt + "/file1")
	CheckSuccess(err)
	if string(c1) != c {
		t.Errorf("Content mismatch relative to original.")
	}
}

// Deal correctly with hard links implied by matching client inode
// numbers.
func TestLinkForget(t *testing.T) {
	me := NewTestCase(t)
	defer me.Cleanup()

	c := "hello"

	err := ioutil.WriteFile(me.orig+"/file1", []byte(c), 0644)
	CheckSuccess(err)
	err = os.Link(me.orig+"/file1", me.orig+"/file2")
	CheckSuccess(err)

	f1, err := os.Lstat(me.mnt + "/file1")
	CheckSuccess(err)

	me.pathFs.ForgetClientInodes()

	f2, err := os.Lstat(me.mnt + "/file2")
	CheckSuccess(err)
	if f1.Ino == f2.Ino {
		t.Error("After forget, we should not export links")
	}
}

func TestSymlink(t *testing.T) {
	me := NewTestCase(t)
	defer me.Cleanup()

	t.Log("testing symlink/readlink.")
	err := ioutil.WriteFile(me.origFile, []byte(contents), 0700)
	CheckSuccess(err)

	linkFile := "symlink-file"
	orig := "hello.txt"
	err = os.Symlink(orig, filepath.Join(me.mnt, linkFile))

	CheckSuccess(err)

	origLink := filepath.Join(me.orig, linkFile)
	fi, err := os.Lstat(origLink)
	CheckSuccess(err)

	if !fi.IsSymlink() {
		t.Errorf("not a symlink: %o", fi.Mode)
		return
	}

	read, err := os.Readlink(filepath.Join(me.mnt, linkFile))
	CheckSuccess(err)

	if read != orig {
		t.Errorf("unexpected symlink value '%v'", read)
	}
}

func TestRename(t *testing.T) {
	me := NewTestCase(t)
	defer me.Cleanup()

	t.Log("Testing rename.")
	err := ioutil.WriteFile(me.origFile, []byte(contents), 0700)
	CheckSuccess(err)
	sd := me.mnt + "/testRename"
	err = os.MkdirAll(sd, 0777)

	subFile := sd + "/subfile"
	err = os.Rename(me.mountFile, subFile)
	CheckSuccess(err)
	f, _ := os.Lstat(me.origFile)
	if f != nil {
		t.Errorf("original %v still exists.", me.origFile)
	}
	f, _ = os.Lstat(subFile)
	if f == nil {
		t.Errorf("destination %v does not exist.", subFile)
	}
}

// Flaky test, due to rename race condition.
func TestDelRename(t *testing.T) {
	me := NewTestCase(t)
	defer me.Cleanup()

	t.Log("Testing del+rename.")

	sd := me.mnt + "/testDelRename"
	err := os.MkdirAll(sd, 0755)
	CheckSuccess(err)

	d := sd + "/dest"
	err = ioutil.WriteFile(d, []byte("blabla"), 0644)
	CheckSuccess(err)

	f, err := os.Open(d)
	CheckSuccess(err)
	defer f.Close()

	err = os.Remove(d)
	CheckSuccess(err)

	s := sd + "/src"
	err = ioutil.WriteFile(s, []byte("blabla"), 0644)
	CheckSuccess(err)

	err = os.Rename(s, d)
	CheckSuccess(err)
}

func TestOverwriteRename(t *testing.T) {
	me := NewTestCase(t)
	defer me.Cleanup()

	t.Log("Testing rename overwrite.")

	sd := me.mnt + "/testOverwriteRename"
	err := os.MkdirAll(sd, 0755)
	CheckSuccess(err)

	d := sd + "/dest"
	err = ioutil.WriteFile(d, []byte("blabla"), 0644)
	CheckSuccess(err)

	s := sd + "/src"
	err = ioutil.WriteFile(s, []byte("blabla"), 0644)
	CheckSuccess(err)

	err = os.Rename(s, d)
	CheckSuccess(err)
}

func TestAccess(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Log("Skipping TestAccess() as root.")
		return
	}
	me := NewTestCase(t)
	defer me.Cleanup()

	err := ioutil.WriteFile(me.origFile, []byte(contents), 0700)
	CheckSuccess(err)
	err = os.Chmod(me.origFile, 0)
	CheckSuccess(err)
	// Ugh - copied from unistd.h
	const W_OK uint32 = 2

	errCode := syscall.Access(me.mountFile, W_OK)
	if errCode != syscall.EACCES {
		t.Errorf("Expected EACCES for non-writable, %v %v", errCode, syscall.EACCES)
	}
	err = os.Chmod(me.origFile, 0222)
	CheckSuccess(err)
	errCode = syscall.Access(me.mountFile, W_OK)
	if errCode != 0 {
		t.Errorf("Expected no error code for writable. %v", errCode)
	}
}

func TestMknod(t *testing.T) {
	me := NewTestCase(t)
	defer me.Cleanup()

	t.Log("Testing mknod.")
	errNo := syscall.Mknod(me.mountFile, syscall.S_IFIFO|0777, 0)
	if errNo != 0 {
		t.Errorf("Mknod %v", errNo)
	}
	fi, _ := os.Lstat(me.origFile)
	if fi == nil || !fi.IsFifo() {
		t.Errorf("Expected FIFO filetype.")
	}
}

func TestReaddir(t *testing.T) {
	me := NewTestCase(t)
	defer me.Cleanup()

	t.Log("Testing readdir.")
	err := ioutil.WriteFile(me.origFile, []byte(contents), 0700)
	CheckSuccess(err)
	err = os.Mkdir(me.origSubdir, 0777)
	CheckSuccess(err)

	dir, err := os.Open(me.mnt)
	CheckSuccess(err)
	infos, err := dir.Readdir(10)
	CheckSuccess(err)

	wanted := map[string]bool{
		"hello.txt": true,
		"subdir":    true,
	}
	if len(wanted) != len(infos) {
		t.Errorf("Length mismatch %v", infos)
	} else {
		for _, v := range infos {
			_, ok := wanted[v.Name]
			if !ok {
				t.Errorf("Unexpected name %v", v.Name)
			}
		}
	}

	dir.Close()
}

func TestFSync(t *testing.T) {
	me := NewTestCase(t)
	defer me.Cleanup()

	t.Log("Testing fsync.")
	err := ioutil.WriteFile(me.origFile, []byte(contents), 0700)
	CheckSuccess(err)

	f, err := os.OpenFile(me.mountFile, os.O_WRONLY, 0)
	_, err = f.WriteString("hello there")
	CheckSuccess(err)

	// How to really test fsync ?
	errNo := syscall.Fsync(f.Fd())
	if errNo != 0 {
		t.Errorf("fsync returned %v", errNo)
	}
	f.Close()
}

func TestLargeRead(t *testing.T) {
	me := NewTestCase(t)
	defer me.Cleanup()

	t.Log("Testing large read.")
	name := filepath.Join(me.orig, "large")
	f, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE, 0777)
	CheckSuccess(err)

	b := bytes.NewBuffer(nil)

	for i := 0; i < 20*1024; i++ {
		b.WriteString("bla")
	}
	b.WriteString("something extra to not be round")

	slice := b.Bytes()
	n, err := f.Write(slice)
	CheckSuccess(err)

	err = f.Close()
	CheckSuccess(err)

	// Read in one go.
	g, err := os.Open(filepath.Join(me.mnt, "large"))
	CheckSuccess(err)
	readSlice := make([]byte, len(slice))
	m, err := g.Read(readSlice)
	if m != n {
		t.Errorf("read mismatch %v %v", m, n)
	}
	for i, v := range readSlice {
		if slice[i] != v {
			t.Errorf("char mismatch %v %v %v", i, slice[i], v)
			break
		}
	}

	CheckSuccess(err)
	g.Close()

	// Read in chunks
	g, err = os.Open(filepath.Join(me.mnt, "large"))
	CheckSuccess(err)
	defer g.Close()
	readSlice = make([]byte, 4096)
	total := 0
	for {
		m, err := g.Read(readSlice)
		if m == 0 && err == os.EOF {
			break
		}
		CheckSuccess(err)
		total += m
	}
	if total != len(slice) {
		t.Errorf("slice error %d", total)
	}
}

func randomLengthString(length int) string {
	r := rand.Intn(length)
	j := 0

	b := make([]byte, r)
	for i := 0; i < r; i++ {
		j = (j + 1) % 10
		b[i] = byte(j) + byte('0')
	}
	return string(b)
}

func TestLargeDirRead(t *testing.T) {
	me := NewTestCase(t)
	defer me.Cleanup()

	t.Log("Testing large readdir.")
	created := 100

	names := make([]string, created)

	subdir := filepath.Join(me.orig, "readdirSubdir")
	os.Mkdir(subdir, 0700)
	longname := "xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"

	nameSet := make(map[string]bool)
	for i := 0; i < created; i++ {
		// Should vary file name length.
		base := fmt.Sprintf("file%d%s", i,
			randomLengthString(len(longname)))
		name := filepath.Join(subdir, base)

		nameSet[base] = true

		f, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE, 0777)
		CheckSuccess(err)
		f.WriteString("bla")
		f.Close()

		names[i] = name
	}

	dir, err := os.Open(filepath.Join(me.mnt, "readdirSubdir"))
	CheckSuccess(err)
	defer dir.Close()

	// Chunked read.
	total := 0
	readSet := make(map[string]bool)
	for {
		namesRead, err := dir.Readdirnames(200)
		if len(namesRead) == 0 || err == os.EOF {
			break
		}
		CheckSuccess(err)
		for _, v := range namesRead {
			readSet[v] = true
		}
		total += len(namesRead)
	}

	if total != created {
		t.Errorf("readdir mismatch got %v wanted %v", total, created)
	}
	for k, _ := range nameSet {
		_, ok := readSet[k]
		if !ok {
			t.Errorf("Name %v not found in output", k)
		}
	}
}

func TestRootDir(t *testing.T) {
	ts := NewTestCase(t)
	defer ts.Cleanup()

	d, err := os.Open(ts.mnt)
	CheckSuccess(err)
	_, err = d.Readdirnames(-1)
	CheckSuccess(err)
	err = d.Close()
	CheckSuccess(err)
}

func TestIoctl(t *testing.T) {
	ts := NewTestCase(t)
	defer ts.Cleanup()

	f, err := os.OpenFile(filepath.Join(ts.mnt, "hello.txt"),
		os.O_WRONLY|os.O_CREATE, 0777)
	defer f.Close()
	CheckSuccess(err)
	ioctl(f.Fd(), 0x5401, 42)
}

func clearStatfs(s *syscall.Statfs_t) {
	empty := syscall.Statfs_t{}
	s.Type = 0
	s.Fsid = empty.Fsid
	s.Spare = empty.Spare
}

// This test is racy. If an external process consumes space while this
// runs, we may see spurious differences between the two statfs() calls.
func TestStatFs(t *testing.T) {
	ts := NewTestCase(t)
	defer ts.Cleanup()

	empty := syscall.Statfs_t{}
	s1 := empty
	err := syscall.Statfs(ts.orig, &s1)
	if err != 0 {
		t.Fatal("statfs orig", err)
	}

	s2 := syscall.Statfs_t{}
	err = syscall.Statfs(ts.mnt, &s2)

	if err != 0 {
		t.Fatal("statfs mnt", err)
	}

	clearStatfs(&s1)
	clearStatfs(&s2)
	if fmt.Sprintf("%v", s2) != fmt.Sprintf("%v", s1) {
		t.Error("Mismatch", s1, s2)
	}
}

func TestFStatFs(t *testing.T) {
	ts := NewTestCase(t)
	defer ts.Cleanup()

	fOrig, err := os.OpenFile(ts.orig+"/file", os.O_CREATE|os.O_TRUNC|os.O_RDWR, 0644)
	CheckSuccess(err)
	defer fOrig.Close()
	
	empty := syscall.Statfs_t{}
	s1 := empty
	errno := syscall.Fstatfs(fOrig.Fd(), &s1)
	if errno != 0 {
		t.Fatal("statfs orig", err)
	}
		
	fMnt, err := os.OpenFile(ts.mnt+"/file", os.O_RDWR, 0644)
	CheckSuccess(err)
	defer fMnt.Close()
	s2 := empty

	errno = syscall.Fstatfs(fMnt.Fd(), &s2)
	if errno != 0 {
		t.Fatal("statfs mnt", err)
	}
	
	clearStatfs(&s1)
	clearStatfs(&s2)
	if fmt.Sprintf("%v", s2) != fmt.Sprintf("%v", s1) {
		t.Error("Mismatch", s1, s2)
	}
}

func TestOriginalIsSymlink(t *testing.T) {
	tmpDir, err := ioutil.TempDir("", "go-fuse")
	CheckSuccess(err)
	defer os.RemoveAll(tmpDir)
	orig := tmpDir + "/orig"
	err = os.Mkdir(orig, 0755)
	CheckSuccess(err)
	link := tmpDir + "/link"
	mnt := tmpDir + "/mnt"
	err = os.Mkdir(mnt, 0755)
	CheckSuccess(err)
	err = os.Symlink("orig", link)
	CheckSuccess(err)

	fs := NewLoopbackFileSystem(link)
	state, _, err := MountPathFileSystem(mnt, fs, nil)
	CheckSuccess(err)
	defer state.Unmount()

	go state.Loop()

	_, err = os.Lstat(mnt)
	CheckSuccess(err)
}

func TestDoubleOpen(t *testing.T) {
	ts := NewTestCase(t)
	defer ts.Cleanup()

	err := ioutil.WriteFile(ts.orig+"/file", []byte("blabla"), 0644)
	CheckSuccess(err)

	roFile, err := os.Open(ts.mnt + "/file")
	CheckSuccess(err)
	defer roFile.Close()

	rwFile, err := os.OpenFile(ts.mnt+"/file", os.O_WRONLY|os.O_TRUNC, 0666)
	CheckSuccess(err)
	defer rwFile.Close()
}

func TestUmask(t *testing.T) {
	ts := NewTestCase(t)
	defer ts.Cleanup()

	// Make sure system setting does not affect test.
	fn := ts.mnt + "/file"
	mask := 020
	cmd := exec.Command("/bin/sh", "-c",
		fmt.Sprintf("umask %o && mkdir %s", mask, fn))
	cmd.Run()

	fi, err := os.Lstat(fn)
	CheckSuccess(err)

	expect := mask ^ 0777
	got := int(fi.Mode & 0777)
	if got != expect {
		t.Errorf("got %o, expect mode %o for file %s", got, expect, fn)
	}
}
