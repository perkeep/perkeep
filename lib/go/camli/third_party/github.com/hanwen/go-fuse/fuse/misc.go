// Random odds and ends.

package fuse

import (
	"bytes"
	"encoding/binary"
	"os"
	"fmt"
	"log"
	"math"
	"regexp"
	"sort"
	"syscall"
	"unsafe"
	"io/ioutil"
)

// Make a temporary directory securely.
func MakeTempDir() string {
	nm, err := ioutil.TempDir("", "go-fuse")
	if err != nil {
		panic("TempDir() failed: " + err.String())
	}
	return nm
}

// Convert os.Error back to Errno based errors.
func OsErrorToFuseError(err os.Error) Status {
	if err != nil {
		asErrno, ok := err.(os.Errno)
		if ok {
			return Status(asErrno)
		}

		asSyscallErr, ok := err.(*os.SyscallError)
		if ok {
			return Status(asSyscallErr.Errno)
		}

		asPathErr, ok := err.(*os.PathError)
		if ok {
			return OsErrorToFuseError(asPathErr.Error)
		}

		asLinkErr, ok := err.(*os.LinkError)
		if ok {
			return OsErrorToFuseError(asLinkErr.Error)
		}

		// Should not happen.  Should we log an error somewhere?
		log.Println("can't convert error type:", err)
		return ENOSYS
	}
	return OK
}

func operationName(opcode uint32) string {
	switch opcode {
	case FUSE_LOOKUP:
		return "FUSE_LOOKUP"
	case FUSE_FORGET:
		return "FUSE_FORGET"
	case FUSE_GETATTR:
		return "FUSE_GETATTR"
	case FUSE_SETATTR:
		return "FUSE_SETATTR"
	case FUSE_READLINK:
		return "FUSE_READLINK"
	case FUSE_SYMLINK:
		return "FUSE_SYMLINK"
	case FUSE_MKNOD:
		return "FUSE_MKNOD"
	case FUSE_MKDIR:
		return "FUSE_MKDIR"
	case FUSE_UNLINK:
		return "FUSE_UNLINK"
	case FUSE_RMDIR:
		return "FUSE_RMDIR"
	case FUSE_RENAME:
		return "FUSE_RENAME"
	case FUSE_LINK:
		return "FUSE_LINK"
	case FUSE_OPEN:
		return "FUSE_OPEN"
	case FUSE_READ:
		return "FUSE_READ"
	case FUSE_WRITE:
		return "FUSE_WRITE"
	case FUSE_STATFS:
		return "FUSE_STATFS"
	case FUSE_RELEASE:
		return "FUSE_RELEASE"
	case FUSE_FSYNC:
		return "FUSE_FSYNC"
	case FUSE_SETXATTR:
		return "FUSE_SETXATTR"
	case FUSE_GETXATTR:
		return "FUSE_GETXATTR"
	case FUSE_LISTXATTR:
		return "FUSE_LISTXATTR"
	case FUSE_REMOVEXATTR:
		return "FUSE_REMOVEXATTR"
	case FUSE_FLUSH:
		return "FUSE_FLUSH"
	case FUSE_INIT:
		return "FUSE_INIT"
	case FUSE_OPENDIR:
		return "FUSE_OPENDIR"
	case FUSE_READDIR:
		return "FUSE_READDIR"
	case FUSE_RELEASEDIR:
		return "FUSE_RELEASEDIR"
	case FUSE_FSYNCDIR:
		return "FUSE_FSYNCDIR"
	case FUSE_GETLK:
		return "FUSE_GETLK"
	case FUSE_SETLK:
		return "FUSE_SETLK"
	case FUSE_SETLKW:
		return "FUSE_SETLKW"
	case FUSE_ACCESS:
		return "FUSE_ACCESS"
	case FUSE_CREATE:
		return "FUSE_CREATE"
	case FUSE_INTERRUPT:
		return "FUSE_INTERRUPT"
	case FUSE_BMAP:
		return "FUSE_BMAP"
	case FUSE_DESTROY:
		return "FUSE_DESTROY"
	case FUSE_IOCTL:
		return "FUSE_IOCTL"
	case FUSE_POLL:
		return "FUSE_POLL"
	}
	return "UNKNOWN"
}

func (code Status) String() string {
	if code == OK {
		return "OK"
	}
	return fmt.Sprintf("%d=%v", int(code), os.Errno(code))
}

func newInput(opcode uint32) Empty {
	switch opcode {
	case FUSE_FORGET:
		return new(ForgetIn)
	case FUSE_GETATTR:
		return new(GetAttrIn)
	case FUSE_MKNOD:
		return new(MknodIn)
	case FUSE_MKDIR:
		return new(MkdirIn)
	case FUSE_RENAME:
		return new(RenameIn)
	case FUSE_LINK:
		return new(LinkIn)
	case FUSE_SETATTR:
		return new(SetAttrIn)
	case FUSE_OPEN:
		return new(OpenIn)
	case FUSE_CREATE:
		return new(CreateIn)
	case FUSE_FLUSH:
		return new(FlushIn)
	case FUSE_RELEASE:
		return new(ReleaseIn)
	case FUSE_READ:
		return new(ReadIn)
	case FUSE_WRITE:
		return new(WriteIn)
	case FUSE_FSYNC:
		return new(FsyncIn)
	// case FUSE_GET/SETLK(W)
	case FUSE_ACCESS:
		return new(AccessIn)
	case FUSE_INIT:
		return new(InitIn)
	case FUSE_BMAP:
		return new(BmapIn)
	case FUSE_INTERRUPT:
		return new(InterruptIn)
	case FUSE_IOCTL:
		return new(IoctlIn)
	case FUSE_POLL:
		return new(PollIn)
	case FUSE_SETXATTR:
		return new(SetXAttrIn)
	case FUSE_GETXATTR:
		return new(GetXAttrIn)
	case FUSE_OPENDIR:
		return new(OpenIn)
	case FUSE_FSYNCDIR:
		return new(FsyncIn)
	case FUSE_READDIR:
		return new(ReadIn)
	case FUSE_RELEASEDIR:
		return new(ReleaseIn)

	}
	return nil
}

func parseLittleEndian(b *bytes.Buffer, data interface{}) bool {
	err := binary.Read(b, binary.LittleEndian, data)
	if err == nil {
		return true
	}
	if err == os.EOF {
		return false
	}
	panic(fmt.Sprintf("Cannot parse %v", data))
}

func SplitNs(time float64, secs *uint64, nsecs *uint32) {
	*nsecs = uint32(1e9 * (time - math.Trunc(time)))
	*secs = uint64(math.Trunc(time))
}

func CopyFileInfo(fi *os.FileInfo, attr *Attr) {
	attr.Ino = uint64(fi.Ino)
	attr.Size = uint64(fi.Size)
	attr.Blocks = uint64(fi.Blocks)

	attr.Atime = uint64(fi.Atime_ns / 1e9)
	attr.Atimensec = uint32(fi.Atime_ns % 1e9)

	attr.Mtime = uint64(fi.Mtime_ns / 1e9)
	attr.Mtimensec = uint32(fi.Mtime_ns % 1e9)

	attr.Ctime = uint64(fi.Ctime_ns / 1e9)
	attr.Ctimensec = uint32(fi.Ctime_ns % 1e9)

	attr.Mode = fi.Mode
	attr.Nlink = uint32(fi.Nlink)
	attr.Uid = uint32(fi.Uid)
	attr.Gid = uint32(fi.Gid)
	attr.Rdev = uint32(fi.Rdev)
	attr.Blksize = uint32(fi.Blksize)
}


func writev(fd int, iovecs *syscall.Iovec, cnt int) (n int, errno int) {
	n1, _, e1 := syscall.Syscall(
		syscall.SYS_WRITEV,
		uintptr(fd), uintptr(unsafe.Pointer(iovecs)), uintptr(cnt))
	return int(n1), int(e1)
}

func Writev(fd int, packet [][]byte) (n int, err os.Error) {
	iovecs := make([]syscall.Iovec, 0, len(packet))

	for _, v := range packet {
		if v == nil || len(v) == 0 {
			continue
		}
		vec := syscall.Iovec{
			Base: &v[0],
		}
		vec.SetLen(len(v))
		iovecs = append(iovecs, vec)
	}

	if len(iovecs) == 0 {
		return 0, nil
	}

	n, errno := writev(fd, &iovecs[0], len(iovecs))
	if errno != 0 {
		err = os.NewSyscallError("writev", errno)
	}
	return n, err
}

func CountCpus() int {
	var contents [10240]byte

	f, err := os.Open("/proc/stat")
	defer f.Close()
	if err != nil {
		return 1
	}
	n, _ := f.Read(contents[:])
	re, _ := regexp.Compile("\ncpu[0-9]")

	return len(re.FindAllString(string(contents[:n]), 100))
}

// Creates a return entry for a non-existent path.
func NegativeEntry(time float64) *EntryOut {
	out := new(EntryOut)
	out.NodeId = 0
	SplitNs(time, &out.EntryValid, &out.EntryValidNsec)
	return out
}

func ModeToType(mode uint32) uint32 {
	return (mode & 0170000) >> 12
}


func CheckSuccess(e os.Error) {
	if e != nil {
		panic(fmt.Sprintf("Unexpected error: %v", e))
	}
}

// For printing latency data.
func PrintMap(m map[string]float64) {
	keys := make([]string, len(m))
	for k, _ := range m {
		keys = append(keys, k)
	}

	sort.Strings(keys)
	for _, k := range keys {
		if m[k] > 0 {
			fmt.Println(k, m[k])
		}
	}
}

func MyPID() string {
	v, _ := os.Readlink("/proc/self")
	return v
}
