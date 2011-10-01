// Random odds and ends.

package fuse

import (
	"flag"
	"fmt"
	"log"
	"math"
	"os"
	"reflect"
	"strings"
	"syscall"
	"unsafe"
)

func (code Status) String() string {
	if code <= 0 {
		return []string{
			"OK",
			"NOTIFY_POLL",
			"NOTIFY_INVAL_INODE",
			"NOTIFY_INVAL_ENTRY",
		}[-code]
	}
	return fmt.Sprintf("%d=%v", int(code), os.Errno(code))
}

func (code Status) Ok() bool {
	return code == OK
}

// Convert os.Error back to Errno based errors.
func OsErrorToErrno(err os.Error) Status {
	if err != nil {
		switch t := err.(type) {
		case os.Errno:
			return Status(t)
		case *os.SyscallError:
			return Status(t.Errno)
		case *os.PathError:
			return OsErrorToErrno(t.Error)
		case *os.LinkError:
			return OsErrorToErrno(t.Error)
		default:
			log.Println("can't convert error type:", err)
			return ENOSYS
		}
	}
	return OK
}

func splitNs(time float64, secs *uint64, nsecs *uint32) {
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

func ModeToType(mode uint32) uint32 {
	return (mode & 0170000) >> 12
}

func CheckSuccess(e os.Error) {
	if e != nil {
		panic(fmt.Sprintf("Unexpected error: %v", e))
	}
}

// Thanks to Andrew Gerrand for this hack.
func asSlice(ptr unsafe.Pointer, byteCount uintptr) []byte {
	h := &reflect.SliceHeader{uintptr(ptr), int(byteCount), int(byteCount)}
	return *(*[]byte)(unsafe.Pointer(h))
}

func ioctl(fd int, cmd int, arg uintptr) (int, int) {
	r0, _, e1 := syscall.Syscall(
		syscall.SYS_IOCTL, uintptr(fd), uintptr(cmd), uintptr(arg))
	val := int(r0)
	errno := int(e1)
	return val, errno
}

func Version() string {
	if version != nil {
		return *version
	}
	return "unknown"
}

func ReverseJoin(rev_components []string, sep string) string {
	components := make([]string, len(rev_components))
	for i, v := range rev_components {
		components[len(rev_components)-i-1] = v
	}
	return strings.Join(components, sep)
}

func CurrentOwner() *Owner {
	return &Owner{
		Uid: uint32(os.Getuid()),
		Gid: uint32(os.Getgid()),
	}
}

func VerboseTest() bool {
	flag := flag.Lookup("test.v")
	return flag != nil && flag.Value.String() == "true"
}
