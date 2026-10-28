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
	"os"
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
	{data: "\x00\x00\x00\x18ftypavif\x00\x00\x00\x00avifmif1", want: "image/avif"},
	{data: "\x00\x00\x00\x18ftypavis\x00\x00\x00\x00avismsf1", want: "image/avif"},
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
	{fileName: "heap.svg", want: "image/svg+xml"},
	{data: "<html>foo</html>", want: "text/html"},
	{data: "\xff", want: ""},
	{fileName: "park.heic", want: "image/heic"}, // truncated file for header only
	// As iOS saves edited photos, of HEVC 4:4:4.
	{data: "\x00\x00\x00\x1cftypheix\x00\x00\x00\x00tmapheixmif1" + pictMeta, want: "image/heic"},
	// A generic major brand, with HEVC only among the compatible brands.
	{data: "\x00\x00\x00\x18ftypmif1\x00\x00\x00\x00mif1heic" + pictMeta, want: "image/heic"},
	// Neither HEVC brands nor a "pict" handler.
	{data: "\x00\x00\x00\x18ftypmif1\x00\x00\x00\x00mif1miaf" + pictMeta, want: ""},
	{data: "\x00\x00\x00\x18ftypheic\x00\x00\x00\x00mif1heic" + strings.Replace(pictMeta, "pict", "vide", 1), want: ""},
}

// pictMeta is a "meta" box with just a "hdlr" box of handler type "pict",
// as in HEIF files of still images.
const pictMeta = "\x00\x00\x00\x2dmeta\x00\x00\x00\x00" +
	"\x00\x00\x00\x21hdlr\x00\x00\x00\x00\x00\x00\x00\x00pict" + "\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00"

func TestHEICBrand(t *testing.T) {
	for _, tt := range []struct{ ftyp, want string }{
		{"\x00\x00\x00\x18ftypheic\x00\x00\x00\x00mif1heic", "heic"},
		{"\x00\x00\x00\x1cftypheix\x00\x00\x00\x00tmapheixmif1", "heix"},
		{"\x00\x00\x00\x18ftypmif1\x00\x00\x00\x00mif1heix", "heix"},
		{"\x00\x00\x00\x18ftypavif\x00\x00\x00\x00avifmif1", ""},
		{"\x00\x00\x00\x14ftypmif1\x00\x00\x00\x00mif1heic", ""}, // heic is past the box
		{"\x00\x00\x00\x40ftypmif1\x00\x00\x00\x00mif1heic", ""}, // truncated box
		{"\x00\x00\x00\x18ftyp", ""},
	} {
		if got := HEICBrand([]byte(tt.ftyp)); got != tt.want {
			t.Errorf("HEICBrand(%q) = %q; want %q", tt.ftyp, got, tt.want)
		}
	}
	b := []byte("\x00\x00\x00\x18ftypmif1\x00\x00\x00\x00mif1heix")
	if n := testing.AllocsPerRun(100, func() { HEICBrand(b) }); n != 0 {
		t.Errorf("HEICBrand allocates %v times; want 0", n)
	}
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
			data, err = os.ReadFile("testdata/" + tt.fileName)
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

func TestIsVideoFilename(t *testing.T) {
	for _, tt := range tests {
		want := strings.HasPrefix(tt.want, "video/")
		got := IsVideoFileName(tt.fileName)
		if got != want {
			t.Errorf("IsVideoFileName(%q) = %t; want %t",
				tt.fileName, got, want)
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
	slurp, err := io.ReadAll(r)
	if string(slurp) != "<html>foobar" {
		t.Errorf("read = %q; want %q", slurp, content)
	}
	if !errors.Is(err, someErr) {
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
