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

package server

import (
	"fmt"
	"io"
	"log"
	"net/http"

	"camlistore.org/pkg/blobref"
	"camlistore.org/pkg/blobserver"
	"camlistore.org/pkg/magic"
	"camlistore.org/pkg/schema"
)

type DownloadHandler struct {
	Fetcher   blobref.StreamingFetcher
	Cache     blobserver.Storage
	ForceMime string // optional
}

func (dh *DownloadHandler) storageSeekFetcher() blobref.SeekFetcher {
	return blobref.SeekerFromStreamingFetcher(dh.Fetcher) // TODO: pass dh.Cache?
}

func (dh *DownloadHandler) ServeHTTP(rw http.ResponseWriter, req *http.Request, file *blobref.BlobRef) {
	if req.Method != "GET" && req.Method != "HEAD" {
		http.Error(rw, "Invalid download method", 400)
		return
	}

	fr, err := schema.NewFileReader(dh.storageSeekFetcher(), file)
	if err != nil {
		http.Error(rw, "Can't serve file: "+err.Error(), 500)
		return
	}
	defer fr.Close()

	schema := fr.FileSchema()
	rw.Header().Set("Content-Length", fmt.Sprintf("%d", schema.SumPartsSize()))

	mimeType, reader := magic.MimeTypeFromReader(fr)
	if dh.ForceMime != "" {
		mimeType = dh.ForceMime
	}
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	rw.Header().Set("Content-Type", mimeType)
	if mimeType == "application/octet-stream" {
		// Chrome seems to silently do nothing on
		// application/octet-stream unless this is set.
		// Maybe it's confused by lack of URL it recognizes
		// along with lack of mime type?
		rw.Header().Set("Content-Disposition", "attachment; filename=file-"+file.String()+".dat")
	}

	if req.Method == "HEAD" {
		vbr := blobref.Parse(req.FormValue("verifycontents"))
		if vbr == nil {
			return
		}
		hash := vbr.Hash()
		if hash == nil {
			return
		}
		io.Copy(hash, reader) // ignore errors, caught later
		if vbr.HashMatches(hash) {
			rw.Header().Set("X-Camli-Contents", vbr.String())
		}
		return
	}

	n, err := io.Copy(rw, reader)
	log.Printf("For %q request of %s: copied %d, %v", req.Method, req.URL.Path, n, err)
	if err != nil {
		log.Printf("error serving download of file schema %s: %v", file, err)
		return
	}
	if size := schema.SumPartsSize(); n != int64(size) {
		log.Printf("error serving download of file schema %s: sent %d, expected size of %d",
			file, n, size)
		return
	}

}
