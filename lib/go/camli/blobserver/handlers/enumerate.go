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
	"camli/blobref"
	"camli/blobserver"
	"fmt"
	"log"
	"http"
	"os"
	"strconv"
)

const defaultMaxEnumerate uint = 10000
const defaultEnumerateSize = 100

type blobInfo struct {
	*blobref.BlobRef
	*os.FileInfo
	os.Error
}


func CreateEnumerateHandler(storage blobserver.Storage) func(http.ResponseWriter, *http.Request) {
	return func(conn http.ResponseWriter, req *http.Request) {
		handleEnumerateBlobs(conn, req, storage)
	}
}

const errMsgMaxWaitSecWithAfter = "Can't use 'maxwaitsec' with 'after'.\n"

func handleEnumerateBlobs(conn http.ResponseWriter, req *http.Request, storage blobserver.BlobEnumerator) {
	// Potential input parameters
	formValueLimit := req.FormValue("limit")
	formValueMaxWaitSec := req.FormValue("maxwaitsec")
	formValueAfter := req.FormValue("after")

	var (
		err   os.Error
		limit uint = defaultEnumerateSize
	)

	maxEnumerate := defaultMaxEnumerate
	if config, ok := storage.(blobserver.MaxEnumerateConfig); ok {
		maxEnumerate = config.MaxEnumerate() - 1 // Since we'll add one below.
	}

	if formValueLimit != "" {
		limit, err = strconv.Atoui(formValueLimit)
		if err != nil || limit > maxEnumerate {
			limit = maxEnumerate
		}
	}

	waitSeconds := 0
	if formValueMaxWaitSec != "" {
		waitSeconds, _ = strconv.Atoi(formValueMaxWaitSec)
		if waitSeconds != 0 && formValueAfter != "" {
			conn.WriteHeader(http.StatusBadRequest)
			fmt.Fprintf(conn, errMsgMaxWaitSecWithAfter)
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

	conn.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	fmt.Fprintf(conn, "{\n  \"blobs\": [\n")

	blobch := make(chan blobref.SizedBlobRef, 100)
	resultch := make(chan os.Error, 1)
	go func() {
		resultch <- storage.EnumerateBlobs(blobch, formValueAfter, limit+1, waitSeconds)
	}()

	after := ""
	needsComma := false

	endsReached := 0
	gotBlobs := uint(0)
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
			if gotBlobs > limit {
				// We requested one more from storage than the user asked for.
				// Now we know to return a "continueAfter" response key.
				// But we don't return this blob.
				continue
			}
			blobName := sb.BlobRef.String()
			if needsComma {
				fmt.Fprintf(conn, ",\n")
			}
			fmt.Fprintf(conn, "    {\"blobRef\": \"%s\", \"size\": %d}",
				blobName, sb.Size)
			after = blobName
			needsComma = true
		case err := <-resultch:
			if err != nil {
				log.Printf("Error during enumerate: %v", err)
				fmt.Fprintf(conn, "{{{ SERVER ERROR }}}")
				return
			}
			endsReached++
		}
	}
	fmt.Fprintf(conn, "\n  ]")
	if after != "" {
		fmt.Fprintf(conn, ",\n  \"continueAfter\": \"%s\"", after)
	}
	const longPollSupported = true
	if longPollSupported {
		fmt.Fprintf(conn, ",\n  \"canLongPoll\": true")
	}
	fmt.Fprintf(conn, "\n}\n")
}
