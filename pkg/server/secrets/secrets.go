/*
Copyright 2026 The Perkeep Authors.

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

// Package secrets serves the owner-only, non-blob secret store API.
package secrets

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"go4.org/jsonconfig"
	"perkeep.org/internal/osutil"
	"perkeep.org/pkg/auth"
	"perkeep.org/pkg/blobserver"
	"perkeep.org/pkg/secretstore"
)

// Handler serves secret metadata and explicitly requested values. It performs
// its own per-method authorization; it must not be wrapped with ordinary OpAll.
type Handler struct {
	store     *secretstore.Store
	prefix    string
	baseURL   func() string
	ownsStore bool
}

// New returns a handler mounted at /secrets/. The caller owns store.
func New(store *secretstore.Store) *Handler {
	return &Handler{store: store, prefix: "/secrets/"}
}

// SecretStore returns the store for trusted in-process consumers.
func (h *Handler) SecretStore() *secretstore.Store { return h.store }

// Close closes a store opened by the configuration constructor, but not a
// caller-owned store passed to New.
func (h *Handler) Close() error {
	if h.ownsStore {
		return h.store.Close()
	}
	return nil
}

func init() { blobserver.RegisterHandlerConstructor("secrets", newFromConfig) }

func newFromConfig(ld blobserver.Loader, conf jsonconfig.Obj) (http.Handler, error) {
	file := conf.OptionalString("file", "")
	memory := conf.OptionalBool("memory", false)
	if err := conf.Validate(); err != nil {
		return nil, err
	}
	if memory && file != "" {
		return nil, errors.New("secrets file and memory are mutually exclusive")
	}
	var store *secretstore.Store
	var err error
	if memory {
		store, err = secretstore.NewMemory()
	} else {
		if file == "" {
			dir, dirErr := osutil.CamliVarDir()
			if dirErr != nil {
				return nil, errors.New("could not locate secret store directory")
			}
			file = filepath.Join(dir, "secrets.sqlite")
		}
		store, err = secretstore.Open(file)
	}
	if err != nil {
		return nil, errors.New("could not open secret store")
	}
	// BaseURL can block on listener startup: retain the method without calling it.
	return &Handler{store: store, prefix: ld.MyPrefix(), baseURL: ld.BaseURL, ownsStore: true}, nil
}

// maxRequestSize permits the worst-case JSON escaping of a MaxValueSize value.
const maxRequestSize = 6*secretstore.MaxValueSize + 4096

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Type", "application/json")
	if !h.sameOrigin(r) {
		fail(w, http.StatusForbidden, "cross-origin request forbidden")
		return
	}
	op := auth.OpSecretRead
	switch r.Method {
	case http.MethodGet:
	case http.MethodPut, http.MethodDelete:
		op = auth.OpSecretWrite
	default:
		w.Header().Set("Allow", "GET, PUT, DELETE")
		fail(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !auth.AllowedUnscoped(r, op) {
		// Do not use SendUnauthorized: development auth may disclose its password.
		w.Header().Set("WWW-Authenticate", `Basic realm="Perkeep secrets"`)
		fail(w, http.StatusUnauthorized, "secret access denied")
		return
	}
	if r.URL.Path == h.prefix && r.Method == http.MethodGet {
		if r.URL.RawQuery != "" {
			fail(w, 400, "unexpected query")
			return
		}
		entries, err := h.store.List(r.Context())
		if err != nil {
			storeError(w, err)
			return
		}
		if entries == nil {
			entries = []secretstore.Metadata{}
		}
		// List returns metadata, never Entry (which includes Value).
		writeJSON(w, entries)
		return
	}
	if r.URL.Path != h.prefix+"value" {
		fail(w, 404, "not found")
		return
	}
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || len(r.URL.RawQuery) > 4096 || len(q) != 1 || len(q["name"]) != 1 || q.Get("name") == "" {
		fail(w, 400, "one name is required")
		return
	}
	name := q.Get("name")
	if r.Method == http.MethodGet {
		entry, err := h.store.Get(r.Context(), name)
		if err != nil {
			storeError(w, err)
			return
		}
		writeJSON(w, entry)
		return
	}
	ct, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || ct != "application/json" {
		fail(w, 415, "application/json required")
		return
	}
	if r.Method == http.MethodDelete {
		var body struct {
			ExpectedRevision *int64 `json:"expectedRevision"`
		}
		if !readJSON(w, r, &body) || !validRevision(w, body.ExpectedRevision) {
			return
		}
		if err := h.store.Delete(r.Context(), name, *body.ExpectedRevision); err != nil {
			storeError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	// A pointer distinguishes an explicit create-if-absent (0) from a missing CAS.
	var body struct {
		Value            *string `json:"value"`
		ExpectedRevision *int64  `json:"expectedRevision"`
		ClearMigration   bool    `json:"clearMigration,omitempty"`
	}
	if !readJSON(w, r, &body) || !validRevision(w, body.ExpectedRevision) {
		return
	}
	if body.Value == nil {
		fail(w, 400, "value required")
		return
	}

	meta, err := h.store.Put(r.Context(), name, *body.Value, secretstore.PutOptions{ExpectedRevision: *body.ExpectedRevision, ClearMigration: body.ClearMigration})
	if err != nil {
		storeError(w, err)
		return
	}
	writeJSON(w, meta)
}

// readJSON accepts exactly one object and rejects unknown fields without ever
// reflecting parser errors, which may contain plaintext input.
func readJSON(w http.ResponseWriter, r *http.Request, out any) bool {
	if r.ContentLength > maxRequestSize {
		fail(w, 413, "request too large")
		return false
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxRequestSize))
	if err != nil {
		requestError(w, err)
		return false
	}
	// encoding/json otherwise silently replaces malformed UTF-8 bytes.
	if !utf8.Valid(data) {
		fail(w, 400, "invalid request")
		return false
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		requestError(w, err)
		return false
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		requestError(w, err)
		return false
	}
	return true
}

func requestError(w http.ResponseWriter, err error) {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		fail(w, 413, "request too large")
	} else {
		fail(w, 400, "invalid request")
	}
}

func validRevision(w http.ResponseWriter, revision *int64) bool {
	if revision == nil || *revision < secretstore.AnyRevision {
		fail(w, 400, "expectedRevision required")
		return false
	}
	return true
}

func (h *Handler) sameOrigin(r *http.Request) bool {
	if strings.EqualFold(r.Header.Get("Sec-Fetch-Site"), "cross-site") {
		return false
	}
	origins := r.Header.Values("Origin")
	if len(origins) == 0 {
		return true
	} // Non-browser clients normally omit Origin.
	if len(origins) != 1 {
		return false
	}
	origin, err := url.Parse(origins[0])
	if err != nil || origin.Host == "" || origin.User != nil || origin.RawQuery != "" || origin.Fragment != "" || origin.Path != "" {
		return false
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if origin.Scheme == scheme && strings.EqualFold(origin.Host, r.Host) {
		return true
	}
	// Only the configured base URL is trusted behind a reverse proxy. Never
	// derive the public origin from attacker-controlled forwarding headers.
	if h.baseURL == nil {
		return false
	}
	base, err := url.Parse(h.baseURL())
	return err == nil && base.Host != "" && origin.Scheme == base.Scheme && strings.EqualFold(origin.Host, base.Host)
}

func storeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, secretstore.ErrNotFound):
		fail(w, 404, "secret not found")
	case errors.Is(err, secretstore.ErrConflict):
		fail(w, 409, "revision conflict")
	case errors.Is(err, secretstore.ErrInvalidName):
		fail(w, 400, "invalid secret name")
	case errors.Is(err, secretstore.ErrInvalidValue):
		fail(w, 400, "invalid secret value")
	case errors.Is(err, secretstore.ErrTooLarge):
		fail(w, 413, "request too large")
	default:
		fail(w, 500, "secret store unavailable")
	}
}

func fail(w http.ResponseWriter, status int, message string) {
	w.WriteHeader(status)
	writeJSON(w, struct {
		Error string `json:"error"`
	}{message})
}

func writeJSON(w http.ResponseWriter, v any) { json.NewEncoder(w).Encode(v) }
