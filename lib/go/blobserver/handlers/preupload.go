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
	"fmt"
	"http"
	"log"
	"os"
)

func CreatePreUploadHandler(storage blobserver.Storage) func(http.ResponseWriter, *http.Request) {
	return func(conn http.ResponseWriter, req *http.Request) {
		handlePreUpload(conn, req, storage)
	}
}

const maxPreUploadBlobs = 1000

// TODO: for testing, tighten this interface to exactly the one method
// we need, rather than using all of blobserver.Storage
func handlePreUpload(conn http.ResponseWriter, req *http.Request, storage blobserver.Storage) {
	if !(req.Method == "POST" && req.URL.Path == "/camli/preupload") {
		httputil.BadRequestError(conn, "Inconfigured handler.")
		return
	}

	camliVersion := req.FormValue("camliversion")
	if camliVersion == "" {
		httputil.BadRequestError(conn, "No camliversion")
		return
	}

	n := 0
	toStat := make([]*blobref.BlobRef, 0)
	for {
		n++
		key := fmt.Sprintf("blob%v", n)
		value := req.FormValue(key)
		if value == "" {
			n--
			break
		}
		if n > maxPreUploadBlobs {
			httputil.BadRequestError(conn, "Too many preupload blob checks")
                        return
		}
		ref := blobref.Parse(value)
		if ref == nil {
			httputil.BadRequestError(conn, "Bogus blobref for key "+key)
			return
		}
		toStat = append(toStat, ref)
	}

	alreadyHave := make([]map[string]interface{}, 0)
	if n > 0 {
		blobch := make(chan *blobref.SizedBlobRef)
		resultch := make(chan os.Error, 1)
		go func() {
			err := storage.Stat(blobch, blobserver.Partition(""), toStat)
			close(blobch)
			resultch <- err
		}()

		for sb := range blobch {
			ah := make(map[string]interface{})
			ah["blobRef"] = sb.BlobRef.String()
			ah["size"] = sb.Size
			alreadyHave = append(alreadyHave, ah)
		}

		err := <-resultch
		if err != nil {
			log.Printf("Stat error: %v", err)
			conn.WriteHeader(http.StatusInternalServerError)
			return
		}
	}

	ret := commonUploadResponse(req)
	ret["alreadyHave"] = alreadyHave
	httputil.ReturnJson(conn, ret)
}
