// +build !js

package react

import (
	"github.com/gopherjs/gopherjs/js"
)

func wrapValue(v interface{}) *js.Object {
	return nil
}

func unwrapValue(v *js.Object) interface{} {
	return nil
}
