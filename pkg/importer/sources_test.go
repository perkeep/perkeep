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

package importer_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"

	"perkeep.org/internal/httputil"
	"perkeep.org/pkg/auth"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver"
	"perkeep.org/pkg/filesource"
	"perkeep.org/pkg/filesource/gdrive"
	"perkeep.org/pkg/importer"
	imptest "perkeep.org/pkg/importer/test"
)

const (
	testClientID     = "test-client-id.apps.googleusercontent.com"
	testClientSecret = "test-client-secret-1234"
	testRefreshToken = "test-refresh-token-5678"
)

// fakeGoogle serves Google's OAuth token endpoint and the parts of the
// Drive API that listing a Drive's top uses.
type fakeGoogle struct {
	t *testing.T

	mu          sync.Mutex
	challenge   string // the PKCE challenge the code was issued for
	redirectURI string // that the code was issued for
	revoked     bool   // whether the refresh token is revoked
	codes       int    // codes exchanged
}

// verified reports whether verifier is that of the PKCE challenge the
// code was issued for. g.mu must be held.
func (g *fakeGoogle) verified(verifier string) bool {
	sum := sha256.Sum256([]byte(verifier))
	return g.challenge != "" && base64.RawURLEncoding.EncodeToString(sum[:]) == g.challenge
}

func (g *fakeGoogle) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	defer g.mu.Unlock()
	switch r.URL.Path {
	case "/token":
		if r.FormValue("client_id") != testClientID && !strings.Contains(r.Header.Get("Authorization"), "Basic") {
			http.Error(w, "no client", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.FormValue("grant_type") {
		case "authorization_code":
			if r.FormValue("code") != "good-code" || !g.verified(r.FormValue("code_verifier")) || r.FormValue("redirect_uri") != g.redirectURI {
				w.WriteHeader(http.StatusBadRequest)
				io.WriteString(w, `{"error": "invalid_grant"}`)
				return
			}
			g.codes++
			json.NewEncoder(w).Encode(map[string]any{
				"access_token": "access-1", "token_type": "Bearer", "expires_in": 3600,
				"refresh_token": testRefreshToken, "scope": gdrive.Scope,
			})
		case "refresh_token":
			if g.revoked || r.FormValue("refresh_token") != testRefreshToken {
				w.WriteHeader(http.StatusBadRequest)
				io.WriteString(w, `{"error": "invalid_grant", "error_description": "Token has been expired or revoked."}`)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"access_token": "access-2", "token_type": "Bearer", "expires_in": 3600})
		}
	case "/drive/v3/files":
		if a := r.Header.Get("Authorization"); a != "Bearer access-2" {
			http.Error(w, "unauthorized "+a, http.StatusUnauthorized)
			return
		}
		// Two folders named Takeout, as Drive allows.
		io.WriteString(w, `{"files": [{"id": "t2", "name": "Takeout", "mimeType": "application/vnd.google-apps.folder"}, {"id": "n", "name": "Notes", "mimeType": "application/vnd.google-apps.folder"}, {"id": "t1", "name": "Takeout", "mimeType": "application/vnd.google-apps.folder"}]}`)
	default:
		http.NotFound(w, r)
	}
}

// toServer is a RoundTripper sending all requests to one server.
type toServer struct {
	base http.RoundTripper
	u    *url.URL
}

func (t toServer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.URL.Scheme, r.URL.Host, r.Host = t.u.Scheme, t.u.Host, t.u.Host
	return t.base.RoundTrip(r)
}

// sourcesHost is a Host for tests of file sources, whose HTTP requests
// all go to a fakeGoogle.
type sourcesHost struct {
	t *testing.T
	h *importer.Host
	g *fakeGoogle

	authorization string // if non-empty, the requests' Authorization header
}

func newSourcesHost(t *testing.T, hc importer.HostConfig) *sourcesHost {
	g := &fakeGoogle{t: t}
	ts := httptest.NewTestServer(t, g)
	c := ts.Client() // which sets ts.URL
	u, _ := url.Parse(ts.URL)
	hc.HTTPClient = &http.Client{Transport: toServer{c.Transport, u}}
	h := imptest.NewMemoryHost(t, hc)
	t.Cleanup(func() { h.Close() })
	return &sourcesHost{t: t, h: h, g: g}
}

// do makes a request of the page at suffix, after the importer prefix,
// with form as a POST body if non-nil.
func (s *sourcesHost) do(method, suffix string, form url.Values) *httptest.ResponseRecorder {
	s.t.Helper()
	var body io.Reader = http.NoBody
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	r := httptest.NewRequest(method, "/importer/"+suffix, body)
	if form != nil {
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if s.authorization != "" {
		r.Header.Set("Authorization", s.authorization)
	}
	path, _, _ := strings.Cut(suffix, "?")
	r.Header.Set(httputil.PathSuffixHeader, path)
	w := httptest.NewRecorder()
	s.h.ServeHTTP(w, r)
	return w
}

// add adds a file source with form, returning its ref.
func (s *sourcesHost) add(form url.Values) blob.Ref {
	s.t.Helper()
	w := s.do("POST", "sources/", form)
	if w.Code != http.StatusSeeOther {
		s.t.Fatalf("adding a source: %d %s", w.Code, w.Body)
	}
	ref, ok := blob.Parse(strings.TrimPrefix(w.Header().Get("Location"), "/importer/sources/"))
	if !ok {
		s.t.Fatalf("added source at %q", w.Header().Get("Location"))
	}
	return ref
}

// checkNoSecrets checks that no blob has any of the credentials.
func (s *sourcesHost) checkNoSecrets() {
	s.t.Helper()
	ctx := context.Background()
	src := s.h.BlobSource()
	err := blobserver.EnumerateAll(ctx, src.(blobserver.BlobEnumerator), func(sb blob.SizedRef) error {
		rc, _, err := src.Fetch(ctx, sb.Ref)
		if err != nil {
			return err
		}
		defer rc.Close()
		b, err := io.ReadAll(rc)
		if err != nil {
			return err
		}
		for _, secret := range []string{testClientID, testClientSecret, testRefreshToken} {
			if strings.Contains(string(b), secret) {
				s.t.Errorf("blob %v has %q", sb.Ref, secret)
			}
		}
		return nil
	})
	if err != nil {
		s.t.Fatal(err)
	}
}

func clientJSON(kind, tokenURL string) string {
	return fmt.Sprintf(`{%q: {"client_id": %q, "client_secret": %q, "auth_uri": "https://accounts.google.com/o/oauth2/auth", "token_uri": %q, "redirect_uris": ["http://localhost"]}}`,
		kind, testClientID, testClientSecret, tokenURL)
}

func TestLocalFileSource(t *testing.T) {
	s := newSourcesHost(t, importer.HostConfig{})
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "takeout-001.zip"), []byte("zip"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"relative/dir", filepath.Join(dir, "missing")} {
		if w := s.do("POST", "sources/", url.Values{"type": {"local"}, "dir": {bad}}); w.Code != http.StatusBadRequest {
			t.Errorf("adding directory %q: %d; want 400", bad, w.Code)
		}
	}
	ref := s.add(url.Values{"type": {"local"}, "dir": {dir + "/"}, "title": {"Takeout zips"}})

	w := s.do("GET", "sources/", nil)
	if !strings.Contains(w.Body.String(), "Takeout zips") {
		t.Errorf("sources page doesn't list the source:\n%s", w.Body)
	}
	w = s.do("POST", "sources/"+ref.String(), url.Values{"mode": {"test"}})
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "takeout-001.zip") {
		t.Errorf("testing the source: %d\n%s", w.Code, w.Body)
	}

	ctx := context.Background()
	src, err := s.h.FileSource(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	root, err := src.Root(ctx)
	if err != nil {
		t.Fatal(err)
	}
	files, err := src.List(ctx, root)
	if err != nil || len(files) != 1 {
		t.Fatalf("List = %+v, %v", files, err)
	}
	if b, err := filesource.ReadRange(ctx, src, files[0], 0, 3); err != nil || string(b) != "zip" {
		t.Errorf("ReadRange = %q, %v", b, err)
	}

	// Deleted, it's gone.
	if w := s.do("POST", "sources/"+ref.String(), url.Values{"mode": {"delete"}}); w.Code != http.StatusSeeOther {
		t.Fatalf("deleting: %d %s", w.Code, w.Body)
	}
	if w := s.do("GET", "sources/", nil); strings.Contains(w.Body.String(), "Takeout zips") {
		t.Error("sources page lists the deleted source")
	}
	if _, err := s.h.FileSource(ctx, ref); err == nil {
		t.Error("FileSource of a deleted source succeeded")
	}
}

var authLinkRx = regexp.MustCompile(`href="(https://accounts\.google\.com/[^"]+)"`)

func TestDesktopDriveFileSource(t *testing.T) {
	s := newSourcesHost(t, importer.HostConfig{})
	ctx := context.Background()
	if w := s.do("POST", "sources/", url.Values{"type": {"gdrive"}, "client": {"{}"}}); w.Code != http.StatusBadRequest {
		t.Errorf("adding a Drive without a client: %d; want 400", w.Code)
	}
	ref := s.add(url.Values{"type": {"gdrive"}, "client": {clientJSON("installed", "https://oauth2.googleapis.com/token")}})
	if _, err := s.h.FileSource(ctx, ref); !errors.Is(err, importer.ErrNeedsAuthorization) {
		t.Errorf("FileSource before authorizing: %v; want ErrNeedsAuthorization", err)
	}

	authorize := func() url.Values {
		t.Helper()
		w := s.do("POST", "sources/"+ref.String(), url.Values{"mode": {"authorize"}})
		m := authLinkRx.FindStringSubmatch(w.Body.String())
		if w.Code != http.StatusOK || m == nil {
			t.Fatalf("authorizing: %d\n%s", w.Code, w.Body)
		}
		u, err := url.Parse(html.UnescapeString(m[1]))
		if err != nil {
			t.Fatal(err)
		}
		q := u.Query()
		if q.Get("redirect_uri") != "http://127.0.0.1/" || q.Get("access_type") != "offline" || q.Get("code_challenge_method") != "S256" || q.Get("scope") != gdrive.Scope {
			t.Errorf("authorization URL %s", u)
		}
		s.g.mu.Lock()
		s.g.challenge, s.g.redirectURI = q.Get("code_challenge"), q.Get("redirect_uri")
		s.g.mu.Unlock()
		return q
	}
	paste := func(q url.Values) *httptest.ResponseRecorder {
		t.Helper()
		return s.do("POST", "sources/"+ref.String(), url.Values{"mode": {"paste"}, "url": {"http://127.0.0.1/?" + q.Encode()}})
	}

	q := authorize()
	if w := paste(url.Values{"state": {"src:" + ref.String() + ":" + strings.Repeat("0", 64)}, "code": {"good-code"}}); w.Code != http.StatusBadRequest {
		t.Errorf("pasting the wrong state: %d; want 400", w.Code)
	}
	if w := paste(url.Values{"state": {q.Get("state")}, "error": {"access_denied"}}); w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "access_denied") {
		t.Errorf("pasting a denial: %d %s", w.Code, w.Body)
	}
	// The denial used up the authorization.
	if w := paste(url.Values{"state": {q.Get("state")}, "code": {"good-code"}}); w.Code != http.StatusBadRequest {
		t.Errorf("pasting after the authorization was used: %d; want 400", w.Code)
	}

	q = authorize()
	pasted := url.Values{"state": {q.Get("state")}, "code": {"good-code"}, "scope": {gdrive.Scope}}
	if w := paste(pasted); w.Code != http.StatusSeeOther {
		t.Fatalf("pasting the authorization: %d %s", w.Code, w.Body)
	}
	if w := paste(pasted); w.Code != http.StatusBadRequest || s.g.exchanged() != 1 {
		t.Errorf("pasting the authorization again: %d, %d codes exchanged; want 400 and 1", w.Code, s.g.exchanged())
	}

	src, err := s.h.FileSource(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	root, _ := src.Root(ctx)
	files, err := src.List(ctx, root)
	if err != nil || len(files) != 3 || files[0].Name != "Takeout" {
		t.Errorf("List = %+v, %v", files, err)
	}

	s.g.mu.Lock()
	s.g.revoked = true
	s.g.mu.Unlock()
	src, err = s.h.FileSource(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := src.List(ctx, root); !errors.Is(err, importer.ErrNeedsAuthorization) {
		t.Errorf("List with a revoked token: %v; want ErrNeedsAuthorization", err)
	}
	s.checkNoSecrets()
}

// exchanged returns the number of codes exchanged for tokens.
func (g *fakeGoogle) exchanged() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.codes
}

func TestWebDriveFileSource(t *testing.T) {
	s := newSourcesHost(t, importer.HostConfig{})
	ref := s.add(url.Values{"type": {"gdrive"}, "client": {clientJSON("web", "https://oauth2.googleapis.com/token")}})
	const callback = "https://perkeep.example/importer/sources/callback"
	if w := s.do("GET", "sources/"+ref.String(), nil); !strings.Contains(w.Body.String(), callback) {
		t.Errorf("source page doesn't say where to allow redirects to:\n%s", w.Body)
	}
	w := s.do("POST", "sources/"+ref.String(), url.Values{"mode": {"authorize"}})
	u, err := url.Parse(w.Header().Get("Location"))
	if w.Code != http.StatusSeeOther || err != nil || u.Host != "accounts.google.com" {
		t.Fatalf("authorizing: %d, to %q", w.Code, w.Header().Get("Location"))
	}
	q := u.Query()
	if q.Get("redirect_uri") != callback {
		t.Errorf("redirect_uri = %q; want %q", q.Get("redirect_uri"), callback)
	}
	s.g.mu.Lock()
	s.g.challenge, s.g.redirectURI = q.Get("code_challenge"), callback
	s.g.mu.Unlock()

	bad := url.Values{"state": {"src:" + ref.String()}, "code": {"good-code"}}
	if w := s.do("GET", "sources/callback?"+bad.Encode(), nil); w.Code != http.StatusBadRequest {
		t.Errorf("callback with a bad state: %d; want 400", w.Code)
	}
	back := url.Values{"state": {q.Get("state")}, "code": {"good-code"}}
	if w := s.do("GET", "sources/callback?"+back.Encode(), nil); w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/importer/sources/"+ref.String() {
		t.Fatalf("callback: %d, to %q: %s", w.Code, w.Header().Get("Location"), w.Body)
	}
	if w := s.do("GET", "sources/", nil); strings.Contains(w.Body.String(), "Needs authorizing") {
		t.Errorf("source isn't authorized:\n%s", w.Body)
	}
	w = s.do("POST", "sources/"+ref.String(), url.Values{"mode": {"test"}})
	if !strings.Contains(w.Body.String(), "Takeout/") {
		t.Errorf("testing the source:\n%s", w.Body)
	}
	// By name, with the IDs of those of the same name only.
	want := `<li>Notes/</li><li>Takeout/ <span class="muted ref">t2</span></li><li>Takeout/ <span class="muted ref">t1</span></li>`
	if !strings.Contains(w.Body.String(), want) {
		t.Errorf("test listing doesn't have %s:\n%s", want, w.Body)
	}

	// A new client needs authorizing again.
	w = s.do("POST", "sources/"+ref.String(), url.Values{"mode": {"save"}, "client": {clientJSON("installed", "https://oauth2.googleapis.com/token")}})
	if w.Code != http.StatusSeeOther {
		t.Fatalf("saving a new client: %d %s", w.Code, w.Body)
	}
	if _, err := s.h.FileSource(context.Background(), ref); !errors.Is(err, importer.ErrNeedsAuthorization) {
		t.Errorf("FileSource with a new client: %v; want ErrNeedsAuthorization", err)
	}
	s.checkNoSecrets()
}

func TestFileSourcesRequireOwner(t *testing.T) {
	auth.SetMode(auth.NewBasicAuth("owner", "owner-password"))
	t.Cleanup(func() { auth.SetMode(auth.None{}) })
	s := newSourcesHost(t, importer.HostConfig{RequireOwnerForSetup: true})
	s.authorization = "Token " + auth.DiscoveryToken()
	for _, req := range []struct {
		method, suffix string
		form           url.Values
	}{
		{"GET", "sources/", nil},
		{"POST", "sources/", url.Values{"type": {"local"}, "dir": {t.TempDir()}}},
		{"GET", "sources/callback?state=x", nil},
	} {
		w := s.do(req.method, req.suffix, req.form)
		if w.Code != http.StatusForbidden || strings.Contains(w.Body.String(), "owner-password") {
			t.Errorf("%s %s by a non-owner: %d %s; want 403", req.method, req.suffix, w.Code, w.Body)
		}
	}
	s.authorization = "Basic " + base64.StdEncoding.EncodeToString([]byte("owner:owner-password"))
	if w := s.do("GET", "sources/", nil); w.Code != http.StatusOK {
		t.Errorf("GET sources/ by the owner: %d; want 200", w.Code)
	}
}
