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
	"strings"
	"time"

	"camlistore.org/pkg/auth"
	"camlistore.org/pkg/blobref"
	"camlistore.org/pkg/blobserver"
	"camlistore.org/pkg/blobserver/gethandler"
	"camlistore.org/pkg/httputil"
	"camlistore.org/pkg/jsonconfig"
	"camlistore.org/pkg/schema"
)

const fetchFailureDelay = 200 * time.Millisecond

// ShareHandler handles the requests for "share" (and shared) blobs.
type shareHandler struct {
	blobRoot string

	fetcher blobref.StreamingFetcher
}

func init() {
	blobserver.RegisterHandlerConstructor("share", newShareFromConfig)
}

func newShareFromConfig(ld blobserver.Loader, conf jsonconfig.Obj) (h http.Handler, err error) {
	blobRoot := conf.RequiredString("blobRoot")
	if blobRoot == "" {
		return nil, errors.New("No blobRoot defined for share handler")
	}

	share := &shareHandler{
		blobRoot: blobRoot,
	}
	bs, err := ld.GetStorage(share.blobRoot)
	if err != nil {
		return nil, fmt.Errorf("Share handler's blobRoot of %q error: %v", share.blobRoot, err)
	}
	fetcher, ok := bs.(blobref.StreamingFetcher)
	if !ok {
		return nil, errors.New("Share handler's storage not a StreamingFetcher.")
	}
	share.fetcher = fetcher
	return share, nil
}

// Unauthenticated user.  Be paranoid.
func handleGetViaSharing(conn http.ResponseWriter, req *http.Request,
	blobRef *blobref.BlobRef, fetcher blobref.StreamingFetcher) {
	if req.Method != "GET" && req.Method != "HEAD" {
		httputil.BadRequestError(conn, "Invalid method")
		return
	}
	if w, ok := fetcher.(blobserver.ContextWrapper); ok {
		fetcher = w.WrapContext(req)
	}

	viaPathOkay := false
	startTime := time.Now()
	defer func() {
		if !viaPathOkay {
			// Insert a delay, to hide timing attacks probing
			// for the existence of blobs.
			sleep := fetchFailureDelay - (time.Now().Sub(startTime))
			time.Sleep(sleep)
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
			if size > schema.MaxSchemaBlobSize {
				log.Printf("Fetch chain 0 of %s too large", br.String())
				auth.SendUnauthorized(conn, req)
				return
			}
			blob, err := schema.BlobFromReader(br, file)
			if err != nil {
				log.Printf("Can't create a blob from %v: %v", br.String(), err)
				auth.SendUnauthorized(conn, req)
				return
			}
			share, ok := blob.AsShare()
			if !ok {
				log.Printf("Fetch chain 0 of %s wasn't a valid Share", br.String())
				auth.SendUnauthorized(conn, req)
				return
			}
			if len(fetchChain) > 1 && fetchChain[1].String() != share.Target().String() {
				log.Printf("Fetch chain 0->1 (%s -> %q) unauthorized, expected hop to %q",
					br.String(), fetchChain[1].String(), share.Target().String())
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
			lr := io.LimitReader(file, schema.MaxSchemaBlobSize)
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

	gethandler.ServeBlobRef(conn, req, blobRef, fetcher)
}

func (h *shareHandler) ServeHTTP(rw http.ResponseWriter, req *http.Request) {
	blobRef := blobref.Parse(req.Header.Get("X-PrefixHandler-PathSuffix"))
	if blobRef == nil {
		http.Error(rw, "Malformed share URL.", 400)
		return
	}
	handleGetViaSharing(rw, req, blobRef, h.fetcher)
}
