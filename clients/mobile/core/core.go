/*
Copyright 2026 The Perkeep Authors

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

// The core command is the Go half of the Perkeep mobile apps. It is built
// with -buildmode=c-archive (iOS) or -buildmode=c-shared (Android) and
// linked into the native app, which calls it through the functions in
// exports.go.
//
// Native code talks to the core with HTTP-style requests handled
// in-process by [handler], so new functionality means new endpoints, not
// new C functions.
package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"runtime"

	"perkeep.org/pkg/buildinfo"
)

func main() {}

var handler = newHandler()

func newHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /hello", serveHello)
	return mux
}

func serveHello(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"message":   "Hello from Perkeep's Go core",
		"goVersion": runtime.Version(),
		"goos":      runtime.GOOS,
		"goarch":    runtime.GOARCH,
		"build":     buildinfo.Summary(),
	})
}

// call runs an HTTP request against handler and returns the response
// status code and body.
func call(method, path string, body []byte) (status int, respBody []byte) {
	req, err := http.NewRequest(method, path, bytes.NewReader(body))
	if err != nil {
		return http.StatusBadRequest, []byte(err.Error())
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec.Code, rec.Body.Bytes()
}
