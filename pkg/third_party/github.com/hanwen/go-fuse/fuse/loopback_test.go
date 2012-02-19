package fuse

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

var _ = strings.Join
var _ = log.Println

////////////////
// state for our testcase, mostly constants

const contents string = "ABC"
const mode uint32 = 0757

type testCase struct {
	origDir      string
	mountPoint   string
	mountFile    string
	mountSubdir  string
	mountSubfile string
	origFile     string
	origSubdir   string
	origSubfile  string
	tester       *testing.T
	state        *MountState
	connector    *PathFileSystemConnector
}

// Create and mount filesystem.
func (me *testCase) Setup(t *testing.T) {
	me.tester = t

	const name string = "hello.txt"
	const subdir string = "subdir"

	me.origDir = MakeTempDir()
	me.mountPoint = MakeTempDir()

	me.mountFile = filepath.Join(me.mountPoint, name)
	me.mountSubdir = filepath.Join(me.mountPoint, subdir)
	me.mountSubfile = filepath.Join(me.mountSubdir, "subfile")
	me.origFile = filepath.Join(me.origDir, name)
	me.origSubdir = filepath.Join(me.origDir, subdir)
	me.origSubfile = filepath.Join(me.origSubdir, "subfile")

	pfs := NewLoopbackFileSystem(me.origDir)
	me.connector = NewPathFileSystemConnector(pfs)
	me.connector.Debug = true
	me.state = NewMountState(me.connector)
	me.state.Mount(me.mountPoint)

	//me.state.Debug = false
	me.state.Debug = true

	fmt.Println("Orig ", me.origDir, " mount ", me.mountPoint)

	// Unthreaded, but in background.
	go me.state.Loop(false)
}

// Unmount and del.
func (me *testCase) Cleanup() {
	fmt.Println("Unmounting.")
	err := me.state.Unmount()
	CheckSuccess(err)
	os.Remove(me.mountPoint)
	os.RemoveAll(me.origDir)
}

////////////////
// Utilities.

func (me *testCase) makeOrigSubdir() {
	err := os.Mkdir(me.origSubdir, 0777)
	CheckSuccess(err)
}

func (me *testCase) removeMountSubdir() {
	err := os.RemoveAll(me.mountSubdir)
	CheckSuccess(err)
}

func (me *testCase) removeMountFile() {
	os.Remove(me.mountFile)
	// ignore errors.
}

func (me *testCase) writeOrigFile() {
	f, err := os.Create(me.origFile)
	CheckSuccess(err)
	_, err = f.Write([]byte(contents))
	CheckSuccess(err)
	f.Close()
}

////////////////
// Tests.

func (me *testCase) testOpenUnreadable() {
	_, err := os.Open(filepath.Join(me.mountPoint, "doesnotexist"))
	if err == nil {
		me.tester.Errorf("open non-existent should raise error")
	}
}

func (me *testCase) testReadThroughFuse() {
	me.writeOrigFile()

	fmt.Println("Testing chmod.")
	err := os.Chmod(me.mountFile, mode)
	CheckSuccess(err)

	fmt.Println("Testing Lstat.")
	fi, err := os.Lstat(me.mountFile)
	CheckSuccess(err)
	if (fi.Mode() & 0777) != mode {
		me.tester.Errorf("Wrong mode %o != %o", fi.Mode(), mode)
	}

	// Open (for read), read.
	fmt.Println("Testing open.")
	f, err := os.Open(me.mountFile)
	CheckSuccess(err)

	fmt.Println("Testing read.")
	var buf [1024]byte
	slice := buf[:]
	n, err := f.Read(slice)

	if len(slice[:n]) != len(contents) {
		me.tester.Errorf("Content error %v", slice)
	}
	fmt.Println("Testing close.")
	f.Close()

	me.removeMountFile()
}

func (me *testCase) testRemove() {
	me.writeOrigFile()

	fmt.Println("Testing remove.")
	err := os.Remove(me.mountFile)
	CheckSuccess(err)
	_, err = os.Lstat(me.origFile)
	if err == nil {
		me.tester.Errorf("Lstat() after delete should have generated error.")
	}
}

func (me *testCase) testWriteThroughFuse() {
	// Create (for write), write.
	me.tester.Log("Testing create.")
	f, err := os.Create(me.mountFile)
	CheckSuccess(err)

	me.tester.Log("Testing write.")
	n, err := f.WriteString(contents)
	CheckSuccess(err)
	if n != len(contents) {
		me.tester.Errorf("Write mismatch: %v of %v", n, len(contents))
	}

	fi, err := os.Lstat(me.origFile)
	if fi.Mode()&0777 != 0644 {
		me.tester.Errorf("create mode error %o", fi.Mode()&0777)
	}

	f, err = os.Open(me.origFile)
	CheckSuccess(err)
	var buf [1024]byte
	slice := buf[:]
	n, err = f.Read(slice)
	CheckSuccess(err)
	me.tester.Log("Orig contents", slice[:n])
	if string(slice[:n]) != contents {
		me.tester.Errorf("write contents error %v", slice[:n])
	}
	f.Close()
	me.removeMountFile()
}

func (me *testCase) testMkdirRmdir() {
	// Mkdir/Rmdir.
	err := os.Mkdir(me.mountSubdir, 0777)
	CheckSuccess(err)
	fi, err := os.Lstat(me.origSubdir)
	if !fi.IsDir() {
		me.tester.Errorf("Not a directory: %o", fi.Mode())
	}

	err = os.Remove(me.mountSubdir)
	CheckSuccess(err)
	CheckSuccess(err)
}

func (me *testCase) testLink() {
	me.tester.Log("Testing hard links.")
	me.writeOrigFile()
	err := os.Mkdir(me.origSubdir, 0777)
	CheckSuccess(err)

	// Link.
	err = os.Link(me.mountFile, me.mountSubfile)
	CheckSuccess(err)

	fi, err := os.Lstat(me.mountFile)
	if fi.Nlink != 2 {
		me.tester.Errorf("Expect 2 links: %v", fi)
	}

	f, err := os.Open(me.mountSubfile)

	var buf [1024]byte
	slice := buf[:]
	n, err := f.Read(slice)
	f.Close()

	strContents := string(slice[:n])
	if strContents != contents {
		me.tester.Errorf("Content error: %v", slice[:n])
	}
	me.removeMountSubdir()
	me.removeMountFile()
}

func (me *testCase) testSymlink() {
	me.tester.Log("testing symlink/readlink.")
	me.writeOrigFile()

	linkFile := "symlink-file"
	orig := "hello.txt"
	err := os.Symlink(orig, filepath.Join(me.mountPoint, linkFile))
	defer os.Remove(filepath.Join(me.mountPoint, linkFile))
	defer me.removeMountFile()

	CheckSuccess(err)

	origLink := filepath.Join(me.origDir, linkFile)
	fi, err := os.Lstat(origLink)
	CheckSuccess(err)

	if !fi.IsSymlink() {
		me.tester.Errorf("not a symlink: %o", fi.Mode())
		return
	}

	read, err := os.Readlink(filepath.Join(me.mountPoint, linkFile))
	CheckSuccess(err)

	if read != orig {
		me.tester.Errorf("unexpected symlink value '%v'", read)
	}
}

func (me *testCase) testRename() {
	me.tester.Log("Testing rename.")
	me.writeOrigFile()
	me.makeOrigSubdir()

	err := os.Rename(me.mountFile, me.mountSubfile)
	CheckSuccess(err)
	f, _ := os.Lstat(me.origFile)
	if f != nil {
		me.tester.Errorf("original %v still exists.", me.origFile)
	}
	f, _ = os.Lstat(me.origSubfile)
	if f == nil {
		me.tester.Errorf("destination %v does not exist.", me.origSubfile)
	}

	me.removeMountSubdir()
}

func (me *testCase) testAccess() {
	me.writeOrigFile()
	err := os.Chmod(me.origFile, 0)
	CheckSuccess(err)
	// Ugh - copied from unistd.h
	const W_OK uint32 = 2

	errCode := syscall.Access(me.mountFile, W_OK)
	if errCode != syscall.EACCES {
		me.tester.Errorf("Expected EACCES for non-writable, %v %v", errCode, syscall.EACCES)
	}
	err = os.Chmod(me.origFile, 0222)
	CheckSuccess(err)
	errCode = syscall.Access(me.mountFile, W_OK)
	if errCode != 0 {
		me.tester.Errorf("Expected no error code for writable. %v", errCode)
	}
	me.removeMountFile()
	me.removeMountFile()
}

func (me *testCase) testMknod() {
	me.tester.Log("Testing mknod.")
	errNo := syscall.Mknod(me.mountFile, syscall.S_IFIFO|0777, 0)
	if errNo != 0 {
		me.tester.Errorf("Mknod %v", errNo)
	}
	fi, _ := os.Lstat(me.origFile)
	if fi == nil || !fi.IsFifo() {
		me.tester.Errorf("Expected FIFO filetype.")
	}

	me.removeMountFile()
}

func (me *testCase) testReaddir() {
	me.tester.Log("Testing readdir.")
	me.writeOrigFile()
	me.makeOrigSubdir()

	dir, err := os.Open(me.mountPoint)
	CheckSuccess(err)
	infos, err := dir.Readdir(10)
	CheckSuccess(err)

	wanted := map[string]bool{
		"hello.txt": true,
		"subdir":    true,
	}
	if len(wanted) != len(infos) {
		me.tester.Errorf("Length mismatch %v", infos)
	} else {
		for _, v := range infos {
			_, ok := wanted[v.Name]
			if !ok {
				me.tester.Errorf("Unexpected name %v", v.Name)
			}
		}
	}

	dir.Close()

	me.removeMountSubdir()
	me.removeMountFile()
}

func (me *testCase) testFSync() {
	me.tester.Log("Testing fsync.")
	me.writeOrigFile()

	f, err := os.OpenFile(me.mountFile, os.O_WRONLY, 0)
	_, err = f.WriteString("hello there")
	CheckSuccess(err)

	// How to really test fsync ?
	errNo := syscall.Fsync(f.Fd())
	if errNo != 0 {
		me.tester.Errorf("fsync returned %v", errNo)
	}
	f.Close()
}

func (me *testCase) testLargeRead() {
	me.tester.Log("Testing large read.")
	name := filepath.Join(me.origDir, "large")
	f, err := os.Create(name)
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
	g, err := os.Open(filepath.Join(me.mountPoint, "large"))
	CheckSuccess(err)
	readSlice := make([]byte, len(slice))
	m, err := g.Read(readSlice)
	if m != n {
		me.tester.Errorf("read mismatch %v %v", m, n)
	}
	for i, v := range readSlice {
		if slice[i] != v {
			me.tester.Errorf("char mismatch %v %v %v", i, slice[i], v)
			break
		}
	}

	CheckSuccess(err)
	g.Close()

	// Read in chunks
	g, err = os.Open(filepath.Join(me.mountPoint, "large"))
	CheckSuccess(err)
	readSlice = make([]byte, 4096)
	total := 0
	for {
		m, err := g.Read(readSlice)
		if m == 0 && err == io.EOF {
			break
		}
		CheckSuccess(err)
		total += m
	}
	if total != len(slice) {
		me.tester.Errorf("slice error %d", total)
	}
	g.Close()

	os.Remove(name)
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

func (me *testCase) testLargeDirRead() {
	me.tester.Log("Testing large readdir.")
	created := 100

	names := make([]string, created)

	subdir := filepath.Join(me.origDir, "readdirSubdir")
	os.Mkdir(subdir, 0700)
	longname := "xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"

	nameSet := make(map[string]bool)
	for i := 0; i < created; i++ {
		// Should vary file name length.
		base := fmt.Sprintf("file%d%s", i,
			randomLengthString(len(longname)))
		name := filepath.Join(subdir, base)

		nameSet[base] = true

		f, err := os.Create(name)
		CheckSuccess(err)
		f.WriteString("bla")
		f.Close()

		names[i] = name
	}

	dir, err := os.Open(filepath.Join(me.mountPoint, "readdirSubdir"))
	CheckSuccess(err)
	// Chunked read.
	total := 0
	readSet := make(map[string]bool)
	for {
		namesRead, err := dir.Readdirnames(200)
		CheckSuccess(err)

		if len(namesRead) == 0 {
			break
		}
		for _, v := range namesRead {
			readSet[v] = true
		}
		total += len(namesRead)
	}

	if total != created {
		me.tester.Errorf("readdir mismatch got %v wanted %v", total, created)
	}
	for k, _ := range nameSet {
		_, ok := readSet[k]
		if !ok {
			me.tester.Errorf("Name %v not found in output", k)
		}
	}

	dir.Close()

	os.RemoveAll(subdir)
}

// Test driver.
func TestMount(t *testing.T) {
	ts := new(testCase)
	ts.Setup(t)

	ts.testOpenUnreadable()
	ts.testReadThroughFuse()
	ts.testRemove()
	ts.testMkdirRmdir()
	ts.testLink()
	ts.testSymlink()
	ts.testRename()
	ts.testAccess()
	ts.testMknod()
	ts.testReaddir()
	ts.testFSync()
	ts.testLargeRead()
	ts.testLargeDirRead()
	ts.Cleanup()
}

func TestRecursiveMount(t *testing.T) {
	ts := new(testCase)
	ts.Setup(t)

	f, err := os.Create(filepath.Join(ts.mountPoint, "hello.txt"))

	CheckSuccess(err)
	f.WriteString("bla")
	f.Close()

	pfs2 := NewLoopbackFileSystem(ts.origDir)
	code := ts.connector.Mount("/hello.txt", pfs2)
	if code != EINVAL {
		t.Error("expect EINVAL", code)
	}

	submnt := filepath.Join(ts.mountPoint, "mnt")
	err = os.Mkdir(submnt, 0777)
	CheckSuccess(err)
	code = ts.connector.Mount("/mnt", pfs2)
	if code != OK {
		t.Errorf("mkdir")
	}

	_, err = os.Lstat(submnt)
	CheckSuccess(err)
	_, err = os.Lstat(filepath.Join(submnt, "hello.txt"))
	CheckSuccess(err)

	f, err = os.Open(filepath.Join(submnt, "hello.txt"))
	CheckSuccess(err)
	code = ts.connector.Unmount("/mnt")
	if code != EBUSY {
		t.Error("expect EBUSY")
	}

	f.Close()

	log.Println("Waiting for kernel to flush file-close to fuse...")
	time.Sleep(1e9)

	code = ts.connector.Unmount("/mnt")
	if code != OK {
		t.Error("umount failed.", code)
	}

	ts.Cleanup()
}
