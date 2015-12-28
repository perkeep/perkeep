/*
Copyright 2013 The Camlistore Authors.

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

package server

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"camlistore.org/pkg/auth"
	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/blobserver"
	"camlistore.org/pkg/blobserver/gethandler"
	"camlistore.org/pkg/httputil"
	"camlistore.org/pkg/schema"
	"go4.org/jsonconfig"
)

type responseType int

const (
	badRequest          responseType = 400
	unauthorizedRequest responseType = 401
)

type errorCode int

const (
	noError errorCode = iota
	assembleNonTransitive
	invalidMethod
	invalidURL
	invalidVia
	shareBlobInvalid
	shareBlobTooLarge
	shareExpired
	shareFetchFailed
	shareReadFailed
	shareTargetInvalid
	shareNotTransitive
	viaChainFetchFailed
	viaChainInvalidLink
	viaChainReadFailed
)

var errorCodeStr = [...]string{
	noError:               "noError",
	assembleNonTransitive: "assembleNonTransitive",
	invalidMethod:         "invalidMethod",
	invalidURL:            "invalidURL",
	invalidVia:            "invalidVia",
	shareBlobInvalid:      "shareBlobInvalid",
	shareBlobTooLarge:     "shareBlobTooLarge",
	shareExpired:          "shareExpired",
	shareFetchFailed:      "shareFetchFailed",
	shareReadFailed:       "shareReadFailed",
	shareTargetInvalid:    "shareTargetInvalid",
	shareNotTransitive:    "shareNotTransitive",
	viaChainFetchFailed:   "viaChainFetchFailed",
	viaChainInvalidLink:   "viaChainInvalidLink",
	viaChainReadFailed:    "viaChainReadFailed",
}

func (ec errorCode) String() string {
	if ec < 0 || int(ec) >= len(errorCodeStr) || errorCodeStr[ec] == "" {
		return fmt.Sprintf("ErrCode#%d", int(ec))
	}
	return errorCodeStr[ec]
}

type shareError struct {
	code     errorCode
	response responseType
	message  string
}

func (e *shareError) Error() string {
	return fmt.Sprintf("share: %v (code=%v, type=%v)", e.message, e.code, e.response)
}

func unauthorized(code errorCode, format string, args ...interface{}) *shareError {
	return &shareError{
		code: code, response: unauthorizedRequest, message: fmt.Sprintf(format, args...),
	}
}

const fetchFailureDelay = 200 * time.Millisecond

// ShareHandler handles the requests for "share" (and shared) blobs.
type shareHandler struct {
	fetcher blob.Fetcher
	log     bool
}

func init() {
	blobserver.RegisterHandlerConstructor("share", newShareFromConfig)
}

func newShareFromConfig(ld blobserver.Loader, conf jsonconfig.Obj) (http.Handler, error) {
	blobRoot := conf.RequiredString("blobRoot")
	if blobRoot == "" {
		return nil, errors.New("No blobRoot defined for share handler")
	}
	if err := conf.Validate(); err != nil {
		return nil, err
	}

	bs, err := ld.GetStorage(blobRoot)
	if err != nil {
		return nil, fmt.Errorf("failed to get share handler's storage at %q: %v", blobRoot, err)
	}
	fetcher, ok := bs.(blob.Fetcher)
	if !ok {
		return nil, errors.New("share handler's storage not a Fetcher.")
	}
	sh := &shareHandler{
		fetcher: fetcher,
		log:     true,
	}
	return sh, nil
}

var timeSleep = time.Sleep // for tests

// Unauthenticated user.  Be paranoid.
func handleGetViaSharing(rw http.ResponseWriter, req *http.Request,
	blobRef blob.Ref, fetcher blob.Fetcher) error {
	if !httputil.IsGet(req) {
		return &shareError{code: invalidMethod, response: badRequest, message: "Invalid method"}
	}

	rw.Header().Set("Access-Control-Allow-Origin", "*")

	viaPathOkay := false
	startTime := time.Now()
	defer func() {
		if !viaPathOkay {
			// Insert a delay, to hide timing attacks probing
			// for the existence of blobs.
			sleep := fetchFailureDelay - (time.Now().Sub(startTime))
			timeSleep(sleep)
		}
	}()
	viaBlobs := make([]blob.Ref, 0)
	if via := req.FormValue("via"); via != "" {
		for _, vs := range strings.Split(via, ",") {
			if br, ok := blob.Parse(vs); ok {
				viaBlobs = append(viaBlobs, br)
			} else {
				return &shareError{code: invalidVia, response: badRequest, message: "Malformed blobref in via param"}
			}
		}
	}

	fetchChain := make([]blob.Ref, 0)
	fetchChain = append(fetchChain, viaBlobs...)
	fetchChain = append(fetchChain, blobRef)
	isTransitive := false
	for i, br := range fetchChain {
		switch i {
		case 0:
			file, size, err := fetcher.Fetch(br)
			if err != nil {
				return unauthorized(shareFetchFailed, "Fetch chain 0 of %s failed: %v", br, err)
			}
			defer file.Close()
			if size > schema.MaxSchemaBlobSize {
				return unauthorized(shareBlobTooLarge, "Fetch chain 0 of %s too large", br)
			}
			blob, err := schema.BlobFromReader(br, file)
			if err != nil {
				return unauthorized(shareReadFailed, "Can't create a blob from %v: %v", br, err)
			}
			share, ok := blob.AsShare()
			if !ok {
				return unauthorized(shareBlobInvalid, "Fetch chain 0 of %s wasn't a valid Share (is %q)", br, blob.Type())
			}
			if share.IsExpired() {
				return unauthorized(shareExpired, "Share is expired")
			}
			if len(fetchChain) > 1 && fetchChain[1].String() != share.Target().String() {
				return unauthorized(shareTargetInvalid,
					"Fetch chain 0->1 (%s -> %q) unauthorized, expected hop to %q",
					br, fetchChain[1], share.Target())
			}
			isTransitive = share.IsTransitive()
			if len(fetchChain) > 2 && !isTransitive {
				return unauthorized(shareNotTransitive, "Share is not transitive")
			}
		case len(fetchChain) - 1:
			// Last one is fine (as long as its path up to here has been proven, and it's
			// not the first thing in the chain)
			continue
		default:
			rc, _, err := fetcher.Fetch(br)
			if err != nil {
				return unauthorized(viaChainFetchFailed, "Fetch chain %d of %s failed: %v", i, br, err)
			}
			defer rc.Close()
			lr := io.LimitReader(rc, schema.MaxSchemaBlobSize)
			slurpBytes, err := ioutil.ReadAll(lr)
			if err != nil {
				return unauthorized(viaChainReadFailed,
					"Fetch chain %d of %s failed in slurp: %v", i, br, err)
			}
			saught := fetchChain[i+1]
			if !bytesHaveSchemaLink(br, slurpBytes, saught) {
				return unauthorized(viaChainInvalidLink,
					"Fetch chain %d of %s failed; no reference to %s", i, br, saught)
			}
		}
	}

	if assemble, _ := strconv.ParseBool(req.FormValue("assemble")); assemble {
		if !isTransitive {
			return unauthorized(assembleNonTransitive, "Cannot assemble non-transitive share")
		}
		dh := &DownloadHandler{
			Fetcher: fetcher,
			// TODO(aa): It would be nice to specify a local cache here, as the UI handler does.
		}
		dh.ServeHTTP(rw, req, blobRef)
	} else {
		gethandler.ServeBlobRef(rw, req, blobRef, fetcher)
	}
	viaPathOkay = true
	return nil
}

func (h *shareHandler) serveHTTP(rw http.ResponseWriter, req *http.Request) error {
	var err error
	pathSuffix := httputil.PathSuffix(req)
	if len(pathSuffix) == 0 {
		// This happens during testing because we don't go through PrefixHandler
		pathSuffix = strings.TrimLeft(req.URL.Path, "/")
	}
	pathParts := strings.SplitN(pathSuffix, "/", 2)
	blobRef, ok := blob.Parse(pathParts[0])
	if !ok {
		err = &shareError{code: invalidURL, response: badRequest,
			message: fmt.Sprintf("Malformed share pathSuffix: %s", pathSuffix)}
	} else {
		err = handleGetViaSharing(rw, req, blobRef, h.fetcher)
	}
	if se, ok := err.(*shareError); ok {
		switch se.response {
		case badRequest:
			httputil.BadRequestError(rw, err.Error())
		case unauthorizedRequest:
			if h.log {
				log.Print(err)
			}
			auth.SendUnauthorized(rw, req)
		}
	}
	return err
}

func (h *shareHandler) ServeHTTP(rw http.ResponseWriter, req *http.Request) {
	h.serveHTTP(rw, req)
}

// bytesHaveSchemaLink reports whether bb is a valid Camlistore schema
// blob and has target somewhere in a schema field used to represent a
// Merkle-tree-ish file or directory.
func bytesHaveSchemaLink(br blob.Ref, bb []byte, target blob.Ref) bool {
	// Fast path for no:
	if !bytes.Contains(bb, []byte(target.String())) {
		return false
	}
	b, err := schema.BlobFromReader(br, bytes.NewReader(bb))
	if err != nil {
		return false
	}
	typ := b.Type()
	switch typ {
	case "file", "blob":
		for _, bp := range b.ByteParts() {
			if bp.BlobRef.Valid() {
				return bp.BlobRef == target
			}
			if bp.BytesRef.Valid() {
				return bp.BytesRef == target
			}
		}
	case "directory":
		if d, ok := b.DirectoryEntries(); ok {
			return d == target
		}
	case "static-set":
		for _, m := range b.StaticSetMembers() {
			if m == target {
				return true
			}
		}
	}
	return false
}
