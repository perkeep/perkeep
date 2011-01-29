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

package main

import (
	"camli/blobref"
	"camli/httputil"
	"container/vector"
	"fmt"
	"http"
	"os"
)

func handlePreUpload(conn http.ResponseWriter, req *http.Request) {
	if !(req.Method == "POST" && req.URL.Path == "/camli/preupload") {
		httputil.BadRequestError(conn, "Inconfigured handler.")
		return
	}

	req.ParseForm()
	camliVersion := req.FormValue("camliversion")
	if camliVersion == "" {
		httputil.BadRequestError(conn, "No camliversion")
		return
	}
	n := 0
	haveVector := new(vector.Vector)

	haveChan := make(chan *map[string]interface{})
	for {
		key := fmt.Sprintf("blob%v", n+1)
		value := req.FormValue(key)
		if value == "" {
			break
		}
		ref := blobref.Parse(value)
		if ref == nil {
			httputil.BadRequestError(conn, "Bogus blobref for key "+key)
			return
		}
		if !ref.IsSupported() {
			httputil.BadRequestError(conn, "Unsupported or bogus blobref "+key)
		}
		n++

		// Parallel stat all the files...
		go func() {
			fi, err := os.Stat(BlobFileName(ref))
			if err == nil && fi.IsRegular() {
				info := make(map[string]interface{})
				info["blobRef"] = ref.String()
				info["size"] = fi.Size
				haveChan <- &info
			} else {
				haveChan <- nil
			}
		}()
	}

	if n > 0 {
		for have := range haveChan {
			if have != nil {
				haveVector.Push(have)
			}
			n--
			if n == 0 {
				break
			}
		}
	}

	ret := commonUploadResponse(req)
	ret["alreadyHave"] = haveVector.Copy()
	httputil.ReturnJson(conn, ret)
}
