package sqlite

import "C"

//export GoVFSOpen
// fd is -1 on error.
func GoVFSOpen(filename *C.char, flags C.int) (int, int) {
	return -1, 0
}
