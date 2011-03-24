package fuse

import (
	"bytes"
	"syscall"
	"fmt"
	"unsafe"
)

var _ = fmt.Print

// TODO - move this into the Go distribution.

func getxattr(path string, attr string, dest []byte) (sz int, errno int) {
	pathBs := []byte(path)
	attrBs := []byte(attr)
	size, _, errNo := syscall.Syscall6(
		syscall.SYS_GETXATTR,
		uintptr(unsafe.Pointer(&pathBs[0])),
		uintptr(unsafe.Pointer(&attrBs[0])),
		uintptr(unsafe.Pointer(&dest[0])),
		uintptr(len(dest)),
		0, 0)
	return int(size), int(errNo)
}

func GetXAttr(path string, attr string) (value []byte, errno int) {
	dest := make([]byte, 1024)
	sz, errno := getxattr(path, attr, dest)

	for sz > cap(dest) && errno == 0 {
		dest = make([]byte, sz)
		sz, errno = getxattr(path, attr, dest)
	}

	if errno != 0 {
		return nil, errno
	}

	return dest[:sz], errno
}

func listxattr(path string, dest []byte) (sz int, errno int) {
	pathbs := []byte(path)
	size, _, errNo := syscall.Syscall(
		syscall.SYS_LISTXATTR,
		uintptr(unsafe.Pointer(&pathbs[0])),
		uintptr(unsafe.Pointer(&dest[0])),
		uintptr(len(dest)))

	return int(size), int(errNo)
}

func ListXAttr(path string) (attributes [][]byte, errno int) {
	dest := make([]byte, 1024)
	sz, errno := listxattr(path, dest)
	if errno != 0 {
		return nil, errno
	}

	for sz > cap(dest) && errno == 0 {
		dest = make([]byte, sz)
		sz, errno = listxattr(path, dest)
	}

	// -1 to drop the final empty slice.
	dest = dest[:sz-1]
	attributes = bytes.Split(dest, []byte{0}, -1)
	return attributes, errno
}
