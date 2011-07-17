package sqlite

import "C"

//export GoVFSOpen
// fd is -1 on error.
func GoVFSOpen(filename *C.char, flags C.int) (fd int, isReadOnly bool) {
	return -1, false
}

//export GoStart
func GoStart(i, xdim, ydim, xstart, xend, ystart, yend C.int, a *C.int, n *C.int) (int, int) {
	return 1, 2
}
