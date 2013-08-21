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

// Package gethandler implements the HTTP handler for fetching blobs.
package gethandler

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/httputil"
)

var kGetPattern = regexp.MustCompile(`/camli/` + blob.Pattern + `$`)

// Handler is the HTTP handler for serving GET requests of blobs.
type Handler struct {
	Fetcher blob.StreamingFetcher
}

// CreateGetHandler returns an http Handler for serving blobs from fetcher.
func CreateGetHandler(fetcher blob.StreamingFetcher) http.Handler {
	return &Handler{Fetcher: fetcher}
}

func (h *Handler) ServeHTTP(conn http.ResponseWriter, req *http.Request) {
	if req.URL.Path == "/camli/sha1-deadbeef00000000000000000000000000000000" {
		// Test handler.
		simulatePrematurelyClosedConnection(conn, req)
		return
	}

	blobRef := blobFromURLPath(req.URL.Path)
	if !blobRef.Valid() {
		http.Error(conn, "Malformed GET URL.", 400)
		return
	}

	ServeBlobRef(conn, req, blobRef, h.Fetcher)
}

// ServeBlobRef serves a blob.
func ServeBlobRef(rw http.ResponseWriter, req *http.Request, blobRef blob.Ref, fetcher blob.StreamingFetcher) {
	seekFetcher := blob.SeekerFromStreamingFetcher(fetcher)

	file, size, err := seekFetcher.Fetch(blobRef)
	switch err {
	case nil:
		break
	case os.ErrNotExist:
		rw.WriteHeader(http.StatusNotFound)
		fmt.Fprintf(rw, "Blob %q not found", blobRef)
		return
	default:
		httputil.ServeError(rw, req, err)
		return
	}
	defer file.Close()
	var content io.ReadSeeker = file

	rw.Header().Set("Content-Type", "application/octet-stream")
	if req.Header.Get("Range") == "" {
		// If it's small and all UTF-8, assume it's text and
		// just render it in the browser.  This is more for
		// demos/debuggability than anything else.  It isn't
		// part of the spec.
		if size <= 32<<10 {
			var buf bytes.Buffer
			_, err := io.Copy(&buf, file)
			if err != nil {
				httputil.ServeError(rw, req, err)
				return
			}
			if utf8.Valid(buf.Bytes()) {
				rw.Header().Set("Content-Type", "text/plain; charset=utf-8")
			}
			content = bytes.NewReader(buf.Bytes())
		}
	}

	http.ServeContent(rw, req, "", dummyModTime, content)
}

// dummyModTime is an arbitrary point in time that we send as fake modtimes for blobs.
// Because blobs are content-addressable, they can never change, so it's better to send
// *some* modtime and let clients do "If-Modified-Since" requests for it.
// This time is the first commit of the Camlistore project.
var dummyModTime = time.Unix(1276213335, 0)

func blobFromURLPath(path string) blob.Ref {
	matches := kGetPattern.FindStringSubmatch(path)
	if len(matches) != 3 {
		return blob.Ref{}
	}
	return blob.ParseOrZero(strings.TrimPrefix(matches[0], "/camli/"))
}

// For client testing.
func simulatePrematurelyClosedConnection(conn http.ResponseWriter, req *http.Request) {
	flusher, ok := conn.(http.Flusher)
	if !ok {
		return
	}
	hj, ok := conn.(http.Hijacker)
	if !ok {
		return
	}
	for n := 1; n <= 100; n++ {
		fmt.Fprintf(conn, "line %d\n", n)
		flusher.Flush()
	}
	wrc, _, _ := hj.Hijack()
	wrc.Close() // without sending final chunk; should be an error for the client
}
