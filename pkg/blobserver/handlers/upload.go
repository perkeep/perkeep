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
	"crypto/sha1"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"regexp"
	"strings"
	"time"

	"camlistore.org/pkg/blobref"
	"camlistore.org/pkg/blobserver"
	"camlistore.org/pkg/httputil"
	"camlistore.org/pkg/jsonsign/signhandler"
	"camlistore.org/pkg/schema"
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

// vivify verifies that all the chunks for the file described by fileblob are on the blobserver.
// It makes a planned permanode, signs it, and uploads it. It finally makes a camliContent claim
// on that permanode for fileblob, signs it, and uploads it to the blobserver.
func vivify(blobReceiver blobserver.BlobReceiveConfiger, fileblob blobref.SizedBlobRef) error {
	sf, ok := blobReceiver.(blobref.StreamingFetcher)
	if !ok {
		return fmt.Errorf("BlobReceiver is not a StreamingFetcher")
	}
	fetcher := blobref.SeekerFromStreamingFetcher(sf)
	fr, err := schema.NewFileReader(fetcher, fileblob.BlobRef)
	if err != nil {
		return fmt.Errorf("Filereader error for blobref %v: %v", fileblob.BlobRef.String(), err)
	}
	defer fr.Close()

	h := sha1.New()
	n, err := io.Copy(h, fr)
	if err != nil {
		return fmt.Errorf("Could not read all file of blobref %v: %v", fileblob.BlobRef.String(), err)
	}
	if n != fr.Size() {
		return fmt.Errorf("Could not read all file of blobref %v. Wanted %v, got %v", fileblob.BlobRef.String(), fr.Size(), n)
	}

	config := blobReceiver.Config()
	if config == nil {
		return errors.New("blobReceiver has no config")
	}
	hf := config.HandlerFinder
	if hf == nil {
		return errors.New("blobReceiver config has no HandlerFinder")
	}
	JSONSignRoot, sh, err := hf.FindHandlerByType("jsonsign")
	// TODO(mpl): second check should not be necessary, and yet it happens. Figure it out.
	if err != nil || sh == nil {
		return errors.New("jsonsign handler not found")
	}
	sigHelper, ok := sh.(*signhandler.Handler)
	if !ok {
		return errors.New("handler is not a JSON signhandler")
	}
	discoMap := sigHelper.DiscoveryMap(JSONSignRoot)
	publicKeyBlobRef, ok := discoMap["publicKeyBlobRef"].(string)
	if !ok {
		return fmt.Errorf("Discovery: json decoding error: %v", err)
	}

	unsigned := schema.NewHashPlannedPermanode(h)
	unsigned["camliSigner"] = publicKeyBlobRef
	signed, err := sigHelper.SignMap(unsigned)
	if err != nil {
		return fmt.Errorf("Signing permanode %v: %v", signed, err)
	}
	signedPerm := blobref.SHA1FromString(signed)
	_, err = blobReceiver.ReceiveBlob(signedPerm, strings.NewReader(signed))
	if err != nil {
		return fmt.Errorf("While uploading signed permanode %v: %v", signed, err)
	}

	contentAttr := schema.NewSetAttributeClaim(signedPerm, "camliContent", fileblob.BlobRef.String())
	claimDate, err := time.Parse(time.RFC3339, fr.FileSchema().UnixMtime)
	contentAttr.SetClaimDate(claimDate)
	contentAttr["camliSigner"] = publicKeyBlobRef
	signed, err = sigHelper.SignMap(contentAttr)
	if err != nil {
		return fmt.Errorf("Signing camliContent claim: %v", err)
	}
	signedClaim := blobref.SHA1FromString(signed)
	_, err = blobReceiver.ReceiveBlob(signedClaim, strings.NewReader(signed))
	if err != nil {
		return fmt.Errorf("While uploading signed camliContent claim %v: %v", signed, err)
	}
	return nil
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

		contentDisposition, params, err := mime.ParseMediaType(mimePart.Header.Get("Content-Disposition"))
		if err != nil {
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

		// TODO: wrap the mimePart reader in a LimitReader-ish
		// wrapper, setting an error flag after reading
		// blobserver.MaxBlobSize+1 bytes, then failing.
		blobGot, err := blobReceiver.ReceiveBlob(ref, mimePart)
		if err != nil {
			addError(fmt.Sprintf("Error receiving blob %v: %v\n", ref, err))
			break
		}
		log.Printf("Received blob %v\n", blobGot)
		receivedBlobs = append(receivedBlobs, blobGot)
	}

	ret, err := commonUploadResponse(blobReceiver, req)
	if err != nil {
		httputil.ServerError(conn, req, err)
	}

	received := make([]map[string]interface{}, 0)
	for _, got := range receivedBlobs {
		blob := make(map[string]interface{})
		blob["blobRef"] = got.BlobRef.String()
		blob["size"] = got.Size
		received = append(received, blob)
	}
	ret["received"] = received

	if req.Header.Get("X-Camlistore-Vivify") == "1" {
		for _, got := range receivedBlobs {
			err := vivify(blobReceiver, got)
			if err != nil {
				addError(fmt.Sprintf("Error vivifying blob %v: %v\n", got.BlobRef.String(), err))
			} else {
				conn.Header().Add("X-Camlistore-Vivified", got.BlobRef.String())
			}
		}
	}

	if errText != "" {
		ret["errorText"] = errText
	}

	httputil.ReturnJSON(conn, ret)
}

func commonUploadResponse(configer blobserver.Configer, req *http.Request) (map[string]interface{}, error) {
	ret := make(map[string]interface{})
	ret["maxUploadSize"] = blobserver.MaxBlobSize
	ret["uploadUrlExpirationSeconds"] = 86400

	if configer == nil {
		err := errors.New("Cannot build uploadUrl: configer is nil")
		log.Printf("%v", err)
		return nil, err
	} else if config := configer.Config(); config != nil {
		// TODO: camli/upload isn't part of the spec.  we should pick
		// something different here just to make it obvious that this
		// isn't a well-known URL and accidentally encourage lazy clients.
		baseURL, err := httputil.BaseURL(config.URLBase, req)
		if err != nil {
			errStr := fmt.Sprintf("Cannot build uploadUrl: %v", err)
			log.Printf(errStr)
			return ret, fmt.Errorf(errStr)
		}
		ret["uploadUrl"] = baseURL + "/camli/upload"
	} else {
		err := errors.New("Cannot build uploadUrl: configer.Config is nil")
		log.Printf("%v", err)
		return nil, err
	}
	return ret, nil
}

// NOTE: not part of the spec at present.  old.  might be re-introduced.
var kPutPattern = regexp.MustCompile(`^/camli/([a-z0-9]+)-([a-f0-9]+)$`)

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
		httputil.ServerError(conn, req, err)
		return
	}

	fmt.Fprint(conn, "OK")
}
