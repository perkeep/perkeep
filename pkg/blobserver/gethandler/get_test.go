/*
Copyright 2013 Google Inc.

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

package gethandler

import (
	"bytes"
	"errors"
	"io"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"camlistore.org/pkg/blob"
)

func TestBlobFromURLPath(t *testing.T) {
	br := blobFromURLPath("/foo/bar/camli/sha1-f1d2d2f924e986ac86fdf7b36c94bcdf32beec15")
	if !br.Valid() {
		t.Fatal("nothing found")
	}
	want := blob.MustParse("sha1-f1d2d2f924e986ac86fdf7b36c94bcdf32beec15")
	if want != br {
		t.Fatalf("got = %v; want %v", br, want)
	}
}

func TestServeBlobRef_UTF8(t *testing.T) {
	testServeBlobContents(t, "foo", "text/plain; charset=utf-8")
}

func TestServeBlobRef_Binary(t *testing.T) {
	testServeBlobContents(t, "foo\xff\x00\x80", "application/octet-stream")
}

func TestServeBlobRef_Missing(t *testing.T) {
	rr := testServeBlobRef(nil, fetcher{})
	if rr.Code != 404 {
		t.Errorf("Response code = %d; want 404", rr.Code)
	}
}

func TestServeBlobRef_Error(t *testing.T) {
	rr := testServeBlobRef(nil, fetcher{size: -1})
	if rr.Code != 500 {
		t.Errorf("Response code = %d; want 500", rr.Code)
	}
}

func TestServeBlobRef_Range(t *testing.T) {
	req, _ := http.NewRequest("GET", "/path/isn't/used", nil)
	req.Header.Set("Range", "bytes=0-2")
	br := blob.MustParse("foo-000")
	rr := httptest.NewRecorder()
	rr.Body = new(bytes.Buffer)
	ServeBlobRef(rr, req, br, fetcher{strings.NewReader("foobar"), 6})
	if rr.Body.String() != "foo" {
		t.Errorf("Got %q; want foo", rr.Body)
	}
}

func TestServeBlobRef_Streams(t *testing.T) {
	var whatWasRead bytes.Buffer
	const size = 1 << 20
	testServeBlobRef(failWriter{}, fetcher{
		io.TeeReader(
			strings.NewReader(strings.Repeat("x", size)),
			&whatWasRead),
		size,
	})
	if whatWasRead.Len() == size {
		t.Errorf("handler slurped instead of streamed")
	}
}

func testServeBlobContents(t *testing.T, contents, wantType string) {
	rr := testServeBlobRef(nil, fetcher{strings.NewReader(contents), int64(len(contents))})
	if rr.Code != 200 {
		t.Errorf("Response code = %d; want 200", rr.Code)
	}
	if g, w := rr.HeaderMap.Get("Content-Type"), wantType; g != w {
		t.Errorf("Content-Type = %q; want %q", g, w)
	}
	if rr.Body.String() != contents {
		t.Errorf("Wrote %q; want %q", rr.Body.String(), contents)
	}
}

func testServeBlobRef(w io.Writer, fetcher blob.Fetcher) *httptest.ResponseRecorder {
	req, _ := http.NewRequest("GET", "/path/isn't/used", nil)
	br := blob.MustParse("foo-123")

	rr := httptest.NewRecorder()
	rr.Body = new(bytes.Buffer)
	var rw http.ResponseWriter = rr
	if w != nil {
		rw = &altWriterRecorder{io.MultiWriter(w, rr.Body), rr}
	}
	ServeBlobRef(rw, req, br, fetcher)
	return rr
}

type fetcher struct {
	r    io.Reader
	size int64
}

func (f fetcher) Fetch(br blob.Ref) (rc io.ReadCloser, size uint32, err error) {
	if f.r == nil {
		if f.size < 0 {
			return nil, 0, errors.New("some other error type")
		}
		return nil, 0, os.ErrNotExist
	}
	if rc, ok := f.r.(io.ReadCloser); ok {
		return rc, uint32(f.size), nil
	}
	return ioutil.NopCloser(f.r), uint32(f.size), nil
}

type altWriterRecorder struct {
	w io.Writer
	*httptest.ResponseRecorder
}

func (a *altWriterRecorder) Write(p []byte) (int, error) { return a.w.Write(p) }

type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { return 0, errors.New("failed to write") }
