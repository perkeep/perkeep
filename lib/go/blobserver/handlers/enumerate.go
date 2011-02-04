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

const maxEnumerate = 100000

type blobInfo struct {
	*blobref.BlobRef
	*os.FileInfo
	os.Error
}


func CreateEnumerateHandler(storage blobserver.Storage, partition blobserver.Partition) func(http.ResponseWriter, *http.Request) {
	return func(conn http.ResponseWriter, req *http.Request) {
		handleEnumerateBlobs(conn, req, storage, partition)
	}
}

func handleEnumerateBlobs(conn http.ResponseWriter, req *http.Request, storage blobserver.Storage, partition blobserver.Partition) {
	limit, err := strconv.Atoui(req.FormValue("limit"))
	if err != nil || limit > maxEnumerate {
		limit = maxEnumerate
	}

	conn.SetHeader("Content-Type", "text/javascript; charset=utf-8")
	fmt.Fprintf(conn, "{\n  \"blobs\": [\n")

	blobch := make(chan *blobref.SizedBlobRef, 100)
	resultch := make(chan os.Error, 1)
	go func() {
		resultch <- storage.EnumerateBlobs(blobch, partition, req.FormValue("after"), limit+1)
	}()

	after := ""
	needsComma := false

	endsReached := 0
	gotBlobs := uint(0)
	for endsReached < 2 {
		select {
		case sb := <-blobch:
			if sb == nil {
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
				close(blobch)   // TODO: necessary?
				close(resultch) // TODO: necessary?
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
	fmt.Fprintf(conn, "\n}\n")
}
