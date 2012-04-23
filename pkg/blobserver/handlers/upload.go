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
	"camlistore.org/pkg/blobref"
	"camlistore.org/pkg/blobserver"
	"camlistore.org/pkg/httputil"
	"io"

	"fmt"
	"log"
	"mime"
	"net/http"
	"regexp"
	"strings"
)

// We used to require that multipart sections had a content type and
// filename to make App Engine happy. Now that App Engine supports up
// to 32 MB requests and programatic blob writing we can just do this
// ourselves and stop making compromises in the spec.  Also because
// the JavaScript FormData spec (http://www.w3.org/TR/XMLHttpRequest2/)
// doesn't let you set those.
const oldAppEngineHappySpec = false

func CreateUploadHandler(storage blobserver.BlobReceiveConfiger) func(http.ResponseWriter, *http.Request) {
	return func(conn http.ResponseWriter, req *http.Request) {
		handleMultiPartUpload(conn, req, storage)
	}
}

func wrapReceiveConfiger(cw blobserver.ContextWrapper,
	req *http.Request,
	oldRC blobserver.BlobReceiveConfiger) blobserver.BlobReceiveConfiger {

	newRC := cw.WrapContext(req)
	if brc, ok := newRC.(blobserver.BlobReceiveConfiger); ok {
		return brc
	}
	type mixAndMatch struct {
		blobserver.BlobReceiver
		blobserver.Configer
	}
	return &mixAndMatch{newRC, oldRC}
}

func handleMultiPartUpload(conn http.ResponseWriter, req *http.Request, blobReceiver blobserver.BlobReceiveConfiger) {
	if w, ok := blobReceiver.(blobserver.ContextWrapper); ok {
		blobReceiver = wrapReceiveConfiger(w, req, blobReceiver)
	}

	if !(req.Method == "POST" && strings.Contains(req.URL.Path, "/camli/upload")) {
		log.Printf("Inconfigured handler upload handler")
		httputil.BadRequestError(conn, "Inconfigured handler.")
		return
	}

	receivedBlobs := make([]blobref.SizedBlobRef, 0, 10)

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
		mimePart, err := multipart.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			addError(fmt.Sprintf("Error reading multipart section: %v", err))
			break
		}

		//POST-r60:
		//contentDisposition, params, err := mime.ParseMediaType(mimePart.Header.Get("Content-Disposition"))
		//if err != nil {
		//	addError(err.String())
		//	break
		//}
		// r60:
		contentDisposition, params, err := mime.ParseMediaType(mimePart.Header.Get("Content-Disposition"))
		if contentDisposition == "" || err != nil {
			addError("invalid Content-Disposition")
			break
		}

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

		if oldAppEngineHappySpec {
			_, hasContentType := mimePart.Header["Content-Type"]
			if !hasContentType {
				addError(fmt.Sprintf("Expected Content-Type header for blobref %s; see spec", ref))
				continue
			}

			_, hasFileName := params["filename"]
			if !hasFileName {
				addError(fmt.Sprintf("Expected 'filename' Content-Disposition parameter for blobref %s; see spec", ref))
				continue
			}
		}

		blobGot, err := blobReceiver.ReceiveBlob(ref, mimePart)
		if err != nil {
			addError(fmt.Sprintf("Error receiving blob %v: %v\n", ref, err))
			break
		}
		log.Printf("Received blob %v\n", blobGot)
		receivedBlobs = append(receivedBlobs, blobGot)
	}

	ret := commonUploadResponse(blobReceiver, req)

	received := make([]map[string]interface{}, 0)
	for _, got := range receivedBlobs {
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

func commonUploadResponse(configer blobserver.Configer, req *http.Request) map[string]interface{} {
	ret := make(map[string]interface{})
	ret["maxUploadSize"] = 2147483647 // 2GB.. *shrug*. TODO: cut this down, standardize
	ret["uploadUrlExpirationSeconds"] = 86400

	if configer == nil {
		ret["uploadUrl"] = "(Error: configer is nil)"
	} else if config := configer.Config(); config != nil {
		// TODO: camli/upload isn't part of the spec.  we should pick
		// something different here just to make it obvious that this
		// isn't a well-known URL and accidentally encourage lazy clients.
		ret["uploadUrl"] = config.URLBase + "/camli/upload"
	} else {
		ret["uploadUrl"] = "(configer.Config is nil)"
	}
	return ret
}

// NOTE: not part of the spec at present.  old.  might be re-introduced.
var kPutPattern *regexp.Regexp = regexp.MustCompile(`^/camli/([a-z0-9]+)-([a-f0-9]+)$`)

// NOTE: not part of the spec at present.  old.  might be re-introduced.
func CreateNonStandardPutHandler(storage blobserver.Storage) func(http.ResponseWriter, *http.Request) {
	return func(conn http.ResponseWriter, req *http.Request) {
		handlePut(conn, req, storage)
	}
}

func handlePut(conn http.ResponseWriter, req *http.Request, blobReceiver blobserver.BlobReceiver) {
	if w, ok := blobReceiver.(blobserver.ContextWrapper); ok {
		blobReceiver = w.WrapContext(req)
	}

	blobRef := blobref.FromPattern(kPutPattern, req.URL.Path)
	if blobRef == nil {
		httputil.BadRequestError(conn, "Malformed PUT URL.")
		return
	}

	if !blobRef.IsSupported() {
		httputil.BadRequestError(conn, "unsupported object hash function")
		return
	}

	_, err := blobReceiver.ReceiveBlob(blobRef, req.Body)
	if err != nil {
		httputil.ServerError(conn, err)
		return
	}

	fmt.Fprint(conn, "OK")
}
