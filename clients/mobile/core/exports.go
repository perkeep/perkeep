/*
Copyright 2026 The Perkeep Authors

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

     http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

/*
#include <stdlib.h>
*/
import "C"
import "unsafe"

// PKCall makes an in-process request to the core. The request body is
// bodyLen bytes at body (which may be NULL if bodyLen is 0). It returns
// the HTTP status code and sets *outBody and *outLen to the response
// body, which the caller must release with PKFree.
//
//export PKCall
func PKCall(method, path *C.char, body unsafe.Pointer, bodyLen C.int, outBody *unsafe.Pointer, outLen *C.int) C.int {
	var reqBody []byte
	if bodyLen > 0 {
		reqBody = C.GoBytes(body, bodyLen)
	}
	status, resp := call(C.GoString(method), C.GoString(path), reqBody)
	*outBody = C.CBytes(resp)
	*outLen = C.int(len(resp))
	return C.int(status)
}

// PKFree releases memory returned by the core.
//
//export PKFree
func PKFree(p unsafe.Pointer) {
	C.free(p)
}
