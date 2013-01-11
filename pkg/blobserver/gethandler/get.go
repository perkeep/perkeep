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

package gethandler

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"camlistore.org/pkg/auth"
	"camlistore.org/pkg/blobref"
	"camlistore.org/pkg/blobserver"
	"camlistore.org/pkg/httputil"
	"camlistore.org/pkg/misc/httprange" // TODO: delete this package, use http.ServeContent
)

var kGetPattern = regexp.MustCompile(`/camli/` + blobref.Pattern + `$`)

// Handler is the HTTP handler for serving GET requests of blobs.
type Handler struct {
	Fetcher           blobref.StreamingFetcher
	AllowGlobalAccess bool
}

func CreateGetHandler(fetcher blobref.StreamingFetcher) func(http.ResponseWriter, *http.Request) {
	gh := &Handler{Fetcher: fetcher}
	return func(conn http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/camli/sha1-deadbeef00000000000000000000000000000000" {
			// Test handler.
			simulatePrematurelyClosedConnection(conn, req)
			return
		}
		gh.ServeHTTP(conn, req)
	}
}

const fetchFailureDelayNs = 200e6 // 200 ms
const maxJSONSize = 64 * 1024     // should be enough for everyone

func (h *Handler) ServeHTTP(conn http.ResponseWriter, req *http.Request) {
	blobRef := blobFromUrlPath(req.URL.Path)
	if blobRef == nil {
		http.Error(conn, "Malformed GET URL.", 400)
		return
	}

	switch {
	case h.AllowGlobalAccess || auth.Allowed(req, auth.OpGet):
		serveBlobRef(conn, req, blobRef, h.Fetcher)
	case auth.TriedAuthorization(req):
		log.Printf("Attempted authorization failed on %s", req.URL)
		auth.SendUnauthorized(conn, req)
	default:
		handleGetViaSharing(conn, req, blobRef, h.Fetcher)
	}
}

// serveBlobRef sends 'blobref' to 'conn' as directed by the Range header in 'req'
func serveBlobRef(conn http.ResponseWriter, req *http.Request,
	blobRef *blobref.BlobRef, fetcher blobref.StreamingFetcher) {

	if w, ok := fetcher.(blobserver.ContextWrapper); ok {
		fetcher = w.WrapContext(req)
	}

	file, size, err := fetcher.FetchStreaming(blobRef)
	switch err {
	case nil:
		break
	case os.ErrNotExist:
		conn.WriteHeader(http.StatusNotFound)
		fmt.Fprintf(conn, "Blob %q not found", blobRef)
		return
	default:
		httputil.ServerError(conn, req, err)
		return
	}

	defer file.Close()

	seeker, isSeeker := file.(io.Seeker)
	reqRange := httprange.FromRequest(req)
	if reqRange.SkipBytes() != 0 && isSeeker {
		// TODO: set the Range-specific response headers too,
		// acknowledging that we honored the content range
		// request.
		_, err = seeker.Seek(reqRange.SkipBytes(), 0)
		if err != nil {
			httputil.ServerError(conn, req, err)
			return
		}
	}

	var input io.Reader = file
	if reqRange.LimitBytes() != -1 {
		input = io.LimitReader(file, reqRange.LimitBytes())
	}

	remainBytes := size - reqRange.SkipBytes()
	if reqRange.LimitBytes() != -1 &&
		reqRange.LimitBytes() < remainBytes {
		remainBytes = reqRange.LimitBytes()
	}

	conn.Header().Set("Content-Type", "application/octet-stream")
	if reqRange.IsWholeFile() {
		conn.Header().Set("Content-Length", strconv.FormatInt(size, 10))
		// If it's small and all UTF-8, assume it's text and
		// just render it in the browser.  This is more for
		// demos/debuggability than anything else.  It isn't
		// part of the spec.
		if size <= 32<<10 {
			var buf bytes.Buffer
			_, err := io.Copy(&buf, input)
			if err != nil {
				httputil.ServerError(conn, req, err)
				return
			}
			if utf8.Valid(buf.Bytes()) {
				conn.Header().Set("Content-Type", "text/plain; charset=utf-8")
			}
			input = &buf
		}
	}

	if !reqRange.IsWholeFile() {
		conn.Header().Set("Content-Range",
			fmt.Sprintf("bytes %d-%d/%d", reqRange.SkipBytes(),
				reqRange.SkipBytes()+remainBytes,
				size))
		conn.WriteHeader(http.StatusPartialContent)
	}
	bytesCopied, err := io.Copy(conn, input)

	// If there's an error at this point, it's too late to tell the client,
	// as they've already been receiving bytes.  But they should be smart enough
	// to verify the digest doesn't match.  But we close the (chunked) response anyway,
	// to further signal errors.
	killConnection := func() {
		if hj, ok := conn.(http.Hijacker); ok {
			log.Printf("Force-closing TCP connection to signal error sending %q", blobRef)
			if closer, _, err := hj.Hijack(); err != nil {
				closer.Close()
			}
		}
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "Error sending file: %v, err=%v\n", blobRef, err)
		killConnection()
		return
	}

	if bytesCopied != remainBytes {
		fmt.Fprintf(os.Stderr, "Error sending file: %v, copied=%d, not %d\n", blobRef,
			bytesCopied, remainBytes)
		killConnection()
		return
	}
}

// Unauthenticated user.  Be paranoid.
func handleGetViaSharing(conn http.ResponseWriter, req *http.Request,
	blobRef *blobref.BlobRef, fetcher blobref.StreamingFetcher) {

	if w, ok := fetcher.(blobserver.ContextWrapper); ok {
		fetcher = w.WrapContext(req)
	}

	viaPathOkay := false
	startTime := time.Now()
	defer func() {
		if !viaPathOkay {
			// Insert a delay, to hide timing attacks probing
			// for the existence of blobs.
			sleep := fetchFailureDelayNs - (time.Now().Sub(startTime))
			if sleep > 0 {
				time.Sleep(sleep)
			}
		}
	}()
	viaBlobs := make([]*blobref.BlobRef, 0)
	if via := req.FormValue("via"); via != "" {
		for _, vs := range strings.Split(via, ",") {
			if br := blobref.Parse(vs); br == nil {
				httputil.BadRequestError(conn, "Malformed blobref in via param")
				return
			} else {
				viaBlobs = append(viaBlobs, br)
			}
		}
	}

	fetchChain := make([]*blobref.BlobRef, 0)
	fetchChain = append(fetchChain, viaBlobs...)
	fetchChain = append(fetchChain, blobRef)
	for i, br := range fetchChain {
		switch i {
		case 0:
			file, size, err := fetcher.FetchStreaming(br)
			if err != nil {
				log.Printf("Fetch chain 0 of %s failed: %v", br.String(), err)
				auth.SendUnauthorized(conn, req)
				return
			}
			defer file.Close()
			if size > maxJSONSize {
				log.Printf("Fetch chain 0 of %s too large", br.String())
				auth.SendUnauthorized(conn, req)
				return
			}
			jd := json.NewDecoder(file)
			m := make(map[string]interface{})
			if err := jd.Decode(&m); err != nil {
				log.Printf("Fetch chain 0 of %s wasn't JSON: %v", br.String(), err)
				auth.SendUnauthorized(conn, req)
				return
			}
			if m["camliType"].(string) != "share" {
				log.Printf("Fetch chain 0 of %s wasn't a share", br.String())
				auth.SendUnauthorized(conn, req)
				return
			}
			if len(fetchChain) > 1 && fetchChain[1].String() != m["target"].(string) {
				log.Printf("Fetch chain 0->1 (%s -> %q) unauthorized, expected hop to %q",
					br.String(), fetchChain[1].String(), m["target"])
				auth.SendUnauthorized(conn, req)
				return
			}
		case len(fetchChain) - 1:
			// Last one is fine (as long as its path up to here has been proven, and it's
			// not the first thing in the chain)
			continue
		default:
			file, _, err := fetcher.FetchStreaming(br)
			if err != nil {
				log.Printf("Fetch chain %d of %s failed: %v", i, br.String(), err)
				auth.SendUnauthorized(conn, req)
				return
			}
			defer file.Close()
			lr := io.LimitReader(file, maxJSONSize)
			slurpBytes, err := ioutil.ReadAll(lr)
			if err != nil {
				log.Printf("Fetch chain %d of %s failed in slurp: %v", i, br.String(), err)
				auth.SendUnauthorized(conn, req)
				return
			}
			saught := fetchChain[i+1].String()
			if bytes.IndexAny(slurpBytes, saught) == -1 {
				log.Printf("Fetch chain %d of %s failed; no reference to %s",
					i, br.String(), saught)
				auth.SendUnauthorized(conn, req)
				return
			}
		}
	}

	viaPathOkay = true

	serveBlobRef(conn, req, blobRef, fetcher)

}

func blobFromUrlPath(path string) *blobref.BlobRef {
	return blobref.FromPattern(kGetPattern, path)
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
