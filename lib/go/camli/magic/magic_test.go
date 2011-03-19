/*
Copyright 2011 Google Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
nYou may obtain a copy of the License at

     http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package magic

import (
	. "camli/test/asserts"
	"io/ioutil"
	"testing"
)

type magicTest struct {
	fileName, expected string
}

var tests = []magicTest{
	{"smile.jpg", "image/jpeg"},
	{"smile.png", "image/png"},
}

func TestGolden(t *testing.T) {
	for _, test := range tests {
		data, err := ioutil.ReadFile("testdata/" + test.fileName)
		AssertNil(t, err, "no error reading " + test.fileName)
		mime := MimeType(data)
		ExpectString(t, test.expected, mime, test.fileName)
	}
}
