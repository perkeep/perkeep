package ctest

/*
#include <stdlib.h>

#include "cstuff.h"
*/
import "C"

import "unsafe"

//export GoFoo
func GoFoo(c *C.char) {
	gstr := C.GoString(c)
	println("I AM GO", gstr)
}

func CallC(s string) {
	cstr := C.CString(s)
	defer C.free(unsafe.Pointer(cstr))
	C.CFoo(cstr)
}
