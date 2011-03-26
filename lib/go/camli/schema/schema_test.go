/*
Copyright 2011 Google Inc.

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

package schema

import (
	. "camli/test/asserts"
	"json"
	"os"
	"strings"
	"testing"
)

type isUtf8Test struct {
	s string
	e bool
}

func TestIsUtf8(t *testing.T) {
	tests := []isUtf8Test{
		{"foo", true},
		{"Straße", true},
		{string([]uint8{65, 234, 234, 192, 23, 123}), false},
		{string([]uint8{65, 97}), true},
	}
	for idx, test := range tests {
		if isValidUtf8(test.s) != test.e {
			t.Errorf("expected isutf8==%d for test index %d", test.e, idx)
		}
	}
}

const kExpectedHeader = `{"camliVersion"`

func TestJson(t *testing.T) {
	fileName := "schema_test.go"
	fi, _ := os.Lstat(fileName)
	m := NewCommonFileMap(fileName, fi)
	json, err := MapToCamliJson(m)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	t.Logf("Got json: [%s]\n", json)
	// TODO: test it parses back

	if !strings.HasPrefix(json, kExpectedHeader) {
		t.Errorf("JSON does't start with expected header.")
	}
	
}

type rfc3339NanoTest struct {
	nanos int64
	e     string
}

func TestRfc3339FromNanos(t *testing.T) {
	tests := []rfc3339NanoTest{
		{0, "1970-01-01T00:00:00Z"},
		{1, "1970-01-01T00:00:00.000000001Z"},
		{10, "1970-01-01T00:00:00.00000001Z"},
		{1000, "1970-01-01T00:00:00.000001Z"},
	}
	for idx, test := range tests {
		got := rfc3339FromNanos(test.nanos)
		if got != test.e {
			t.Errorf("On test %d got %q; expected %q", idx, got, test.e)
		}
	}
}

func TestRegularFile(t *testing.T) {
	fileName := "schema_test.go"
	fi, err := os.Lstat(fileName)
        AssertNil(t, err, "test-symlink stat")
	m := NewCommonFileMap("schema_test.go", fi)
	json, err := MapToCamliJson(m)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	t.Logf("Got json for regular file: [%s]\n", json)
}

func TestSymlink(t *testing.T) {
	fileName := "testdata/test-symlink"
	fi, err := os.Lstat(fileName)
	AssertNil(t, err, "test-symlink stat")
	m := NewCommonFileMap(fileName, fi)
	json, err := MapToCamliJson(m)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	t.Logf("Got json for symlink file: [%s]\n", json)
}

type mixPartsTest struct {
	json, expected string
}

func TestStringFromMixedArray(t *testing.T) {
	tests := []mixPartsTest{
		{`["brad"]`, "brad"},
		{`["brad", 32, 70]`, "brad F"},
		{`["brad", "fitz"]`, "bradfitz"},
		{`["../foo/Am", 233, "lie.jpg"]`, "../foo/Am\xe9lie.jpg"},
	}
	for idx, test := range tests {
		var v []interface{}
		if err := json.Unmarshal([]byte(test.json), &v); err != nil {
			t.Fatalf("invalid JSON in test %d", idx)
		}
		got := stringFromMixedArray(v)
		if got != test.expected {
			t.Errorf("test %d got %q; expected %q", idx, got, test.expected)
		}
	}
}