// Package jsbuiltin provides minimal wrappers around some JavasScript
// built-in functions.
package jsbuiltin

import (
	"errors"

	"github.com/gopherjs/gopherjs/js"
)

// DecodeURI decodes a Uniform Resource Identifier (URI) previously created
// by EncodeURI() or by a similar routine. If the underlying JavaScript
// function throws an error, it is returned as an error.
func DecodeURI(uri string) (raw string, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = r.(*js.Error)
		}
	}()
	raw = js.Global.Call("decodeURI", uri).String()
	return
}

// EncodeURI encodes a Uniform Resource Identifier (URI) by replacing each
// instance of certain characters by one, two, three, or four escape sequences
// representing the UTF-8 encoding of the character (will only be four escape
// sequences for characters composed of two "surrogate" characters).
func EncodeURI(uri string) string {
	return js.Global.Call("encodeURI", uri).String()
}

// EncodeURIComponent encodes a Uniform Resource Identifier (URI) component
// by replacing each instance of certain characters by one, two, three, or
// four escape sequences representing the UTF-8 encoding of the character
// (will only be four escape sequences for characters composed of two
// "surrogate" characters).
func EncodeURIComponent(uri string) string {
	return js.Global.Call("encodeURIComponent", uri).String()
}

// DecodeURIComponent decodes a Uniform Resource Identifier (URI) component
// previously created by EncodeURIComponent() or by a similar routine. If the
// underlying JavaScript function throws an error, it is returned as an error.
func DecodeURIComponent(uri string) (raw string, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = r.(*js.Error)
		}
	}()
	raw = js.Global.Call("decodeURIComponent", uri).String()
	return
}

// IsFinite determines whether the passed value is a finite number, and returns
// true if it is. If needed, the parameter is first converted to a number.
func IsFinite(value interface{}) bool {
	return js.Global.Call("isFinite", value).Bool()
}

// IsNaN determines whether a value is NaN (Not-a-Number) or not. A return
// value of true indicates the input value is considered NaN by JavaScript.
func IsNaN(value interface{}) bool {
	return js.Global.Call("isNaN", value).Bool()
}

// Type constants represent the JavaScript builtin types, which may be returned
// by TypeOf().
const (
	TypeUndefined = "undefined"
	TypeNull      = "null"
	TypeObject    = "object"
	TypeBoolean   = "boolean"
	TypeNumber    = "number"
	TypeString    = "string"
	TypeFunction  = "function"
	TypeSymbol    = "symbol"
)

// TypeOf returns the JavaScript type of the passed value
func TypeOf(value interface{}) string {
	return js.Global.Get("$jsbuiltin$").Call("typeoffunc", value).String()
}

// InstanceOf returns true if value is an instance of object according to the
// built-in 'instanceof' operator. `object` must be a *js.Object representing
// a javascript constructor function.
func InstanceOf(value interface{}, object *js.Object) bool {
	return js.Global.Get("$jsbuiltin$").Call("instanceoffunc", value, object).Bool()
}

// In returns true if key is a member of obj. An error is returned if obj is not
// a JavaScript object.
func In(key string, obj *js.Object) (ok bool, err error) {
	if obj == nil || obj == js.Undefined || TypeOf(obj) != TypeObject {
		return false, errors.New("obj not a JavaScript object")
	}
	return js.Global.Get("$jsbuiltin$").Call("infunc", key, obj).Bool(), nil
}
