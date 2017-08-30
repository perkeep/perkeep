// +build js

package jsbuiltin

import (
	"testing"

	"github.com/gopherjs/gopherjs/js"
)

func TestEncodeURI(t *testing.T) {
	data := map[string]string{
		"http://foo.com/?msg=Привет мир.": "http://foo.com/?msg=%D0%9F%D1%80%D0%B8%D0%B2%D0%B5%D1%82%20%D0%BC%D0%B8%D1%80.",
		"http://user:host@foo.com/":       "http://user:host@foo.com/",
	}
	for url, expected := range data {
		result := EncodeURI(url)
		if result != expected {
			t.Fatalf("EncodeURI(%s) returned '%s', not '%s'", url, result, expected)
		}
	}
}

type testData struct {
	URL           string
	ExpectedURL   string
	ExpectedError string
}

func TestDecodeURI(t *testing.T) {
	data := []testData{
		testData{
			"http://foo.com/?msg=%D0%9F%D1%80%D0%B8%D0%B2%D0%B5%D1%82%20%D0%BC%D0%B8%D1%80.", "http://foo.com/?msg=Привет мир.", "",
		},
		testData{
			"http://user:host@foo.com/", "http://user:host@foo.com/", "",
		},
		testData{
			"http://foo.com/?invalidutf8=%80", "", "JavaScript error: URI malformed",
		},
	}
	for _, test := range data {
		result, err := DecodeURI(test.URL)
		if test.ExpectedError != "" {
			if err == nil {
				t.Fatalf("DecodeURI(%s) should have resulted in an error", test.URL)
			}
			if err.Error() != test.ExpectedError {
				t.Fatalf("DecodeURI(%s) should have resulted in error '%s', got '%s'", test.URL, test.ExpectedError, err)
			}
		} else {
			if err != nil {
				t.Fatalf("DecodeURI() resulted in an error: %s", err)
			}
			if result != test.ExpectedURL {
				t.Fatalf("DecodeURI(%s) returned '%s', not '%s'", test.URL, result, test.ExpectedURL)
			}
		}
	}
}

func TestEncodeURIComponentn(t *testing.T) {
	data := map[string]string{
		"Привет мир.": "%D0%9F%D1%80%D0%B8%D0%B2%D0%B5%D1%82%20%D0%BC%D0%B8%D1%80.",
		"bar": "bar",
	}
	for url, expected := range data {
		result := EncodeURIComponent(url)
		if result != expected {
			t.Fatalf("EncodeURIComponent(%s) returned '%s', not '%s'", url, result, expected)
		}
	}
}

func TestDecodeURIComponentn(t *testing.T) {
	data := []testData{
		testData{
			"%D0%9F%D1%80%D0%B8%D0%B2%D0%B5%D1%82%20%D0%BC%D0%B8%D1%80.", "Привет мир.", "",
		},
		testData{
			"bar", "bar", "",
		},
		testData{
			"%80", "", "JavaScript error: URI malformed",
		},
	}
	for _, test := range data {
		result, err := DecodeURIComponent(test.URL)
		if test.ExpectedError != "" {
			if err == nil {
				t.Fatalf("DecodeURIComponent(%s) should have resulted in an error", test.URL)
			}
			if err.Error() != test.ExpectedError {
				t.Fatalf("DecodeURIComponent(%s) should have resulted in error '%s', got '%s'", test.URL, test.ExpectedError, err)
			}
		} else {
			if err != nil {
				t.Fatalf("DecodeURIComponent() resulted in an error: %s", err)
			}
			if result != test.ExpectedURL {
				t.Fatalf("DecodeURIComponent(%s) returned '%s', not '%s'", test.URL, result, test.ExpectedURL)
			}
		}
	}
}

func TestIsFinite(t *testing.T) {
	data := map[interface{}]bool{
		123:          true,
		-1.23:        true,
		5 - 2:        true,
		0:            true,
		"123":        true,
		"Hello":      false,
		"2005/12/12": false,
	}
	for value, expected := range data {
		result := IsFinite(value)
		if result != expected {
			t.Fatalf("IsFinite(%s) returned %t, not %t", value, result, expected)
		}
	}
}

func TestIsNaN(t *testing.T) {
	data := map[interface{}]bool{
		123:          false,
		-1.23:        false,
		5 - 2:        false,
		0:            false,
		"123":        false,
		"Hello":      true,
		"2005/12/12": true,
	}
	for value, expected := range data {
		result := IsNaN(value)
		if result != expected {
			t.Fatalf("IsNaN(%s) returned %t, not %t", value, result, expected)
		}
	}
}

type toTest struct {
	value  interface{}
	result string
}

func TestTypeOf(t *testing.T) {
	data := []toTest{
		// Standard JS types
		toTest{js.Undefined, "undefined"},
		toTest{nil, "object"},
		toTest{true, "boolean"},
		toTest{false, "boolean"},
		toTest{12345, "number"},
		toTest{"one two three", "string"},
		toTest{js.Global.Call, "function"},
		toTest{js.Global, "object"},
	}
	// Check whether the JS interpretor supports the 'symbol' type (Node >= 0.12)
	if TypeOf(js.Global.Get("Symbol")) == "function" {
		symbol := js.Global.Call("Symbol", "foo")
		data = append(data, toTest{&symbol, "symbol"})
	}
	for _, test := range data {
		result := TypeOf(test.value)
		if result != test.result {
			t.Fatalf("Typeof(%s) returned %s, not %s", test.value, result, test.result)
		}
	}

	if to := TypeOf(map[string]string{}); to != "object" {
		t.Fatalf("Obscure type not recognized as object")
	}
	if to := TypeOf(js.Object{}); to != "object" {
		t.Fatal("Invalid/empty JS object not recognized as object")
	}
}

type ioTest struct {
	value  interface{}
	object *js.Object
	result bool
}

func TestInstanceOf(t *testing.T) {
	data := []ioTest{
		// Standard JS types
		ioTest{js.Undefined, js.Global.Get("Object"), false},
		ioTest{"a string", js.Global.Get("String"), false},
		ioTest{js.Global.Get("String").New("foo"), js.Global.Get("String"), true},
		ioTest{js.Global.Get("String").New("foo"), js.Global.Get("Object"), true},
	}
	for _, test := range data {
		result := InstanceOf(test.value, test.object)
		if result != test.result {
			t.Errorf("InstanceOf(%s,%s) returned %t, not %t", test.value, test.object, result, test.result)
		}
	}
}

type inTest struct {
	obj    *js.Object
	key    string
	result bool
	err    string
}

func TestIn(t *testing.T) {
	obj := js.Global.Get("Object").New()
	obj.Set("foo", "bar")
	jsString := js.Global.Call("eval", `'"test string"'`)
	data := []inTest{
		{obj: obj, key: "foo", result: true},
		{obj: obj, key: "bar", result: false},
		{obj: js.Undefined, key: "foo", err: "obj not a JavaScript function"},
		{obj: nil, key: "foo", err: "obj not a JavaScript function"},
		{obj: jsString, key: "foo", err: "obj not a JavaScript function"},
	}
	for _, test := range data {
		result, err := In(test.key, test.obj)
		var msg string
		if err != nil {
			msg = err.Error()
		}
		if msg != test.err {
			t.Errorf("Unexpected error: %s", msg)
		}
		if result != test.result {
			t.Errorf("In(%v, %s) returned %t, not %t", test.obj, test.key, result, test.result)
		}
	}
}
