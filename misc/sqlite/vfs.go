package sqlite

import "C"
import "os"
import "sync"

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
func GoVFSOpen(filename *C.char, flags C.int) (int) {
	file, err := os.OpenFile(C.GoString(filename), int(flags), 0)
	if err != nil {
		return -1
	}

	file_map_mutex.Lock()
	defer file_map_mutex.Unlock()
	file_map[file.Fd()] = file
	return file.Fd()
}
