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
// in-process by a [server], so new functionality means new endpoints, not
// new C functions. Requests and responses are JSON. Errors have a non-2xx
// status and a body of {"error": "message"}.
//
// The native app must first call POST /init with {"dataDir": dir}, the
// directory where the core keeps its state.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"runtime"
	"slices"
	"sync"

	"perkeep.org/pkg/buildinfo"
)

func main() {}

var theServer = newServer()

// server is the core's state and HTTP handler.
type server struct {
	mux *http.ServeMux

	mu       sync.Mutex
	dataDir  string       // empty until initialized
	accounts *accountList // nil until initialized
}

func newServer() *server {
	s := &server{mux: http.NewServeMux()}
	s.mux.HandleFunc("GET /hello", s.serveHello)
	s.mux.HandleFunc("POST /init", s.serveInit)
	s.mux.HandleFunc("GET /accounts", s.withAccounts(s.serveAccounts))
	s.mux.HandleFunc("POST /accounts", s.withAccounts(s.serveAddAccount))
	s.mux.HandleFunc("PUT /accounts/{id}", s.withAccount(s.serveUpdateAccount))
	s.mux.HandleFunc("DELETE /accounts/{id}", s.withAccount(s.serveDeleteAccount))
	s.mux.HandleFunc("POST /accounts/{id}/select", s.withAccount(s.serveSelectAccount))
	s.mux.HandleFunc("POST /accounts/{id}/albumstates", s.withAccount(s.serveAlbumStates))
	s.mux.HandleFunc("PUT /accounts/{id}/albums", s.withAccount(s.serveSetAlbum))
	return s
}

func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

// httpError is an error with an HTTP status code.
type httpError struct {
	code int
	msg  string
}

func (e httpError) Error() string { return e.msg }

func errorf(code int, msg string) error { return httpError{code, msg} }

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, err error) {
	code := http.StatusInternalServerError
	if he, ok := errors.AsType[httpError](err); ok {
		code = he.code
	}
	writeJSON(w, code, map[string]string{"error": err.Error()})
}

func readJSON(r *http.Request, v any) error {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		return errorf(http.StatusBadRequest, "bad request body: "+err.Error())
	}
	return nil
}

func (s *server) serveHello(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"message":   "Hello from Perkeep's Go core",
		"goVersion": runtime.Version(),
		"goos":      runtime.GOOS,
		"goarch":    runtime.GOARCH,
		"build":     buildinfo.Summary(),
	})
}

func (s *server) serveInit(w http.ResponseWriter, r *http.Request) {
	var req struct {
		DataDir string `json:"dataDir"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, err)
		return
	}
	if req.DataDir == "" {
		writeError(w, errorf(http.StatusBadRequest, "dataDir is required"))
		return
	}
	if err := os.MkdirAll(req.DataDir, 0o700); err != nil {
		writeError(w, err)
		return
	}
	accounts, err := loadAccounts(req.DataDir)
	if err != nil {
		writeError(w, err)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dataDir = req.DataDir
	s.accounts = accounts
	w.WriteHeader(http.StatusNoContent)
}

// withAccounts wraps a handler that uses the account list. It holds s.mu
// while fn runs, and if fn returns an error, writes it as the response.
func (s *server) withAccounts(fn func(w http.ResponseWriter, r *http.Request) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.accounts == nil {
			writeError(w, errorf(http.StatusServiceUnavailable, "core not initialized"))
			return
		}
		if err := fn(w, r); err != nil {
			writeError(w, err)
		}
	}
}

// withAccount is like withAccounts for handlers of a single account,
// identified by the {id} path parameter.
func (s *server) withAccount(fn func(w http.ResponseWriter, r *http.Request, a *Account) error) http.HandlerFunc {
	return s.withAccounts(func(w http.ResponseWriter, r *http.Request) error {
		a := s.accounts.find(r.PathValue("id"))
		if a == nil {
			return errorf(http.StatusNotFound, "no such account")
		}
		return fn(w, r, a)
	})
}

func (s *server) serveAccounts(w http.ResponseWriter, r *http.Request) error {
	writeJSON(w, http.StatusOK, s.accounts)
	return nil
}

// accountFields are the user-editable account settings, other than
// albums.
type accountFields struct {
	Name     string `json:"name"`
	Server   string `json:"server"`
	Username string `json:"username"`
}

func (f accountFields) apply(a *Account) error {
	server, err := normalizeServer(f.Server)
	if err != nil {
		return errorf(http.StatusBadRequest, err.Error())
	}
	a.Server = server
	a.Username = f.Username
	a.Name = f.Name
	if a.Name == "" {
		u, _ := url.Parse(server)
		a.Name = u.Host
	}
	return nil
}

func (s *server) serveAddAccount(w http.ResponseWriter, r *http.Request) error {
	var f accountFields
	if err := readJSON(r, &f); err != nil {
		return err
	}
	a := &Account{ID: newAccountID()}
	a.Include, a.Exclude = newAccountAlbums()
	if err := f.apply(a); err != nil {
		return err
	}
	s.accounts.Accounts = append(s.accounts.Accounts, a)
	if s.accounts.Current == "" {
		s.accounts.Current = a.ID
	}
	if err := s.accounts.save(s.dataDir); err != nil {
		return err
	}
	writeJSON(w, http.StatusCreated, a)
	return nil
}

func (s *server) serveUpdateAccount(w http.ResponseWriter, r *http.Request, a *Account) error {
	var f accountFields
	if err := readJSON(r, &f); err != nil {
		return err
	}
	updated := *a
	if err := f.apply(&updated); err != nil {
		return err
	}
	*a = updated
	if err := s.accounts.save(s.dataDir); err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, a)
	return nil
}

func (s *server) serveDeleteAccount(w http.ResponseWriter, r *http.Request, a *Account) error {
	l := s.accounts
	l.Accounts = slices.DeleteFunc(l.Accounts, func(x *Account) bool { return x == a })
	if l.Current == a.ID {
		l.Current = ""
		if len(l.Accounts) > 0 {
			l.Current = l.Accounts[0].ID
		}
	}
	if err := l.save(s.dataDir); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *server) serveSelectAccount(w http.ResponseWriter, r *http.Request, a *Account) error {
	s.accounts.Current = a.ID
	if err := s.accounts.save(s.dataDir); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// serveAlbumStates reports whether each of the device's albums (in the
// request) is backed up, plus the account's album references that match
// none of them, such as the default WhatsApp exclusion on a device without
// WhatsApp.
func (s *server) serveAlbumStates(w http.ResponseWriter, r *http.Request, a *Account) error {
	var req struct {
		Albums []Album `json:"albums"`
	}
	if err := readJSON(r, &req); err != nil {
		return err
	}
	type entry struct {
		Album Album      `json:"album"`
		State AlbumState `json:"state"`
	}
	var res struct {
		Albums    []entry `json:"albums"`
		Unmatched struct {
			Include []Album `json:"include"`
			Exclude []Album `json:"exclude"`
		} `json:"unmatched"`
	}
	res.Albums = []entry{}
	for _, al := range req.Albums {
		res.Albums = append(res.Albums, entry{al, a.albumState(al)})
	}
	res.Unmatched.Include = unmatched(a.Include, req.Albums)
	res.Unmatched.Exclude = unmatched(a.Exclude, req.Albums)
	writeJSON(w, http.StatusOK, res)
	return nil
}

func (s *server) serveSetAlbum(w http.ResponseWriter, r *http.Request, a *Account) error {
	var req struct {
		Album Album      `json:"album"`
		State AlbumState `json:"state"`
	}
	if err := readJSON(r, &req); err != nil {
		return err
	}
	updated := *a
	updated.Include = slices.Clone(a.Include)
	updated.Exclude = slices.Clone(a.Exclude)
	if err := updated.setAlbumState(req.Album, req.State); err != nil {
		return errorf(http.StatusBadRequest, err.Error())
	}
	*a = updated
	if err := s.accounts.save(s.dataDir); err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, a)
	return nil
}

// call runs an HTTP request against theServer and returns the response
// status code and body.
func call(method, path string, body []byte) (status int, respBody []byte) {
	req, err := http.NewRequest(method, path, bytes.NewReader(body))
	if err != nil {
		return http.StatusBadRequest, []byte(err.Error())
	}
	rec := httptest.NewRecorder()
	theServer.ServeHTTP(rec, req)
	return rec.Code, rec.Body.Bytes()
}
