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

package main

import (
	"bytes"
	"camli/auth"
	"camli/blobref"
	"camli/httputil"
	"fmt"
	"http"
	"os"
	"io"
	"io/ioutil"
	"json"
	"log"
	"strings"
	"time"
)

func createGetHandler(fetcher blobref.Fetcher) func(http.ResponseWriter, *http.Request) {
	return func(conn http.ResponseWriter, req *http.Request) {
		handleGet(conn, req, fetcher)
	}
}

const fetchFailureDelayNs = 200e6 // 200 ms
const maxJsonSize = 64 * 1024  // should be enough for everyone

func handleGet(conn http.ResponseWriter, req *http.Request, fetcher blobref.Fetcher) {
	isOwner := auth.IsAuthorized(req)

	blobRef := BlobFromUrlPath(req.URL.Path)
	if blobRef == nil {
		httputil.BadRequestError(conn, "Malformed GET URL.")
		return
	}

	var viaBlobs []*blobref.BlobRef
	if !isOwner {
		viaPathOkay := false
		startTime := time.Nanoseconds()
		defer func() {
			if !viaPathOkay {
				// Insert a delay, to hide timing attacks probing
				// for the existence of blobs.
				sleep := fetchFailureDelayNs - (time.Nanoseconds() - startTime)
				if sleep > 0 {
					time.Sleep(sleep)
				}
			}
		}()
		viaBlobs = make([]*blobref.BlobRef, 0)
		if via := req.FormValue("via"); via != "" {
			for _, vs := range strings.Split(via, ",", -1) {
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
				file, size, err := fetcher.Fetch(br)
				if err != nil {
					log.Printf("Fetch chain 0 of %s failed: %v", br.String(), err)
					conn.WriteHeader(http.StatusUnauthorized)
					return
				}
				defer file.Close()
				if size > maxJsonSize {
					log.Printf("Fetch chain 0 of %s too large", br.String())
					conn.WriteHeader(http.StatusUnauthorized)
					return
				}
				jd := json.NewDecoder(file)
				m := make(map[string]interface{})
				if err := jd.Decode(&m); err != nil {
					log.Printf("Fetch chain 0 of %s wasn't JSON: %v", br.String(), err)
					conn.WriteHeader(http.StatusUnauthorized)
					return
				}
				if m["camliType"].(string) != "share" {
					log.Printf("Fetch chain 0 of %s wasn't a share", br.String())
					conn.WriteHeader(http.StatusUnauthorized)
					return
				}
				if len(fetchChain) > 1 && fetchChain[1].String() != m["target"].(string) {
					log.Printf("Fetch chain 0->1 (%s -> %q) unauthorized, expected hop to %q",
						br.String(), fetchChain[1].String(), m["target"])
					conn.WriteHeader(http.StatusUnauthorized)
					return
				}
			case len(fetchChain) - 1:
				// Last one is fine (as long as its path up to here has been proven, and it's
				// not the first thing in the chain)
				continue
			default:
				file, _, err := fetcher.Fetch(br)
				if err != nil {
					log.Printf("Fetch chain %d of %s failed: %v", i, br.String(), err)
					conn.WriteHeader(http.StatusUnauthorized)
					return
				}
				defer file.Close()
				lr := io.LimitReader(file, maxJsonSize)
				slurpBytes, err := ioutil.ReadAll(lr)
				if err != nil {
					log.Printf("Fetch chain %d of %s failed in slurp: %v", i, br.String(), err)
					conn.WriteHeader(http.StatusUnauthorized)
					return
				}
				saught := fetchChain[i+1].String()
				if bytes.IndexAny(slurpBytes, saught) == -1 {
					log.Printf("Fetch chain %d of %s failed; no reference to %s",
						i, br.String(), saught)
                                        conn.WriteHeader(http.StatusUnauthorized)
                                        return
				}
			}
		}
		viaPathOkay = true
	}

	file, size, err := fetcher.Fetch(blobRef)
	switch err {
	case nil:
		break
	case os.ENOENT:
		conn.WriteHeader(http.StatusNotFound)
		fmt.Fprintf(conn, "Object not found.")
		return
	default:
		httputil.ServerError(conn, err)
		return
	}

	defer file.Close()

	reqRange := getRequestedRange(req)
	if reqRange.SkipBytes != 0 {
		_, err = file.Seek(reqRange.SkipBytes, 0)
		if err != nil {
			httputil.ServerError(conn, err)
			return
		}
	}

	var input io.Reader = file
	if reqRange.LimitBytes != -1 {
		input = io.LimitReader(file, reqRange.LimitBytes)
	}

	remainBytes := size - reqRange.SkipBytes
	if reqRange.LimitBytes != -1 &&
		reqRange.LimitBytes < remainBytes {
		remainBytes = reqRange.LimitBytes
	}

	conn.SetHeader("Content-Type", "application/octet-stream")
	if !reqRange.IsWholeFile() {
		conn.SetHeader("Content-Range",
			fmt.Sprintf("bytes %d-%d/%d", reqRange.SkipBytes,
				reqRange.SkipBytes+remainBytes,
				size))
		conn.WriteHeader(http.StatusPartialContent)
	}
	bytesCopied, err := io.Copy(conn, input)

	// If there's an error at this point, it's too late to tell the client,
	// as they've already been receiving bytes.  But they should be smart enough
	// to verify the digest doesn't match.  But we close the (chunked) response anyway,
	// to further signal errors.
	killConnection := func() {
		closer, _, err := conn.Hijack()
		if err != nil {
			closer.Close()
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
