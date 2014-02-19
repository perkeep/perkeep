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
	"io/ioutil"
	"testing"
)

type magicTest struct {
	fileName, data string // one of these set
	want           string
}

var tests = []magicTest{
	{fileName: "smile.jpg", want: "image/jpeg"},
	{fileName: "smile.png", want: "image/png"},
	{fileName: "smile.psd", want: "image/vnd.adobe.photoshop"},
	{fileName: "smile.tiff", want: "image/tiff"},
	{fileName: "smile.xcf", want: "image/xcf"},
	{fileName: "smile.gif", want: "image/gif"},
	{fileName: "foo.tar.gz", want: "application/gzip"},
	{fileName: "foo.tar.xz", want: "application/x-xz"},
	{fileName: "foo.tbz2", want: "application/bzip2"},
	{fileName: "foo.zip", want: "application/zip"},
	{fileName: "magic.pdf", want: "application/pdf"},
	{data: "<html>foo</html>", want: "text/html"},
	{data: "\xff", want: ""},
}

func TestMagic(t *testing.T) {
	for i, tt := range tests {
		var err error
		data := []byte(tt.data)
		if tt.fileName != "" {
			data, err = ioutil.ReadFile("testdata/" + tt.fileName)
			if err != nil {
				t.Fatalf("Error reading %s: %v", tt.fileName,
					err)
			}
		}
		mime := MIMEType(data)
		if mime != tt.want {
			t.Errorf("%d. got %q; want %q", i, mime, tt.want)
		}
	}
}
