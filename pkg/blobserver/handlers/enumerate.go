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

package handlers

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/blobserver"
	"camlistore.org/pkg/context"
)

const defaultMaxEnumerate = 10000
const defaultEnumerateSize = 100

type blobInfo struct {
	blob.Ref
	os.FileInfo
	error
}

func CreateEnumerateHandler(storage blobserver.BlobEnumerator) http.Handler {
	return http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		handleEnumerateBlobs(rw, req, storage)
	})
}

const errMsgMaxWaitSecWithAfter = "Can't use 'maxwaitsec' with 'after'.\n"

func handleEnumerateBlobs(rw http.ResponseWriter, req *http.Request, storage blobserver.BlobEnumerator) {
	// Potential input parameters
	formValueLimit := req.FormValue("limit")
	formValueMaxWaitSec := req.FormValue("maxwaitsec")
	formValueAfter := req.FormValue("after")

	maxEnumerate := defaultMaxEnumerate
	if config, ok := storage.(blobserver.MaxEnumerateConfig); ok {
		maxEnumerate = config.MaxEnumerate() - 1 // Since we'll add one below.
	}

	limit := defaultEnumerateSize
	if formValueLimit != "" {
		n, err := strconv.ParseUint(formValueLimit, 10, 32)
		if err != nil || n > uint64(maxEnumerate) {
			limit = maxEnumerate
		} else {
			limit = int(n)
		}
	}

	waitSeconds := 0
	if formValueMaxWaitSec != "" {
		waitSeconds, _ = strconv.Atoi(formValueMaxWaitSec)
		if waitSeconds != 0 && formValueAfter != "" {
			rw.WriteHeader(http.StatusBadRequest)
			fmt.Fprintf(rw, errMsgMaxWaitSecWithAfter)
			return
		}
		switch {
		case waitSeconds < 0:
			waitSeconds = 0
		case waitSeconds > 30:
			// TODO: don't hard-code 30.  push this up into a blobserver interface
			// for getting the configuration of the server (ultimately a flag in
			// in the binary)
			waitSeconds = 30
		}
	}

	rw.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	fmt.Fprintf(rw, "{\n  \"blobs\": [\n")

	loop := true
	needsComma := false
	deadline := time.Now().Add(time.Duration(waitSeconds) * time.Second)
	after := ""
	for loop && (waitSeconds == 0 || time.Now().After(deadline)) {
		if waitSeconds == 0 {
			loop = false
		}

		blobch := make(chan blob.SizedRef, 100)
		resultch := make(chan error, 1)
		go func() {
			resultch <- storage.EnumerateBlobs(context.TODO(), blobch, formValueAfter, limit+1)
		}()

		endsReached := 0
		gotBlobs := 0
		for endsReached < 2 {
			select {
			case sb, ok := <-blobch:
				if !ok {
					endsReached++
					if gotBlobs <= limit {
						after = ""
					}
					continue
				}
				gotBlobs++
				loop = false
				if gotBlobs > limit {
					// We requested one more from storage than the user asked for.
					// Now we know to return a "continueAfter" response key.
					// But we don't return this blob.
					continue
				}
				blobName := sb.Ref.String()
				if needsComma {
					fmt.Fprintf(rw, ",\n")
				}
				fmt.Fprintf(rw, "    {\"blobRef\": \"%s\", \"size\": %d}",
					blobName, sb.Size)
				after = blobName
				needsComma = true
			case err := <-resultch:
				if err != nil {
					log.Printf("Error during enumerate: %v", err)
					fmt.Fprintf(rw, "{{{ SERVER ERROR }}}")
					return
				}
				endsReached++
			}
		}

		if loop {
			blobserver.WaitForBlob(storage, deadline, nil)
		}
	}
	fmt.Fprintf(rw, "\n  ]")
	if after != "" {
		fmt.Fprintf(rw, ",\n  \"continueAfter\": \"%s\"", after)
	}
	const longPollSupported = true
	if longPollSupported {
		fmt.Fprintf(rw, ",\n  \"canLongPoll\": true")
	}
	fmt.Fprintf(rw, "\n}\n")
}
