package sqlite

/*
#define SKIP_SQLITE_VERSION
#include "sqlite3.h"
*/
import "C"

import (
	"os"
	"sync"
)

var file_map_mutex sync.Mutex
var file_map = make(map[int]*os.File)

func GetFile(fd int) (file *os.File) {
	file_map_mutex.Lock()
	defer file_map_mutex.Unlock()
	return file_map[fd]
}

//export GoFileClose
// Returns 0 on success and 1 on error.
func GoFileClose(fd C.int) (int) {
	file := GetFile(int(fd))
	if file.Close() != nil {
		return 1
	}
	return 0
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
