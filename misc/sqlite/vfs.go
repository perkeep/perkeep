package sqlite

/*
#include <string.h>

#define SKIP_SQLITE_VERSION
#include "sqlite3.h"
*/
import "C"

import (
	"os"
	"sync"
	"unsafe"
)

var file_map_mutex sync.Mutex
var file_map = make(map[int]*os.File)

func GetFile(fd int) (file *os.File) {
	file_map_mutex.Lock()
	defer file_map_mutex.Unlock()
	return file_map[fd]
}

//export GoFileClose
// Returns 0 on success and -1 on error.
func GoFileClose(fd C.int) (int) {
	file := GetFile(int(fd))
	if file.Close() != nil {
		return -1
	}
	return 0
}

//export GoFileRead
// Returns SQLite error code to be returned by xRead:
//   SQLITE_OK: read n bytes
//   SQLITE_IOERR_READ: got error while reading
//   SQLITE_IOERR_SHORT_READ: read fewer than n bytes; rest will be zeroed
func GoFileRead(fd C.int, dst *C.char, n C.int, offset C.long) (rv int) {
	println("reading", n, "bytes at offset", offset, "from fd", fd);
	defer func() {
		println("read returning", rv);
	}()

	file := GetFile(int(fd))
	if file == nil {
		return C.SQLITE_IOERR_READ
	}

	buf := make([]byte, n)
	curbuf := buf
	for n > 0 {
		read, err := file.ReadAt(curbuf, int64(offset))
		curbuf = curbuf[read:]
		n -= C.int(read)
		if err == os.EOF {
			break
		}
		if err != nil {
			return C.SQLITE_IOERR_READ
		}
	}

	C.memcpy(unsafe.Pointer(dst), unsafe.Pointer(&buf[0]), C.size_t(len(buf)))

	if n != 0 {
		return C.SQLITE_IOERR_SHORT_READ
	}
	return C.SQLITE_OK
}

//export GoFileWrite
// Returns SQLite error code to be returned by xWrite:
//   SQLITE_OK: wrote n bytes
//   SQLITE_IOERR_WRITE: got error while writing
//   SQLITE_FULL: partial write
func GoFileWrite(fd C.int, src *C.char, n C.int, offset C.long) (rv int) {
	println("writing", n, "bytes at offset", offset, "to fd", fd);
	defer func() {
		println("write returning", rv);
	}()

	file := GetFile(int(fd))
	if file == nil {
		return C.SQLITE_IOERR_WRITE
	}

	// TODO: avoid this copy
	buf := make([]byte, n)
	C.memcpy(unsafe.Pointer(&buf[0]), unsafe.Pointer(src), C.size_t(len(buf)))

	nwritten, err := file.WriteAt(buf, int64(offset))
	if err != nil {
		if err == os.ENOSPC {
			return C.SQLITE_FULL
		}
		return C.SQLITE_IOERR_WRITE
	}
	if nwritten != int(n) {
		return C.SQLITE_IOERR_WRITE
	}

	return C.SQLITE_OK
}

//export GoFileFileSize
// return[0]: 0 on success and -1 on error.
// return[1]: size
func GoFileFileSize(fd C.int) (rv int, size C.long) {
	println("getting file size for fd", fd);
	defer func() {
		println("returning", rv, "with size", size);
	}()

	file := GetFile(int(fd))
	if file == nil {
		return -1, 0
	}

	info, err := file.Stat()
	if err != nil {
		return -1, 0
	}
	return 0, C.long(info.Size)
}

//export GoVFSOpen
// fd is -1 on error.
func GoVFSOpen(filename *C.char, flags C.int) (fd int) {
	println("opening", C.GoString(filename), "with flags", int(flags))

	goflags := 0
	if flags & C.SQLITE_OPEN_READONLY != 0 {
		goflags |= os.O_RDONLY
	}
	if flags & C.SQLITE_OPEN_READWRITE != 0 {
		goflags |= os.O_RDWR
	}
	if flags & C.SQLITE_OPEN_CREATE != 0 {
		goflags |= os.O_RDWR | os.O_CREATE | os.O_TRUNC
	}
	if flags & C.SQLITE_OPEN_DELETEONCLOSE != 0 {
		// TODO: Do something.
	}
	if flags & C.SQLITE_OPEN_EXCLUSIVE != 0{
		goflags |= os.O_EXCL
	}

	file, err := os.OpenFile(C.GoString(filename), goflags, 0666)
	defer func() {
		if err != nil {
			println("got error:", err.String())
		}
		println("returning fd", fd);
	}()
	if err != nil {
		return -1
	}

	file_map_mutex.Lock()
	defer file_map_mutex.Unlock()
	file_map[file.Fd()] = file
	return file.Fd()
}

//export GoVFSAccess
func GoVFSAccess(filename *C.char, flags C.int) (rv int) {
	fi, err := os.Stat(C.GoString(filename))
	if err != nil {
		return 0
	}
	switch flags {
	case C.SQLITE_ACCESS_EXISTS:
		if fi.Size != 0 {
			return 1
		} else {
			return 0
		}
	case C.SQLITE_ACCESS_READWRITE:
		// TODO: compute read/writeability in a manner similar to access()
		return 1
	case C.SQLITE_ACCESS_READ:
		return 1
	}
	return 0
}
