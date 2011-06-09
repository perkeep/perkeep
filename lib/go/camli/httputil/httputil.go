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

package httputil

import (
	"fmt"
	"http"
	"json"
	"os"
	"log"
	"strings"
)

func ErrorRouting(conn http.ResponseWriter, req *http.Request) {
	http.Error(conn, "Handlers wired up wrong; this path shouldn't be hit", 500)
	log.Printf("Internal routing error on %q", req.URL.Path)
}

func BadRequestError(conn http.ResponseWriter, errorMessage string) {
	conn.WriteHeader(http.StatusBadRequest)
	log.Printf("Bad request: %s", errorMessage)
	fmt.Fprintf(conn, "%s\n", errorMessage)
}

func ServerError(conn http.ResponseWriter, err os.Error) {
	conn.WriteHeader(http.StatusInternalServerError)
	fmt.Fprintf(conn, "Server error: %s\n", err)
}

func ReturnJson(conn http.ResponseWriter, data interface{}) {
	conn.Header().Set("Content-Type", "text/javascript")

	if m, ok := data.(map[string]interface{}); ok {
		if t, ok := m["errorType"].(string); ok {
			switch t {
			case "server":
				conn.WriteHeader(http.StatusInternalServerError)
			case "input":
				conn.WriteHeader(http.StatusBadRequest)
			}
		}
	}

	bytes, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		BadRequestError(conn, fmt.Sprintf(
			"JSON serialization error: %v", err))
		return
	}
	conn.Write(bytes)
	conn.Write([]byte("\n"))
}

type PrefixHandler struct {
	Prefix  string
	Handler http.Handler
}

func (p *PrefixHandler) ServeHTTP(rw http.ResponseWriter, req *http.Request) {
	if !strings.HasPrefix(req.URL.Path, p.Prefix) {
		http.Error(rw, "Inconfigured PrefixHandler", 500)
		return
	}
	req.Header.Set("X-PrefixHandler-PathBase", p.Prefix)
	req.Header.Set("X-PrefixHandler-PathSuffix", req.URL.Path[len(p.Prefix):])
	p.Handler.ServeHTTP(rw, req)
}
