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

// Package httputil contains a bunch of HTTP utility code, some generic,
// and some Camlistore-specific.
package httputil

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"

	"camlistore.org/pkg/blob"
)

// IsGet reports whether r.Method is a GET or HEAD request.
func IsGet(r *http.Request) bool {
	return r.Method == "GET" || r.Method == "HEAD"
}

func ErrorRouting(conn http.ResponseWriter, req *http.Request) {
	http.Error(conn, "Handlers wired up wrong; this path shouldn't be hit", 500)
	log.Printf("Internal routing error on %q", req.URL.Path)
}

func BadRequestError(conn http.ResponseWriter, errorMessage string, args ...interface{}) {
	conn.WriteHeader(http.StatusBadRequest)
	log.Printf("Bad request: %s", fmt.Sprintf(errorMessage, args...))
	fmt.Fprintf(conn, "<h1>Bad Request</h1>")
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

func ServeError(conn http.ResponseWriter, req *http.Request, err error) {
	conn.WriteHeader(http.StatusInternalServerError)
	if IsLocalhost(req) || os.Getenv("CAMLI_DEV_CAMLI_ROOT") != "" {
		fmt.Fprintf(conn, "Server error: %s\n", err)
		return
	}
	fmt.Fprintf(conn, "An internal error occured, sorry.")
}

func ReturnJSON(rw http.ResponseWriter, data interface{}) {
	ReturnJSONCode(rw, 200, data)
}

func ReturnJSONCode(rw http.ResponseWriter, code int, data interface{}) {
	rw.Header().Set("Content-Type", "text/javascript")
	js, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		BadRequestError(rw, fmt.Sprintf("JSON serialization error: %v", err))
		return
	}
	rw.Header().Set("Content-Length", strconv.Itoa(len(js)+1))
	rw.WriteHeader(code)
	rw.Write(js)
	rw.Write([]byte("\n"))
}

// PrefixHandler wraps another Handler and verifies that all requests'
// Path begin with Prefix. If they don't, a 500 error is returned.
// If they do, the headers PathBaseHeader and PathSuffixHeader are set
// on the request before proxying to Handler.
// PathBaseHeader is just the value of Prefix.
// PathSuffixHeader is the part of the path that follows Prefix.
type PrefixHandler struct {
	Prefix  string
	Handler http.Handler
}

const (
	PathBaseHeader   = "X-Prefixhandler-Pathbase"
	PathSuffixHeader = "X-Prefixhandler-Pathsuffix"
)

func (p *PrefixHandler) ServeHTTP(rw http.ResponseWriter, req *http.Request) {
	if !strings.HasPrefix(req.URL.Path, p.Prefix) {
		http.Error(rw, "Inconfigured PrefixHandler", 500)
		return
	}
	req.Header.Set(PathBaseHeader, p.Prefix)
	req.Header.Set(PathSuffixHeader, strings.TrimPrefix(req.URL.Path, p.Prefix))
	p.Handler.ServeHTTP(rw, req)
}

// PathBase returns a Request's base path, if it went via a PrefixHandler.
func PathBase(req *http.Request) string { return req.Header.Get(PathBaseHeader) }

// PathSuffix returns a Request's suffix path, if it went via a PrefixHandler.
func PathSuffix(req *http.Request) string { return req.Header.Get(PathSuffixHeader) }

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

// Recover is meant to be used at the top of handlers with "defer"
// to catch errors from MustGet, etc:
//
//   func handler(rw http.ResponseWriter, req *http.Request) {
//       defer httputil.Recover(rw, req)
//       id := req.MustGet("id")
//       ....
//
// Recover will send the proper HTTP error type and message (e.g.
// a 400 Bad Request for MustGet)
func Recover(rw http.ResponseWriter, req *http.Request) {
	RecoverJSON(rw, req) // TODO: for now. alternate format?
}

// RecoverJSON is like Recover but returns with a JSON response.
func RecoverJSON(rw http.ResponseWriter, req *http.Request) {
	e := recover()
	if e == nil {
		return
	}
	ServeJSONError(rw, e)
}

type httpCoder interface {
	HTTPCode() int
}

// An InvalidMethodError is returned when an HTTP handler is invoked
// with an unsupported method.
type InvalidMethodError struct{}

func (InvalidMethodError) Error() string { return "invalid method" }
func (InvalidMethodError) HTTPCode() int { return http.StatusMethodNotAllowed }

// A MissingParameterError represents a missing HTTP parameter.
// The underlying string is the missing parameter name.
type MissingParameterError string

func (p MissingParameterError) Error() string { return fmt.Sprintf("Missing parameter %q", string(p)) }
func (MissingParameterError) HTTPCode() int   { return http.StatusBadRequest }

// An InvalidParameterError represents an invalid HTTP parameter.
// The underlying string is the invalid parameter name, not value.
type InvalidParameterError string

func (p InvalidParameterError) Error() string { return fmt.Sprintf("Invalid parameter %q", string(p)) }
func (InvalidParameterError) HTTPCode() int   { return http.StatusBadRequest }

// A ServerError is a generic 500 error.
type ServerError string

func (e ServerError) Error() string { return string(e) }
func (ServerError) HTTPCode() int   { return http.StatusInternalServerError }

// MustGet returns a non-empty GET (or HEAD) parameter param and panics
// with a special error as caught by a deferred httputil.Recover.
func MustGet(req *http.Request, param string) string {
	if !IsGet(req) {
		panic(InvalidMethodError{})
	}
	v := req.FormValue(param)
	if v == "" {
		panic(MissingParameterError(param))
	}
	return v
}

// MustGetBlobRef returns a non-nil BlobRef from req, as given by param.
// If it doesn't, it panics with a value understood by Recover or RecoverJSON.
func MustGetBlobRef(req *http.Request, param string) blob.Ref {
	br, ok := blob.Parse(MustGet(req, param))
	if !ok {
		panic(InvalidParameterError(param))
	}
	return br
}

// OptionalInt returns the integer in req given by param, or 0 if not present.
// If the form value is not an integer, it panics with a a value understood by Recover or RecoverJSON.
func OptionalInt(req *http.Request, param string) int {
	v := req.FormValue(param)
	if v == "" {
		return 0
	}
	i, err := strconv.Atoi(v)
	if err != nil {
		panic(InvalidParameterError(param))
	}
	return i
}

// ServeJSONError sends a JSON error response to rw for the provided
// error value.
func ServeJSONError(rw http.ResponseWriter, err interface{}) {
	code := 500
	if i, ok := err.(httpCoder); ok {
		code = i.HTTPCode()
	}
	msg := fmt.Sprint(err)
	log.Printf("Sending error %v to client for: %v", code, msg)
	ReturnJSONCode(rw, code, map[string]interface{}{
		"error":     msg,
		"errorType": http.StatusText(code),
	})
}

// TODO: use a sync.Pool if/when Go 1.3 includes it and Camlistore depends on that.
var freeBuf = make(chan *bytes.Buffer, 2)

func getBuf() *bytes.Buffer {
	select {
	case b := <-freeBuf:
		b.Reset()
		return b
	default:
		return new(bytes.Buffer)
	}
}

func putBuf(b *bytes.Buffer) {
	select {
	case freeBuf <- b:
	default:
	}
}

// DecodeJSON decodes the JSON in res.Body into dest and then closes
// res.Body.
// It defensively caps the JSON at 8 MB for now.
func DecodeJSON(res *http.Response, dest interface{}) error {
	defer CloseBody(res.Body)
	buf := getBuf()
	defer putBuf(buf)
	if err := json.NewDecoder(io.TeeReader(io.LimitReader(res.Body, 8<<20), buf)).Decode(dest); err != nil {
		return fmt.Errorf("httputil.DecodeJSON: %v, on input: %s", err, buf.Bytes())
	}
	return nil
}

// CloseBody should be used to close an http.Response.Body.
//
// It does a final little Read to maybe see EOF (to trigger connection
// re-use) before calling Close.
func CloseBody(rc io.ReadCloser) {
	// Go 1.2 pseudo-bug: the NewDecoder(res.Body).Decode never
	// sees an EOF, so we have to do this 0-byte copy here to
	// force the http Transport to see its own EOF and recycle the
	// connection. In Go 1.1 at least, the Close would cause it to
	// read to EOF and recycle the connection, but in Go 1.2, a
	// Close before EOF kills the underlying TCP connection.
	//
	// Will hopefully be fixed in Go 1.3, at least for bodies with
	// Content-Length.  Or maybe Go 1.3's Close itself would look
	// to see if we're at EOF even if it hasn't been Read.

	// TODO: use a bytepool package somewhere for this byte?
	// Justification for 3 byte reads: two for up to "\r\n" after
	// a JSON/XML document, and then 1 to see EOF if we haven't yet.
	buf := make([]byte, 1)
	for i := 0; i < 3; i++ {
		_, err := rc.Read(buf)
		if err != nil {
			break
		}
	}
	rc.Close()
}

func IsWebsocketUpgrade(req *http.Request) bool {
	return req.Method == "GET" && req.Header.Get("Upgrade") == "websocket"
}
