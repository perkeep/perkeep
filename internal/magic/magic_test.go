/*
Copyright 2011 The Perkeep Authors

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
	"bytes"
	"errors"
	"io"
	"io/ioutil"
	"os/exec"
	"strings"
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
	{fileName: "smile.xcf", want: "image/x-xcf"},
	{fileName: "smile.gif", want: "image/gif"},
	{fileName: "foo.tar.gz", want: "application/x-gzip"},
	{fileName: "foo.tar.xz", want: "application/x-xz"},
	{fileName: "foo.tbz2", want: "application/x-bzip2"},
	{fileName: "foo.zip", want: "application/zip"},
	{fileName: "magic.pdf", want: "application/pdf"},
	{fileName: "hello.mp4", want: "video/mp4"},
	{fileName: "hello.3gp", want: "video/3gpp"},
	{fileName: "hello.avi", want: "video/x-msvideo"},
	{fileName: "hello.mov", want: "video/quicktime"},
	{fileName: "silence.wav", want: "audio/x-wav"},
	{fileName: "silence.flac", want: "audio/x-flac"},
	{data: "<html>foo</html>", want: "text/html"},
	{data: "\xff", want: ""},
	{fileName: "park.heic", want: "image/heic"}, // truncated file for header only
}

func TestMatcherTableValid(t *testing.T) {
	for i, mte := range matchTable {
		if mte.fn != nil && (mte.offset != 0 || mte.prefix != nil) {
			t.Errorf("entry %d has both function and offset/prefix set: %+v", i, mte)
		}
	}
}

func TestMagic(t *testing.T) {
	var hasFile bool
	if _, err := exec.LookPath("file"); err == nil {
		hasFile = true
	}

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
		if !hasFile {
			continue
		}
		fmime, ok := runFileCmd(data)
		if ok && fmime != tt.want {
			t.Logf("%d. warning: got %q via file; want %q", i, fmime, tt.want)
		}
	}
}

// runFileCmd runs the file utility and returns the mime-type and true
// or an empty string and false on any error. It also mimics the MIMEType
// behaviour.
func runFileCmd(data []byte) (string, bool) {
	cmd := exec.Command("file", "--mime-type", "-")
	cmd.Stdin = bytes.NewReader(data)
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return "", false
	}
	outString := out.String()
	idx := strings.LastIndex(outString, " ")
	if idx == -1 {
		return "", false
	}
	mime := outString[idx+1 : len(outString)-1]
	if mime != "application/octet-stream" && mime != "text/plain" {
		return mime, true
	}
	return "", true
}

func TestMIMETypeFromReader(t *testing.T) {
	someErr := errors.New("some error")
	const content = "<html>foobar"
	mime, r := MIMETypeFromReader(io.MultiReader(
		strings.NewReader(content),
		&onceErrReader{someErr},
	))
	if want := "text/html"; mime != want {
		t.Errorf("mime = %q; want %q", mime, want)
	}
	slurp, err := ioutil.ReadAll(r)
	if string(slurp) != "<html>foobar" {
		t.Errorf("read = %q; want %q", slurp, content)
	}
	if err != someErr {
		t.Errorf("read error = %v; want %v", err, someErr)
	}
}

// errReader is an io.Reader which just returns err, once.
type onceErrReader struct{ err error }

func (er *onceErrReader) Read([]byte) (int, error) {
	if er.err != nil {
		err := er.err
		er.err = nil
		return 0, err
	}
	return 0, io.EOF
}
