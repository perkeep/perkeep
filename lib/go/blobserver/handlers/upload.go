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
	"camli/httputil"
	"flag"
	"fmt"
	"http"
	"log"
	"mime"
	"regexp"
	"strings"
)

var flagQueuePartitions = flag.String("queue-partitions", "", "Comma-separated list of queue partitions to reference uploaded blobs into.  Typically one for your indexer and one per mirror full syncer.")

func CreateUploadHandler(storage blobserver.Storage) func(http.ResponseWriter, *http.Request) {
	return func(conn http.ResponseWriter, req *http.Request) {
		handleMultiPartUpload(conn, req, storage)
	}
}

func handleMultiPartUpload(conn http.ResponseWriter, req *http.Request, storage blobserver.BlobReceiver) {
	if !(req.Method == "POST" && req.URL.Path == "/camli/upload") {
		httputil.BadRequestError(conn, "Inconfigured handler.")
		return
	}

	receivedBlobs := make([]*blobref.SizedBlobRef, 0, 10)

	multipart, err := req.MultipartReader()
	if multipart == nil {
		httputil.BadRequestError(conn, fmt.Sprintf(
			"Expected multipart/form-data POST request; %v", err))
		return
	}

	var errText string
	addError := func(s string) {
		log.Printf("Client error: %s", s)
		if errText == "" {
			errText = s
			return
		}
		errText = errText + "\n" + s
	}

	for {
		part, err := multipart.NextPart()
		if err != nil {
			addError(fmt.Sprintf("Error reading multipart section: %v", err))
			break
		}
		if part == nil {
			break
		}

		contentDisposition, params := mime.ParseMediaType(part.Header["Content-Disposition"])
		if contentDisposition != "form-data" {
			addError(fmt.Sprintf("Expected Content-Disposition of \"form-data\"; got %q", contentDisposition))
			break
		}

		formName := params["name"]
		ref := blobref.Parse(formName)
		if ref == nil {
			addError(fmt.Sprintf("Ignoring form key %q", formName))
			continue
		}

		_, hasContentType := part.Header["Content-Type"]
		if !hasContentType {
			addError(fmt.Sprintf("Expected Content-Type header for blobref %s; see spec", ref))
			continue
		}

		_, hasFileName := params["filename"]
		if !hasFileName {
			addError(fmt.Sprintf("Expected 'filename' Content-Disposition parameter for blobref %s; see spec", ref))
			continue
		}

		blobGot, err := storage.ReceiveBlob(ref, part, getMirrorPartitions())
		if err != nil {
			addError(fmt.Sprintf("Error receiving blob %v: %v\n", ref, err))
			break
		}
		log.Printf("Received blob %v\n", blobGot)
		receivedBlobs = append(receivedBlobs, blobGot)
	}

	log.Println("Done reading multipart body.")
	ret := commonUploadResponse(req)

	received := make([]map[string]interface{}, 0)
	for _, got := range receivedBlobs {
		log.Printf("Got blob: %v\n", got)
		blob := make(map[string]interface{})
		blob["blobRef"] = got.BlobRef.String()
		blob["size"] = got.Size
		received = append(received, blob)
	}
	ret["received"] = received

	if errText != "" {
		ret["errorText"] = errText
	}

	httputil.ReturnJson(conn, ret)
}

func commonUploadResponse(req *http.Request) map[string]interface{} {
	ret := make(map[string]interface{})
	ret["maxUploadSize"] = 2147483647 // 2GB.. *shrug*
	ret["uploadUrlExpirationSeconds"] = 86400
	if len(req.Host) > 0 {
		scheme := "http" // TODO: https
		ret["uploadUrl"] = fmt.Sprintf("%s://%s/camli/upload",
			scheme, req.Host)
	} else {
		ret["uploadUrl"] = "/camli/upload"
	}
	return ret
}

func getMirrorPartitions() []blobserver.Partition {
	mp := make([]blobserver.Partition, 0)
	if *flagQueuePartitions != "" {
		for _, partName := range strings.Split(*flagQueuePartitions, ",", -1) {
			mp = append(mp, blobserver.Partition(partName))
		}
	}
	return mp
}

// NOTE: not part of the spec at present.  old.  might be re-introduced.
var kPutPattern *regexp.Regexp = regexp.MustCompile(`^/camli/([a-z0-9]+)-([a-f0-9]+)$`)

// NOTE: not part of the spec at present.  old.  might be re-introduced.
func CreateNonStandardPutHandler(storage blobserver.Storage) func(http.ResponseWriter, *http.Request) {
	return func(conn http.ResponseWriter, req *http.Request) {
		handlePut(conn, req, storage)
	}
}

func handlePut(conn http.ResponseWriter, req *http.Request, storage blobserver.BlobReceiver) {
	blobRef := blobref.FromPattern(kPutPattern, req.URL.Path)
	if blobRef == nil {
		httputil.BadRequestError(conn, "Malformed PUT URL.")
		return
	}

	if !blobRef.IsSupported() {
		httputil.BadRequestError(conn, "unsupported object hash function")
		return
	}

	_, err := storage.ReceiveBlob(blobRef, req.Body, getMirrorPartitions())
	if err != nil {
		httputil.ServerError(conn, err)
		return
	}

	fmt.Fprint(conn, "OK")
}
