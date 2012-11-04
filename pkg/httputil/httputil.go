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
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"

	"camlistore.org/pkg/auth"
)

func ErrorRouting(conn http.ResponseWriter, req *http.Request) {
	http.Error(conn, "Handlers wired up wrong; this path shouldn't be hit", 500)
	log.Printf("Internal routing error on %q", req.URL.Path)
}

func BadRequestError(conn http.ResponseWriter, errorMessage string, args ...interface{}) {
	conn.WriteHeader(http.StatusBadRequest)
	log.Printf("Bad request: %s", fmt.Sprintf(errorMessage, args...))
	fmt.Fprintf(conn, "%s\n", errorMessage)
}

func ForbiddenError(conn http.ResponseWriter, errorMessage string, args ...interface{}) {
	conn.WriteHeader(http.StatusForbidden)
	log.Printf("Forbidden: %s", fmt.Sprintf(errorMessage, args...))
	fmt.Fprintf(conn, "<h1>Forbidden</h1>")
}

func RequestEntityTooLargeError(conn http.ResponseWriter) {
	conn.WriteHeader(http.StatusRequestEntityTooLarge)
	fmt.Fprintf(conn, "<h1>Request entity is too large</h1>")
}

func ServerError(conn http.ResponseWriter, req *http.Request, err error) {
	conn.WriteHeader(http.StatusInternalServerError)
	if auth.LocalhostAuthorized(req) {
		fmt.Fprintf(conn, "Server error: %s\n", err)
		return
	}
	fmt.Fprintf(conn, "An internal error occured, sorry.")
}

func ReturnJSON(conn http.ResponseWriter, data interface{}) {
	conn.Header().Set("Content-Type", "text/javascript")

	if m, ok := data.(map[string]interface{}); ok {
		statusCode := 0
		if t, ok := m["error"].(string); ok && len(t) > 0 {
			statusCode = http.StatusInternalServerError
		}
		if t, ok := m["errorType"].(string); ok {
			switch t {
			case "server":
				statusCode = http.StatusInternalServerError
			case "input":
				statusCode = http.StatusBadRequest
			}
		}
		if statusCode != 0 {
			conn.WriteHeader(statusCode)
		}
	}

	bytes, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		BadRequestError(conn, fmt.Sprintf(
			"JSON serialization error: %v", err))
		return
	}

	conn.Header().Set("Content-Length", strconv.Itoa(len(bytes) + 1))
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

// BaseURL returns the base URL (scheme + host and optional port +
// blobserver prefix) that should be used for requests (and responses)
// subsequent to req. The returned URL does not end in a trailing slash.
// The scheme and host:port are taken from urlStr if present,
// or derived from req otherwise.
// The prefix part comes from urlStr.
func BaseURL(urlStr string, req *http.Request) (string, error) {
	var baseURL string
	defaultURL, err := url.Parse(urlStr)
	if err != nil {
		return baseURL, err
	}
	prefix := path.Clean(defaultURL.Path)
	scheme := "http"
	if req.TLS != nil {
		scheme = "https"
	}
	host := req.Host
	if defaultURL.Host != "" {
		host = defaultURL.Host
	}
	if defaultURL.Scheme != "" {
		scheme = defaultURL.Scheme
	}
	baseURL = scheme + "://" + host + prefix
	return baseURL, nil
}

// RequestTargetPort returns the port targetted by the client
// in req. If not present, it returns 80, or 443 if TLS is used.
func RequestTargetPort(req *http.Request) int {
	_, portStr, err := net.SplitHostPort(req.Host)
	if err == nil && portStr != "" {
		port, err := strconv.ParseInt(portStr, 0, 64)
		if err == nil {
			return int(port)
		}
	}
	if req.TLS != nil {
		return 443
	}
	return 80
}
