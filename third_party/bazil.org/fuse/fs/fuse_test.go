package fs

import (
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"runtime"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

import (
	"camlistore.org/third_party/bazil.org/fuse"
	"camlistore.org/third_party/bazil.org/fuse/fuseutil"
	"camlistore.org/third_party/bazil.org/fuse/syscallx"
)

var fuseRun = flag.String("fuserun", "", "which fuse test to run. runs all if empty.")

// umount tries its best to unmount dir.
func umount(dir string) {
	err := exec.Command("umount", dir).Run()
	if err != nil && runtime.GOOS == "linux" {
		exec.Command("fusermount", "-u", dir).Run()
	}
}

func gather(ch chan []byte) []byte {
	var buf []byte
	for b := range ch {
		buf = append(buf, b...)
	}
	return buf
}

// debug adapts fuse.Debug to match t.Log calling convention; due to
// varargs, we can't just assign tb.Log to fuse.Debug
func debug(tb testing.TB) func(msg interface{}) {
	return func(msg interface{}) {
		tb.Log(msg)
	}
}

type badRootFS struct{}

func (badRootFS) Root() (Node, fuse.Error) {
	// pick a really distinct error, to identify it later
	return nil, fuse.Errno(syscall.ENAMETOOLONG)
}

func TestRootErr(t *testing.T) {
	fuse.Debug = debug(t)
	dir, err := ioutil.TempDir("", "fusetest")
	if err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(dir, 0777)

	c, err := fuse.Mount(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer umount(dir)

	ch := make(chan error, 1)
	go func() {
		ch <- Serve(c, badRootFS{})
	}()

	select {
	case err := <-ch:
		// TODO this is not be a textual comparison, Serve hides
		// details
		if err.Error() != "cannot obtain root node: file name too long" {
			t.Errorf("Unexpected error: %v", err)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("Serve did not return an error as expected, aborting")
	}
}

type testStatFS struct{}

func (f testStatFS) Root() (Node, fuse.Error) {
	return f, nil
}

func (f testStatFS) Attr() fuse.Attr {
	return fuse.Attr{Inode: 1, Mode: os.ModeDir | 0777}
}

func (f testStatFS) Statfs(req *fuse.StatfsRequest, resp *fuse.StatfsResponse, int Intr) fuse.Error {
	resp.Blocks = 42
	resp.Files = 13
	return nil
}

func TestStatfs(t *testing.T) {
	fuse.Debug = debug(t)
	dir, err := ioutil.TempDir("", "fusetest")
	if err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(dir, 0777)

	c, err := fuse.Mount(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer umount(dir)

	go func() {
		err := Serve(c, testStatFS{})
		if err != nil {
			fmt.Printf("SERVE ERROR: %v\n", err)
		}
	}()

	waitForMount_inode1(t, dir)

	{
		var st syscall.Statfs_t
		err = syscall.Statfs(dir, &st)
		if err != nil {
			t.Errorf("Statfs failed: %v", err)
		}
		t.Logf("Statfs got: %#v", st)
		if g, e := st.Blocks, uint64(42); g != e {
			t.Errorf("got Blocks = %q; want %q", g, e)
		}
		if g, e := st.Files, uint64(13); g != e {
			t.Errorf("got Files = %d; want %d", g, e)
		}
	}

	{
		var st syscall.Statfs_t
		f, err := os.Open(dir)
		if err != nil {
			t.Errorf("Open for fstatfs failed: %v", err)
		}
		defer f.Close()
		err = syscall.Fstatfs(int(f.Fd()), &st)
		if err != nil {
			t.Errorf("Fstatfs failed: %v", err)
		}
		t.Logf("Fstatfs got: %#v", st)
		if g, e := st.Blocks, uint64(42); g != e {
			t.Errorf("got Blocks = %q; want %q", g, e)
		}
		if g, e := st.Files, uint64(13); g != e {
			t.Errorf("got Files = %d; want %d", g, e)
		}
	}

}

func TestFuse(t *testing.T) {
	fuse.Debug = debug(t)
	dir, err := ioutil.TempDir("", "fusetest")
	if err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(dir, 0777)

	for _, tt := range fuseTests {
		if *fuseRun == "" || *fuseRun == tt.name {
			if st, ok := tt.node.(interface {
				setup(*testing.T)
			}); ok {
				t.Logf("setting up %T", tt.node)
				st.setup(t)
			}
		}
	}

	c, err := fuse.Mount(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer umount(dir)

	go func() {
		err := Serve(c, testFS{})
		if err != nil {
			fmt.Printf("SERVE ERROR: %v\n", err)
		}
	}()

	waitForMount(t, dir)

	for _, tt := range fuseTests {
		if *fuseRun == "" || *fuseRun == tt.name {
			t.Logf("running %T", tt.node)
			tt.node.test(dir+"/"+tt.name, t)
		}
	}
}

func waitForMount(t *testing.T, dir string) {
	// Filename to wait for in dir:
	probeEntry := *fuseRun
	if probeEntry == "" {
		probeEntry = fuseTests[0].name
	}
	for tries := 0; tries < 100; tries++ {
		_, err := os.Stat(dir + "/" + probeEntry)
		if err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("mount did not work")
}

// TODO maybe wait for fstype to change to FUse (verify it's not fuse to begin with)
func waitForMount_inode1(t *testing.T, dir string) {
	for tries := 0; tries < 100; tries++ {
		fi, err := os.Stat(dir)
		if err == nil {
			if si, ok := fi.Sys().(*syscall.Stat_t); ok {
				if si.Ino == 1 {
					return
				}
				t.Logf("waiting for root: %v", si.Ino)
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("mount did not work")
}

var fuseTests = []struct {
	name string
	node interface {
		Node
		test(string, *testing.T)
	}
}{
	{"root", &root{}},
	{"readAll", readAll{}},
	{"readAll1", &readAll1{}},
	{"release", &release{}},
	{"write", &write{}},
	{"writeTruncateFlush", &writeTruncateFlush{}},
	{"mkdir1", &mkdir1{}},
	{"create1", &create1{}},
	{"create3", &create3{}},
	{"symlink1", &symlink1{}},
	{"link1", &link1{}},
	{"rename1", &rename1{}},
	{"mknod1", &mknod1{}},
	{"dataHandle", dataHandleTest{}},
	{"interrupt", &interrupt{}},
	{"truncate42", &truncate{toSize: 42}},
	{"truncate0", &truncate{toSize: 0}},
	{"ftruncate42", &ftruncate{toSize: 42}},
	{"ftruncate0", &ftruncate{toSize: 0}},
	{"truncateWithOpen", &truncateWithOpen{}},
	{"readdir", &readdir{}},
	{"chmod", &chmod{}},
	{"open", &open{}},
	{"fsyncDir", &fsyncDir{}},
	{"getxattr", &getxattr{}},
	{"getxattrTooSmall", &getxattrTooSmall{}},
	{"getxattrSize", &getxattrSize{}},
	{"listxattr", &listxattr{}},
	{"listxattrTooSmall", &listxattrTooSmall{}},
	{"listxattrSize", &listxattrSize{}},
	{"setxattr", &setxattr{}},
	{"removexattr", &removexattr{}},
}

// TO TEST:
//	Lookup(*LookupRequest, *LookupResponse)
//	Getattr(*GetattrRequest, *GetattrResponse)
//	Attr with explicit inode
//	Setattr(*SetattrRequest, *SetattrResponse)
//	Access(*AccessRequest)
//	Open(*OpenRequest, *OpenResponse)
//	Write(*WriteRequest, *WriteResponse)
//	Flush(*FlushRequest, *FlushResponse)

// Test Stat of root.

type root struct {
	dir
}

func (f *root) test(path string, t *testing.T) {
	fi, err := os.Stat(path + "/..")
	if err != nil {
		t.Fatalf("root getattr failed with %v", err)
	}
	mode := fi.Mode()
	if (mode & os.ModeType) != os.ModeDir {
		t.Errorf("root is not a directory: %#v", fi)
	}
	if mode.Perm() != 0555 {
		t.Errorf("root has weird access mode: %v", mode.Perm())
	}
	switch stat := fi.Sys().(type) {
	case *syscall.Stat_t:
		if stat.Ino != 1 {
			t.Errorf("root has wrong inode: %v", stat.Ino)
		}
		if stat.Nlink != 1 {
			t.Errorf("root has wrong link count: %v", stat.Nlink)
		}
		if stat.Uid != 0 {
			t.Errorf("root has wrong uid: %d", stat.Uid)
		}
		if stat.Gid != 0 {
			t.Errorf("root has wrong gid: %d", stat.Gid)
		}
	}
}

// Test Read calling ReadAll.

type readAll struct{ file }

const hi = "hello, world"

func (readAll) ReadAll(intr Intr) ([]byte, fuse.Error) {
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

func (readAll1) Read(req *fuse.ReadRequest, resp *fuse.ReadResponse, intr Intr) fuse.Error {
	fuseutil.HandleRead(req, resp, []byte(hi))
	return nil
}

func (readAll1) test(path string, t *testing.T) {
	readAll{}.test(path, t)
}

// Test Write calling basic Write, with an fsync thrown in too.

type write struct {
	file
	seen struct {
		data  chan []byte
		fsync chan bool
	}
}

func (w *write) Write(req *fuse.WriteRequest, resp *fuse.WriteResponse, intr Intr) fuse.Error {
	w.seen.data <- req.Data
	resp.Size = len(req.Data)
	return nil
}

func (w *write) Fsync(r *fuse.FsyncRequest, intr Intr) fuse.Error {
	w.seen.fsync <- true
	return nil
}

func (w *write) Release(r *fuse.ReleaseRequest, intr Intr) fuse.Error {
	close(w.seen.data)
	return nil
}

func (w *write) setup(t *testing.T) {
	w.seen.data = make(chan []byte, 10)
	w.seen.fsync = make(chan bool, 1)
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

	err = syscall.Fsync(int(f.Fd()))
	if err != nil {
		t.Fatalf("Fsync = %v", err)
	}
	if !<-w.seen.fsync {
		t.Errorf("never received expected fsync call")
	}

	log.Printf("pre-write Close")
	err = f.Close()
	if err != nil {
		t.Fatalf("Close: %v", err)
	}
	log.Printf("post-write Close")
	if got := string(gather(w.seen.data)); got != hi {
		t.Errorf("write = %q, want %q", got, hi)
	}
}

// Test Write calling Setattr+Write+Flush.

type writeTruncateFlush struct {
	file
	seen struct {
		data    chan []byte
		setattr chan bool
		flush   chan bool
	}
}

func (w *writeTruncateFlush) Setattr(req *fuse.SetattrRequest, resp *fuse.SetattrResponse, intr Intr) fuse.Error {
	w.seen.setattr <- true
	return nil
}

func (w *writeTruncateFlush) Flush(req *fuse.FlushRequest, intr Intr) fuse.Error {
	w.seen.flush <- true
	return nil
}

func (w *writeTruncateFlush) Write(req *fuse.WriteRequest, resp *fuse.WriteResponse, intr Intr) fuse.Error {
	w.seen.data <- req.Data
	resp.Size = len(req.Data)
	return nil
}

func (w *writeTruncateFlush) Release(r *fuse.ReleaseRequest, intr Intr) fuse.Error {
	close(w.seen.data)
	return nil
}

func (w *writeTruncateFlush) setup(t *testing.T) {
	w.seen.data = make(chan []byte, 100)
	w.seen.setattr = make(chan bool, 1)
	w.seen.flush = make(chan bool, 1)
}

func (w *writeTruncateFlush) test(path string, t *testing.T) {
	err := ioutil.WriteFile(path, []byte(hi), 0666)
	if err != nil {
		t.Errorf("WriteFile: %v", err)
		return
	}
	if !<-w.seen.setattr {
		t.Errorf("writeTruncateFlush expected Setattr")
	}
	if !<-w.seen.flush {
		t.Errorf("writeTruncateFlush expected Setattr")
	}
	if got := string(gather(w.seen.data)); got != hi {
		t.Errorf("writeTruncateFlush = %q, want %q", got, hi)
	}
}

// Test Mkdir.

type mkdirSeen struct {
	name string
	mode os.FileMode
}

func (s mkdirSeen) String() string {
	return fmt.Sprintf("%T{name:%q mod:%v}", s, s.name, s.mode)
}

type mkdir1 struct {
	dir
	seen chan mkdirSeen
}

func (f *mkdir1) Mkdir(req *fuse.MkdirRequest, intr Intr) (Node, fuse.Error) {
	f.seen <- mkdirSeen{
		name: req.Name,
		mode: req.Mode,
	}
	return &mkdir1{}, nil
}

func (f *mkdir1) setup(t *testing.T) {
	f.seen = make(chan mkdirSeen, 1)
}

func (f *mkdir1) test(path string, t *testing.T) {
	// uniform umask needed to make os.Mkdir's mode into something
	// reproducible
	defer syscall.Umask(syscall.Umask(0022))
	err := os.Mkdir(path+"/foo", 0771)
	if err != nil {
		t.Error(err)
		return
	}
	want := mkdirSeen{name: "foo", mode: os.ModeDir | 0751}
	if g, e := <-f.seen, want; g != e {
		t.Errorf("mkdir saw %v, want %v", g, e)
		return
	}
}

// Test Create (and fsync)

type create1 struct {
	dir
	f write
}

func (f *create1) Create(req *fuse.CreateRequest, resp *fuse.CreateResponse, intr Intr) (Node, Handle, fuse.Error) {
	if req.Name != "foo" {
		log.Printf("ERROR create1.Create unexpected name: %q\n", req.Name)
		return nil, nil, fuse.EPERM
	}
	flags := req.Flags
	// OS X does not pass O_TRUNC here, Linux does; as this is a
	// Create, that's acceptable
	flags &^= fuse.OpenFlags(os.O_TRUNC)
	if g, e := flags, fuse.OpenFlags(os.O_CREATE|os.O_RDWR); g != e {
		log.Printf("ERROR create1.Create unexpected flags: %v != %v\n", g, e)
		return nil, nil, fuse.EPERM
	}
	if g, e := req.Mode, os.FileMode(0644); g != e {
		log.Printf("ERROR create1.Create unexpected mode: %v != %v\n", g, e)
		return nil, nil, fuse.EPERM
	}
	return &f.f, &f.f, nil
}

func (f *create1) setup(t *testing.T) {
	f.f.setup(t)
}

func (f *create1) test(path string, t *testing.T) {
	// uniform umask needed to make os.Create's 0666 into something
	// reproducible
	defer syscall.Umask(syscall.Umask(0022))
	ff, err := os.Create(path + "/foo")
	if err != nil {
		t.Errorf("create1 WriteFile: %v", err)
		return
	}

	err = syscall.Fsync(int(ff.Fd()))
	if err != nil {
		t.Fatalf("Fsync = %v", err)
	}

	if !<-f.f.seen.fsync {
		t.Errorf("never received expected fsync call")
	}

	ff.Close()
}

// Test Create + WriteAll + Remove

type create3 struct {
	dir
	f         write
	fooExists bool
}

func (f *create3) Create(req *fuse.CreateRequest, resp *fuse.CreateResponse, intr Intr) (Node, Handle, fuse.Error) {
	if req.Name != "foo" {
		log.Printf("ERROR create3.Create unexpected name: %q\n", req.Name)
		return nil, nil, fuse.EPERM
	}
	return &f.f, &f.f, nil
}

func (f *create3) Lookup(name string, intr Intr) (Node, fuse.Error) {
	if f.fooExists && name == "foo" {
		return file{}, nil
	}
	return nil, fuse.ENOENT
}

func (f *create3) Remove(r *fuse.RemoveRequest, intr Intr) fuse.Error {
	if f.fooExists && r.Name == "foo" && !r.Dir {
		f.fooExists = false
		return nil
	}
	return fuse.ENOENT
}

func (f *create3) setup(t *testing.T) {
	f.f.setup(t)
}

func (f *create3) test(path string, t *testing.T) {
	err := ioutil.WriteFile(path+"/foo", []byte(hi), 0666)
	if err != nil {
		t.Fatalf("create3 WriteFile: %v", err)
	}
	if got := string(gather(f.f.seen.data)); got != hi {
		t.Fatalf("create3 write = %q, want %q", got, hi)
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
	seen struct {
		req chan *fuse.SymlinkRequest
	}
}

func (f *symlink1) Symlink(req *fuse.SymlinkRequest, intr Intr) (Node, fuse.Error) {
	f.seen.req <- req
	return symlink{target: req.Target}, nil
}

func (f *symlink1) setup(t *testing.T) {
	f.seen.req = make(chan *fuse.SymlinkRequest, 1)
}

func (f *symlink1) test(path string, t *testing.T) {
	const target = "/some-target"

	err := os.Symlink(target, path+"/symlink.file")
	if err != nil {
		t.Errorf("os.Symlink: %v", err)
		return
	}

	req := <-f.seen.req

	if req.NewName != "symlink.file" {
		t.Errorf("symlink newName = %q; want %q", req.NewName, "symlink.file")
	}
	if req.Target != target {
		t.Errorf("symlink target = %q; want %q", req.Target, target)
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

// Test link

type link1 struct {
	dir
	seen struct {
		newName chan string
	}
}

func (f *link1) Lookup(name string, intr Intr) (Node, fuse.Error) {
	if name == "old" {
		return file{}, nil
	}
	return nil, fuse.ENOENT
}

func (f *link1) Link(r *fuse.LinkRequest, old Node, intr Intr) (Node, fuse.Error) {
	f.seen.newName <- r.NewName
	return file{}, nil
}

func (f *link1) setup(t *testing.T) {
	f.seen.newName = make(chan string, 1)
}

func (f *link1) test(path string, t *testing.T) {
	err := os.Link(path+"/old", path+"/new")
	if err != nil {
		t.Fatalf("Link: %v", err)
	}
	if got := <-f.seen.newName; got != "new" {
		t.Fatalf("saw Link for newName %q; want %q", got, "new")
	}
}

// Test Rename

type rename1 struct {
	dir
	renames int32
}

func (f *rename1) Lookup(name string, intr Intr) (Node, fuse.Error) {
	if name == "old" {
		return file{}, nil
	}
	return nil, fuse.ENOENT
}

func (f *rename1) Rename(r *fuse.RenameRequest, newDir Node, intr Intr) fuse.Error {
	if r.OldName == "old" && r.NewName == "new" && newDir == f {
		atomic.AddInt32(&f.renames, 1)
		return nil
	}
	return fuse.EIO
}

func (f *rename1) test(path string, t *testing.T) {
	err := os.Rename(path+"/old", path+"/new")
	if err != nil {
		t.Fatalf("Rename: %v", err)
	}
	if atomic.LoadInt32(&f.renames) != 1 {
		t.Fatalf("expected rename didn't happen")
	}
	err = os.Rename(path+"/old2", path+"/new2")
	if err == nil {
		t.Fatal("expected error on second Rename; got nil")
	}
}

// Test Release.

type release struct {
	file
	seen struct {
		did chan bool
	}
}

func (r *release) Release(*fuse.ReleaseRequest, Intr) fuse.Error {
	r.seen.did <- true
	return nil
}

func (r *release) setup(t *testing.T) {
	r.seen.did = make(chan bool, 1)
}

func (r *release) test(path string, t *testing.T) {
	f, err := os.Open(path)
	if err != nil {
		t.Error(err)
		return
	}
	f.Close()
	time.Sleep(1 * time.Second)
	if !<-r.seen.did {
		t.Error("Close did not Release")
	}
}

// Test mknod

type mknod1 struct {
	dir
	seen struct {
		gotr chan *fuse.MknodRequest
	}
}

func (f *mknod1) Mknod(r *fuse.MknodRequest, intr Intr) (Node, fuse.Error) {
	f.seen.gotr <- r
	return fifo{}, nil
}

func (f *mknod1) setup(t *testing.T) {
	f.seen.gotr = make(chan *fuse.MknodRequest, 1)
}

func (f *mknod1) test(path string, t *testing.T) {
	if os.Getuid() != 0 {
		t.Logf("skipping unless root")
		return
	}
	defer syscall.Umask(syscall.Umask(0))
	err := syscall.Mknod(path+"/node", syscall.S_IFIFO|0666, 123)
	if err != nil {
		t.Fatalf("Mknod: %v", err)
	}
	gotr := <-f.seen.gotr
	if gotr == nil {
		t.Fatalf("no recorded MknodRequest")
	}
	if g, e := gotr.Name, "node"; g != e {
		t.Errorf("got Name = %q; want %q", g, e)
	}
	if g, e := gotr.Rdev, uint32(123); g != e {
		if runtime.GOOS == "linux" {
			// Linux fuse doesn't echo back the rdev if the node
			// isn't a device (we're using a FIFO here, as that
			// bit is portable.)
		} else {
			t.Errorf("got Rdev = %v; want %v", g, e)
		}
	}
	if g, e := gotr.Mode, os.FileMode(os.ModeNamedPipe|0666); g != e {
		t.Errorf("got Mode = %v; want %v", g, e)
	}
	t.Logf("Got request: %#v", gotr)
}

type file struct{}
type dir struct{}
type fifo struct{}
type symlink struct {
	target string
}

func (f file) Attr() fuse.Attr    { return fuse.Attr{Mode: 0666} }
func (f dir) Attr() fuse.Attr     { return fuse.Attr{Mode: os.ModeDir | 0777} }
func (f fifo) Attr() fuse.Attr    { return fuse.Attr{Mode: os.ModeNamedPipe | 0666} }
func (f symlink) Attr() fuse.Attr { return fuse.Attr{Mode: os.ModeSymlink | 0666} }

func (f symlink) Readlink(*fuse.ReadlinkRequest, Intr) (string, fuse.Error) {
	return f.target, nil
}

type testFS struct{}

func (testFS) Root() (Node, fuse.Error) {
	return testFS{}, nil
}

func (testFS) Attr() fuse.Attr {
	return fuse.Attr{Inode: 1, Mode: os.ModeDir | 0555}
}

func (testFS) Lookup(name string, intr Intr) (Node, fuse.Error) {
	for _, tt := range fuseTests {
		if tt.name == name {
			return tt.node, nil
		}
	}
	return nil, fuse.ENOENT
}

func (testFS) ReadDir(intr Intr) ([]fuse.Dirent, fuse.Error) {
	var dirs []fuse.Dirent
	for _, tt := range fuseTests {
		if *fuseRun == "" || *fuseRun == tt.name {
			log.Printf("Readdir; adding %q", tt.name)
			dirs = append(dirs, fuse.Dirent{Name: tt.name})
		}
	}
	return dirs, nil
}

// Test Read served with DataHandle.

type dataHandleTest struct {
	file
}

func (dataHandleTest) Open(*fuse.OpenRequest, *fuse.OpenResponse, Intr) (Handle, fuse.Error) {
	return DataHandle([]byte(hi)), nil
}

func (dataHandleTest) test(path string, t *testing.T) {
	data, err := ioutil.ReadFile(path)
	if err != nil {
		t.Errorf("readAll: %v", err)
		return
	}
	if string(data) != hi {
		t.Errorf("readAll = %q, want %q", data, hi)
	}
}

// Test interrupt

type interrupt struct {
	file

	// closed to signal we have a read hanging
	hanging chan struct{}
}

func (it *interrupt) Read(req *fuse.ReadRequest, resp *fuse.ReadResponse, intr Intr) fuse.Error {
	if it.hanging == nil {
		fuseutil.HandleRead(req, resp, []byte("don't read this outside of the test"))
		return nil
	}

	close(it.hanging)
	<-intr
	return fuse.EINTR
}

func (it *interrupt) setup(t *testing.T) {
	it.hanging = make(chan struct{})
}

func (it *interrupt) test(path string, t *testing.T) {

	// start a subprocess that can hang until signaled
	cmd := exec.Command("cat", path)

	err := cmd.Start()
	if err != nil {
		t.Errorf("interrupt: cannot start cat: %v", err)
		return
	}

	// try to clean up if child is still alive when returning
	defer cmd.Process.Kill()

	// wait till we're sure it's hanging in read
	<-it.hanging

	err = cmd.Process.Signal(os.Interrupt)
	if err != nil {
		t.Errorf("interrupt: cannot interrupt cat: %v", err)
		return
	}

	p, err := cmd.Process.Wait()
	if err != nil {
		t.Errorf("interrupt: cat bork: %v", err)
		return
	}
	switch ws := p.Sys().(type) {
	case syscall.WaitStatus:
		if ws.CoreDump() {
			t.Errorf("interrupt: didn't expect cat to dump core: %v", ws)
		}

		if ws.Exited() {
			t.Errorf("interrupt: didn't expect cat to exit normally: %v", ws)
		}

		if !ws.Signaled() {
			t.Errorf("interrupt: expected cat to get a signal: %v", ws)
		} else {
			if ws.Signal() != os.Interrupt {
				t.Errorf("interrupt: cat got wrong signal: %v", ws)
			}
		}
	default:
		t.Logf("interrupt: this platform has no test coverage")
	}
}

// Test truncate

type truncate struct {
	toSize int64

	file
	seen struct {
		gotr chan *fuse.SetattrRequest
	}
}

// present purely to trigger bugs in WriteAll logic
func (*truncate) WriteAll(data []byte, intr Intr) fuse.Error {
	return nil
}

func (f *truncate) Setattr(req *fuse.SetattrRequest, resp *fuse.SetattrResponse, intr Intr) fuse.Error {
	f.seen.gotr <- req
	return nil
}

func (f *truncate) setup(t *testing.T) {
	f.seen.gotr = make(chan *fuse.SetattrRequest, 1)
}

func (f *truncate) test(path string, t *testing.T) {
	err := os.Truncate(path, f.toSize)
	if err != nil {
		t.Fatalf("Truncate: %v", err)
	}
	gotr := <-f.seen.gotr
	if gotr == nil {
		t.Fatalf("no recorded SetattrRequest")
	}
	if g, e := gotr.Size, uint64(f.toSize); g != e {
		t.Errorf("got Size = %q; want %q", g, e)
	}
	if g, e := gotr.Valid&^fuse.SetattrLockOwner, fuse.SetattrSize; g != e {
		t.Errorf("got Valid = %q; want %q", g, e)
	}
	t.Logf("Got request: %#v", gotr)
}

// Test ftruncate

type ftruncate struct {
	toSize int64

	file
	seen struct {
		gotr chan *fuse.SetattrRequest
	}
}

// present purely to trigger bugs in WriteAll logic
func (*ftruncate) WriteAll(data []byte, intr Intr) fuse.Error {
	return nil
}

func (f *ftruncate) Setattr(req *fuse.SetattrRequest, resp *fuse.SetattrResponse, intr Intr) fuse.Error {
	f.seen.gotr <- req
	return nil
}

func (f *ftruncate) setup(t *testing.T) {
	f.seen.gotr = make(chan *fuse.SetattrRequest, 1)
}

func (f *ftruncate) test(path string, t *testing.T) {
	{
		fil, err := os.OpenFile(path, os.O_WRONLY, 0666)
		if err != nil {
			t.Error(err)
			return
		}
		defer fil.Close()

		err = fil.Truncate(f.toSize)
		if err != nil {
			t.Fatalf("Ftruncate: %v", err)
		}
	}
	gotr := <-f.seen.gotr
	if gotr == nil {
		t.Fatalf("no recorded SetattrRequest")
	}
	if g, e := gotr.Size, uint64(f.toSize); g != e {
		t.Errorf("got Size = %q; want %q", g, e)
	}
	if g, e := gotr.Valid&^fuse.SetattrLockOwner, fuse.SetattrHandle|fuse.SetattrSize; g != e {
		t.Errorf("got Valid = %q; want %q", g, e)
	}
	t.Logf("Got request: %#v", gotr)
}

// Test opening existing file truncates

type truncateWithOpen struct {
	file
	seen struct {
		gotr chan *fuse.SetattrRequest
	}
}

// present purely to trigger bugs in WriteAll logic
func (*truncateWithOpen) WriteAll(data []byte, intr Intr) fuse.Error {
	return nil
}

func (f *truncateWithOpen) Setattr(req *fuse.SetattrRequest, resp *fuse.SetattrResponse, intr Intr) fuse.Error {
	f.seen.gotr <- req
	return nil
}

func (f *truncateWithOpen) setup(t *testing.T) {
	f.seen.gotr = make(chan *fuse.SetattrRequest, 1)
}

func (f *truncateWithOpen) test(path string, t *testing.T) {
	fil, err := os.OpenFile(path, os.O_WRONLY|os.O_TRUNC, 0666)
	if err != nil {
		t.Error(err)
		return
	}
	fil.Close()

	gotr := <-f.seen.gotr
	if gotr == nil {
		t.Fatalf("no recorded SetattrRequest")
	}
	if g, e := gotr.Size, uint64(0); g != e {
		t.Errorf("got Size = %q; want %q", g, e)
	}
	// osxfuse sets SetattrHandle here, linux does not
	if g, e := gotr.Valid&^(fuse.SetattrLockOwner|fuse.SetattrHandle), fuse.SetattrSize; g != e {
		t.Errorf("got Valid = %q; want %q", g, e)
	}
	t.Logf("Got request: %#v", gotr)
}

// Test readdir

type readdir struct {
	dir
}

func (d *readdir) ReadDir(intr Intr) ([]fuse.Dirent, fuse.Error) {
	return []fuse.Dirent{
		{Name: "one", Inode: 11, Type: fuse.DT_Dir},
		{Name: "three", Inode: 13},
		{Name: "two", Inode: 12, Type: fuse.DT_File},
	}, nil
}

func (f *readdir) test(path string, t *testing.T) {
	fil, err := os.Open(path)
	if err != nil {
		t.Error(err)
		return
	}
	defer fil.Close()

	// go Readdir is just Readdirnames + Lstat, there's no point in
	// testing that here; we have no consumption API for the real
	// dirent data
	names, err := fil.Readdirnames(100)
	if err != nil {
		t.Error(err)
		return
	}

	t.Logf("Got readdir: %q", names)

	if len(names) != 3 ||
		names[0] != "one" ||
		names[1] != "three" ||
		names[2] != "two" {
		t.Errorf(`expected 3 entries of "one", "three", "two", got: %q`, names)
		return
	}
}

// Test Chmod.

type chmodSeen struct {
	mode os.FileMode
}

type chmod struct {
	file
	seen chan chmodSeen
}

func (f *chmod) Setattr(req *fuse.SetattrRequest, resp *fuse.SetattrResponse, intr Intr) fuse.Error {
	if !req.Valid.Mode() {
		log.Printf("setattr not a chmod: %v", req.Valid)
		return fuse.EIO
	}
	f.seen <- chmodSeen{mode: req.Mode}
	return nil
}

func (f *chmod) setup(t *testing.T) {
	f.seen = make(chan chmodSeen, 1)
}

func (f *chmod) test(path string, t *testing.T) {
	err := os.Chmod(path, 0764)
	if err != nil {
		t.Errorf("chmod: %v", err)
		return
	}
	close(f.seen)
	got := <-f.seen
	if g, e := got.mode, os.FileMode(0764); g != e {
		t.Errorf("wrong mode: %v != %v", g, e)
	}
}

// Test open

type openSeen struct {
	dir   bool
	flags fuse.OpenFlags
}

func (s openSeen) String() string {
	return fmt.Sprintf("%T{dir:%v flags:%v}", s, s.dir, s.flags)
}

type open struct {
	file
	seen chan openSeen
}

func (f *open) Open(req *fuse.OpenRequest, resp *fuse.OpenResponse, intr Intr) (Handle, fuse.Error) {
	f.seen <- openSeen{dir: req.Dir, flags: req.Flags}
	// pick a really distinct error, to identify it later
	return nil, fuse.Errno(syscall.ENAMETOOLONG)

}

func (f *open) setup(t *testing.T) {
	f.seen = make(chan openSeen, 1)
}

func (f *open) test(path string, t *testing.T) {
	// node: mode only matters with O_CREATE
	fil, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if err == nil {
		t.Error("Open err == nil, expected ENAMETOOLONG")
		fil.Close()
		return
	}

	switch err2 := err.(type) {
	case *os.PathError:
		if err2.Err == syscall.ENAMETOOLONG {
			break
		}
		t.Errorf("unexpected inner error: %#v", err2)
	default:
		t.Errorf("unexpected error: %v", err)
	}

	want := openSeen{dir: false, flags: fuse.OpenFlags(os.O_WRONLY | os.O_APPEND)}
	if runtime.GOOS == "darwin" {
		// osxfuse does not let O_APPEND through at all
		//
		// https://code.google.com/p/macfuse/issues/detail?id=233
		// https://code.google.com/p/macfuse/issues/detail?id=132
		// https://code.google.com/p/macfuse/issues/detail?id=133
		want.flags &^= fuse.OpenFlags(os.O_APPEND)
	}
	if g, e := <-f.seen, want; g != e {
		t.Errorf("open saw %v, want %v", g, e)
		return
	}
}

// Test Fsync on a dir

type fsyncSeen struct {
	flags uint32
	dir   bool
}

type fsyncDir struct {
	dir
	seen chan fsyncSeen
}

func (f *fsyncDir) Fsync(r *fuse.FsyncRequest, intr Intr) fuse.Error {
	f.seen <- fsyncSeen{flags: r.Flags, dir: r.Dir}
	return nil
}

func (f *fsyncDir) setup(t *testing.T) {
	f.seen = make(chan fsyncSeen, 1)
}

func (f *fsyncDir) test(path string, t *testing.T) {
	fil, err := os.Open(path)
	if err != nil {
		t.Errorf("fsyncDir open: %v", err)
		return
	}
	defer fil.Close()
	err = fil.Sync()
	if err != nil {
		t.Errorf("fsyncDir sync: %v", err)
		return
	}

	close(f.seen)
	got := <-f.seen
	want := uint32(0)
	if runtime.GOOS == "darwin" {
		// TODO document the meaning of these flags, figure out why
		// they differ
		want = 1
	}
	if g, e := got.flags, want; g != e {
		t.Errorf("fsyncDir bad flags: %v != %v", g, e)
	}
	if g, e := got.dir, true; g != e {
		t.Errorf("fsyncDir bad dir: %v != %v", g, e)
	}
}

// Test Getxattr

type getxattrSeen struct {
	name string
}

type getxattr struct {
	file
	seen chan getxattrSeen
}

func (f *getxattr) Getxattr(req *fuse.GetxattrRequest, resp *fuse.GetxattrResponse, intr Intr) fuse.Error {
	f.seen <- getxattrSeen{name: req.Name}
	resp.Xattr = []byte("hello, world")
	return nil
}

func (f *getxattr) setup(t *testing.T) {
	f.seen = make(chan getxattrSeen, 1)
}

func (f *getxattr) test(path string, t *testing.T) {
	buf := make([]byte, 8192)
	n, err := syscallx.Getxattr(path, "not-there", buf)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
		return
	}
	buf = buf[:n]
	if g, e := string(buf), "hello, world"; g != e {
		t.Errorf("wrong getxattr content: %#v != %#v", g, e)
	}
	close(f.seen)
	seen := <-f.seen
	if g, e := seen.name, "not-there"; g != e {
		t.Errorf("wrong getxattr name: %#v != %#v", g, e)
	}
}

// Test Getxattr that has no space to return value

type getxattrTooSmall struct {
	file
}

func (f *getxattrTooSmall) Getxattr(req *fuse.GetxattrRequest, resp *fuse.GetxattrResponse, intr Intr) fuse.Error {
	resp.Xattr = []byte("hello, world")
	return nil
}

func (f *getxattrTooSmall) test(path string, t *testing.T) {
	buf := make([]byte, 3)
	_, err := syscallx.Getxattr(path, "whatever", buf)
	if err == nil {
		t.Error("Getxattr = nil; want some error")
	}
	if err != syscall.ERANGE {
		t.Errorf("unexpected error: %v", err)
		return
	}
}

// Test Getxattr used to probe result size

type getxattrSize struct {
	file
}

func (f *getxattrSize) Getxattr(req *fuse.GetxattrRequest, resp *fuse.GetxattrResponse, intr Intr) fuse.Error {
	resp.Xattr = []byte("hello, world")
	return nil
}

func (f *getxattrSize) test(path string, t *testing.T) {
	n, err := syscallx.Getxattr(path, "whatever", nil)
	if err != nil {
		t.Errorf("Getxattr unexpected error: %v", err)
		return
	}
	if g, e := n, len("hello, world"); g != e {
		t.Errorf("Getxattr incorrect size: %d != %d", g, e)
	}
}

// Test Listxattr

type listxattr struct {
	file
	seen chan bool
}

func (f *listxattr) Listxattr(req *fuse.ListxattrRequest, resp *fuse.ListxattrResponse, intr Intr) fuse.Error {
	f.seen <- true
	resp.Append("one", "two")
	return nil
}

func (f *listxattr) setup(t *testing.T) {
	f.seen = make(chan bool, 1)
}

func (f *listxattr) test(path string, t *testing.T) {
	buf := make([]byte, 8192)
	n, err := syscallx.Listxattr(path, buf)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
		return
	}
	buf = buf[:n]
	if g, e := string(buf), "one\x00two\x00"; g != e {
		t.Errorf("wrong listxattr content: %#v != %#v", g, e)
	}
	close(f.seen)
	seen := <-f.seen
	if g, e := seen, true; g != e {
		t.Errorf("listxattr not seen: %#v != %#v", g, e)
	}
}

// Test Listxattr that has no space to return value

type listxattrTooSmall struct {
	file
}

func (f *listxattrTooSmall) Listxattr(req *fuse.ListxattrRequest, resp *fuse.ListxattrResponse, intr Intr) fuse.Error {
	resp.Xattr = []byte("one\x00two\x00")
	return nil
}

func (f *listxattrTooSmall) test(path string, t *testing.T) {
	buf := make([]byte, 3)
	_, err := syscallx.Listxattr(path, buf)
	if err == nil {
		t.Error("Listxattr = nil; want some error")
	}
	if err != syscall.ERANGE {
		t.Errorf("unexpected error: %v", err)
		return
	}
}

// Test Listxattr used to probe result size

type listxattrSize struct {
	file
}

func (f *listxattrSize) Listxattr(req *fuse.ListxattrRequest, resp *fuse.ListxattrResponse, intr Intr) fuse.Error {
	resp.Xattr = []byte("one\x00two\x00")
	return nil
}

func (f *listxattrSize) test(path string, t *testing.T) {
	n, err := syscallx.Listxattr(path, nil)
	if err != nil {
		t.Errorf("Listxattr unexpected error: %v", err)
		return
	}
	if g, e := n, len("one\x00two\x00"); g != e {
		t.Errorf("Getxattr incorrect size: %d != %d", g, e)
	}
}

// Test Setxattr

type setxattrSeen struct {
	name  string
	flags uint32
	value string
}

type setxattr struct {
	file
	seen chan setxattrSeen
}

func (f *setxattr) Setxattr(req *fuse.SetxattrRequest, intr Intr) fuse.Error {
	f.seen <- setxattrSeen{
		name:  req.Name,
		flags: req.Flags,
		value: string(req.Xattr),
	}
	return nil
}

func (f *setxattr) setup(t *testing.T) {
	f.seen = make(chan setxattrSeen, 1)
}

func (f *setxattr) test(path string, t *testing.T) {
	err := syscallx.Setxattr(path, "greeting", []byte("hello, world"), 0)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
		return
	}
	close(f.seen)
	want := setxattrSeen{flags: 0, name: "greeting", value: "hello, world"}
	if g, e := <-f.seen, want; g != e {
		t.Errorf("setxattr saw %v, want %v", g, e)
	}
}

// Test Removexattr

type removexattrSeen struct {
	name string
}

type removexattr struct {
	file
	seen chan removexattrSeen
}

func (f *removexattr) Removexattr(req *fuse.RemovexattrRequest, intr Intr) fuse.Error {
	f.seen <- removexattrSeen{name: req.Name}
	return nil
}

func (f *removexattr) setup(t *testing.T) {
	f.seen = make(chan removexattrSeen, 1)
}

func (f *removexattr) test(path string, t *testing.T) {
	err := syscallx.Removexattr(path, "greeting")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
		return
	}
	close(f.seen)
	want := removexattrSeen{name: "greeting"}
	if g, e := <-f.seen, want; g != e {
		t.Errorf("removexattr saw %v, want %v", g, e)
	}
}
