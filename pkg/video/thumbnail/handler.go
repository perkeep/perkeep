/*
Copyright 2014 The Camlistore Authors

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

package thumbnail

import (
	"log"
	"net/http"
	"strings"
	"time"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/httputil"
	"camlistore.org/pkg/schema"
)

// serveRef gets the file at ref from fetcher and serves its contents.
// It is used by Service as a one time handler to serve to the thumbnail child process on localhost.
func serveRef(rw http.ResponseWriter, req *http.Request, ref blob.Ref, fetcher blob.Fetcher) {

	if !httputil.IsGet(req) {
		http.Error(rw, "Invalid download method.", 400)
		return
	}

	if !httputil.IsLocalhost(req) {
		http.Error(rw, "Forbidden.", 403)
		return
	}

	parts := strings.Split(req.URL.Path, "/")
	if len(parts) < 2 {
		http.Error(rw, "Malformed GET URL.", 400)
		return
	}

	blobRef, ok := blob.Parse(parts[1])
	if !ok {
		http.Error(rw, "Malformed GET URL.", 400)
		return
	}

	// only serves its ref
	if blobRef != ref {
		log.Printf("videothumbnail: access to %v forbidden; wrong blobref for handler", blobRef)
		http.Error(rw, "Forbidden.", 403)
		return
	}

	rw.Header().Set("Content-Type", "application/octet-stream")

	fr, err := schema.NewFileReader(fetcher, ref)
	if err != nil {
		httputil.ServeError(rw, req, err)
		return
	}
	defer fr.Close()

	http.ServeContent(rw, req, "", time.Now(), fr)
}

func createVideothumbnailHandler(ref blob.Ref, fetcher blob.Fetcher) http.Handler {
	return http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		serveRef(rw, req, ref, fetcher)
	})
}
