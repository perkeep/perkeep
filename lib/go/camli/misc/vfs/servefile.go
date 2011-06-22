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

// vfs is a temporary copy of src/pkg/http/fs.go until http gets ServeFileFromFS
package vfs

import (
	"fmt"
	"io"
	"os"
	"http"
	"path/filepath"
	"mime"
	"strconv"
	"strings"
	"time"
	"utf8"
)

// The FileSystem interface represents the virtual filesystem subset
// used by ServeFileFromFs to serve files.
type FileSystem interface {
	Open(name string) (File, os.Error)
}

// The File interface is the subset of *os.File methods needed
// by ServeFileFromFs and the FileSystem interface.
type File interface {
	Close() os.Error
	Stat() (*os.FileInfo, os.Error)
	Readdir(count int) ([]os.FileInfo, os.Error)
	Read([]byte) (int, os.Error)
	Seek(offset int64, whence int) (int64, os.Error)
}

type osFileSystem struct{}

func (osFileSystem) Open(name string) (File, os.Error) {
	return os.Open(name)
}

func dirList(w http.ResponseWriter, f File) {
	fmt.Fprintf(w, "<pre>\n")
	for {
		dirs, err := f.Readdir(100)
		if err != nil || len(dirs) == 0 {
			break
		}
		for _, d := range dirs {
			name := d.Name
			if d.IsDirectory() {
				name += "/"
			}
			// TODO htmlescape
			fmt.Fprintf(w, "<a href=\"%s\">%s</a>\n", name, name)
		}
	}
	fmt.Fprintf(w, "</pre>\n")
}

func serveFile(w http.ResponseWriter, r *http.Request, name string, fs FileSystem, redirect bool) {
	const indexPage = "/index.html"

	// redirect .../index.html to .../
	if strings.HasSuffix(r.URL.Path, indexPage) {
		http.Redirect(w, r, r.URL.Path[0:len(r.URL.Path)-len(indexPage)+1], http.StatusMovedPermanently)
		return
	}

	f, err := fs.Open(name)
	if err != nil {
		// TODO expose actual error?
		http.NotFound(w, r)
		return
	}
	defer f.Close()

	d, err1 := f.Stat()
	if err1 != nil {
		// TODO expose actual error?
		http.NotFound(w, r)
		return
	}

	if redirect {
		// redirect to canonical path: / at end of directory url
		// r.URL.Path always begins with /
		url := r.URL.Path
		if d.IsDirectory() {
			if url[len(url)-1] != '/' {
				http.Redirect(w, r, url+"/", http.StatusMovedPermanently)
				return
			}
		} else {
			if url[len(url)-1] == '/' {
				http.Redirect(w, r, url[0:len(url)-1], http.StatusMovedPermanently)
				return
			}
		}
	}

	if t, _ := time.Parse(http.TimeFormat, r.Header.Get("If-Modified-Since")); t != nil && d.Mtime_ns/1e9 <= t.Seconds() {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Last-Modified", time.SecondsToUTC(d.Mtime_ns/1e9).Format(http.TimeFormat))

	// use contents of index.html for directory, if present
	if d.IsDirectory() {
		index := name + filepath.FromSlash(indexPage)
		ff, err := fs.Open(index)
		if err == nil {
			defer ff.Close()
			dd, err := ff.Stat()
			if err == nil {
				name = index
				d = dd
				f = ff
			}
		}
	}

	if d.IsDirectory() {
		dirList(w, f)
		return
	}

	// serve file
	size := d.Size
	code := http.StatusOK

	// If Content-Type isn't set, use the file's extension to find it.
	if w.Header().Get("Content-Type") == "" {
		ctype := mime.TypeByExtension(filepath.Ext(name))
		if ctype == "" {
			// read a chunk to decide between utf-8 text and binary
			var buf [1024]byte
			n, _ := io.ReadFull(f, buf[:])
			b := buf[:n]
			if isText(b) {
				ctype = "text/plain; charset=utf-8"
			} else {
				// generic binary
				ctype = "application/octet-stream"
			}
			f.Seek(0, os.SEEK_SET) // rewind to output whole file
		}
		w.Header().Set("Content-Type", ctype)
	}

	// handle Content-Range header.
	// TODO(adg): handle multiple ranges
	ranges, err := parseRange(r.Header.Get("Range"), size)
	if err == nil && len(ranges) > 1 {
		err = os.NewError("multiple ranges not supported")
	}
	if err != nil {
		http.Error(w, err.String(), http.StatusRequestedRangeNotSatisfiable)
		return
	}
	if len(ranges) == 1 {
		ra := ranges[0]
		if _, err := f.Seek(ra.start, os.SEEK_SET); err != nil {
			http.Error(w, err.String(), http.StatusRequestedRangeNotSatisfiable)
			return
		}
		size = ra.length
		code = http.StatusPartialContent
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", ra.start, ra.start+ra.length-1, d.Size))
	}

	w.Header().Set("Accept-Ranges", "bytes")
	if w.Header().Get("Content-Encoding") == "" {
		w.Header().Set("Content-Length", strconv.Itoa64(size))
	}

	w.WriteHeader(code)

	if r.Method != "HEAD" {
		io.Copyn(w, f, size)
	}
}

// Heuristic: b is text if it is valid UTF-8 and doesn't
// contain any unprintable ASCII or Unicode characters.
func isText(b []byte) bool {
	for len(b) > 0 && utf8.FullRune(b) {
		rune, size := utf8.DecodeRune(b)
		if size == 1 && rune == utf8.RuneError {
			// decoding error
			return false
		}
		if 0x7F <= rune && rune <= 0x9F {
			return false
		}
		if rune < ' ' {
			switch rune {
			case '\n', '\r', '\t':
				// okay
			default:
				// binary garbage
				return false
			}
		}
		b = b[size:]
	}
	return true
}

// httpRange specifies the byte range to be sent to the client.
type httpRange struct {
	start, length int64
}

// parseRange parses a Range header string as per RFC 2616.
func parseRange(s string, size int64) ([]httpRange, os.Error) {
	if s == "" {
		return nil, nil // header not present
	}
	const b = "bytes="
	if !strings.HasPrefix(s, b) {
		return nil, os.NewError("invalid range")
	}
	var ranges []httpRange
	for _, ra := range strings.Split(s[len(b):], ",", -1) {
		i := strings.Index(ra, "-")
		if i < 0 {
			return nil, os.NewError("invalid range")
		}
		start, end := ra[:i], ra[i+1:]
		var r httpRange
		if start == "" {
			// If no start is specified, end specifies the
			// range start relative to the end of the file.
			i, err := strconv.Atoi64(end)
			if err != nil {
				return nil, os.NewError("invalid range")
			}
			if i > size {
				i = size
			}
			r.start = size - i
			r.length = size - r.start
		} else {
			i, err := strconv.Atoi64(start)
			if err != nil || i > size || i < 0 {
				return nil, os.NewError("invalid range")
			}
			r.start = i
			if end == "" {
				// If no end is specified, range extends to end of the file.
				r.length = size - r.start
			} else {
				i, err := strconv.Atoi64(end)
				if err != nil || r.start > i {
					return nil, os.NewError("invalid range")
				}
				if i >= size {
					i = size - 1
				}
				r.length = i - r.start + 1
			}
		}
		ranges = append(ranges, r)
	}
	return ranges, nil
}

// ServeFile replies to the request with the contents of the named file or directory.
func ServeFile(w http.ResponseWriter, r *http.Request, name string) {
	serveFile(w, r, name, osFileSystem{}, false)
}

// ServeFileFromFs replies to the request with the contents of the
// named file or directory, served out of the provided FileSystem
// interface.
func ServeFileFromFS(w http.ResponseWriter, r *http.Request, fs FileSystem, name string) {
	serveFile(w, r, name, fs, false)
}

