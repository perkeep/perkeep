// +build js

package react

import (
	"unsafe"

	"github.com/gopherjs/gopherjs/js"
)

func wrapValue(v interface{}) *js.Object {
	return js.InternalObject(v)
}

func unwrapValue(v *js.Object) interface{} {
	return (interface{})(unsafe.Pointer(v.Unsafe()))
}
