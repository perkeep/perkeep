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
	"camlistore.org/pkg/httputil"
	"camlistore.org/pkg/jsonsign"
	"fmt"
	"net/http"
)

const kMaxJsonLength = 1024 * 1024

func handleSign(conn http.ResponseWriter, req *http.Request) {
	if !(req.Method == "POST" && req.URL.Path == "/camli/sig/sign") {
		httputil.BadRequestError(conn, "Inconfigured handler.")
		return
	}

	req.ParseForm()

	jsonStr := req.FormValue("json")
	if jsonStr == "" {
		httputil.BadRequestError(conn, "Missing json parameter")
		return
	}
	if len(jsonStr) > kMaxJsonLength {
		httputil.BadRequestError(conn, "json parameter too large")
		return
	}

	sreq := &jsonsign.SignRequest{UnsignedJSON: jsonStr, Fetcher: pubKeyFetcher}
	signedJson, err := sreq.Sign()
	if err != nil {
		// TODO: some aren't really a "bad request"
		httputil.BadRequestError(conn, fmt.Sprintf("%v", err))
		return
	}
	conn.Write([]byte(signedJson))
}
