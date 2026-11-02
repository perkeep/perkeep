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

package access

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/tailscale/tailcat"
	"golang.org/x/net/xsrftoken"
	"perkeep.org/internal/httputil"
	"perkeep.org/pkg/auth"
	"perkeep.org/pkg/health"
	"tailscale.com/derp/derpserver"
	"tailscale.com/tailcfg"
	"tailscale.com/types/key"
	"tailscale.com/types/logger"
)

var testRoots = Roots{
	Blob:   "/bs/",
	Search: "/search/",
	Sign:   "/sighelper/",
	UI:     "/ui/",
}

const testRef = "sha224-d14a028c2a3a2bc9476102bb288234c415a2b01f828ea62ac5b3e42f"

func TestNeed(t *testing.T) {
	const none Perm = 0
	tests := []struct {
		method, url string
		accept      string
		want        Perm
		wantOK      bool
	}{
		{"GET", "/?camli.mode=config", "", none, true},
		{"GET", "/", "text/x-camli-configuration", none, true},
		{"GET", "/", "", none, false},
		{"POST", "/?camli.mode=config", "", none, false},

		{"POST", "/bs/camli/upload", "", PermUpload, true},
		{"PUT", "/bs/camli/" + testRef, "", PermUpload, true},
		{"POST", "/bs/camli/session", "", PermUpload, true},
		{"POST", "/bs/camli/session/abc/flush", "", PermUpload, true},
		{"DELETE", "/bs/camli/session/abc", "", PermUpload, true},
		{"POST", "/bs/camli/stat", "", PermUpload | PermDownload, true},
		{"GET", "/bs/camli/stat?blob1=" + testRef, "", PermUpload | PermDownload, true},
		{"GET", "/bs/camli/" + testRef, "", PermDownload, true},
		{"HEAD", "/bs/camli/" + testRef, "", PermDownload, true},
		{"GET", "/bs/camli/enumerate-blobs", "", PermDownload, true},
		{"POST", "/bs/camli/remove", "", none, false},
		{"GET", "/bs/camli/ws", "", none, false},
		{"POST", "/bs/camli/" + testRef, "", none, false},
		{"GET", "/bs/", "", none, false},

		{"GET", "/search/camli/search/describe?blobref=" + testRef, "", PermSearch, true},
		{"POST", "/search/camli/search/query", "", PermSearch, true},
		{"GET", "/search/camli/search/contentpermanodes?wholedigest=" + testRef, "", PermUpload | PermSign | PermSearch, true},
		{"POST", "/search/camli/search/contentpermanodes", "", PermUpload | PermSign | PermSearch, true},
		{"PUT", "/search/camli/search/contentpermanodes", "", none, false},
		{"GET", "/search/camli/search/ws", "", none, false},
		{"GET", "/search/camli/search/query", "", none, false},

		{"POST", "/sighelper/camli/sig/sign", "", PermSign, true},
		{"GET", "/sighelper/camli/sig/discovery", "", PermSign, true},
		{"POST", "/sighelper/other", "", none, false},

		{"GET", "/status/status.json", "", none, false}, // stats are in discovery
		{"GET", "/status/", "", none, false},
		{"POST", "/status/restart", "", none, false},

		{"GET", "/ui/thumbnail/" + testRef + "/x.jpg?mw=100", "", PermDownload, true},
		{"GET", "/ui/download/" + testRef + "/x.jpg", "", PermDownload, true},
		{"GET", "/ui/download/x?camli.mode=config", "", none, false},
		{"GET", "/ui/", "", none, false},
		{"GET", "/ui/index.html", "", none, false},

		{"GET", "/access/", "", none, false},
		{"GET", "/debug/config", "", none, false},
		{"GET", "/importer/", "", none, false},
		{"GET", "/bs/camli/../../debug/config", "", none, false},
		{"GET", "/bs//camli/" + testRef, "", none, false},
		{"GET", "/ui/download/./x", "", none, false},
	}
	for _, tt := range tests {
		r := httptest.NewRequest(tt.method, tt.url, nil)
		if tt.accept != "" {
			r.Header.Set("Accept", tt.accept)
		}
		// httptest.NewRequest leaves dot segments as they are, like
		// requests off the wire.
		got, ok := testRoots.need(r)
		if got != tt.want || ok != tt.wantOK {
			t.Errorf("%s %s = %v, %v; want %v, %v", tt.method, tt.url, got, ok, tt.want, tt.wantOK)
		}
	}
}

// grantRecorder is a handler that records the grant of the requests it
// serves.
type grantRecorder struct {
	mu     sync.Mutex
	served []string
}

func (g *grantRecorder) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	granted, ok := auth.GrantCtxKey.ValueOk(r.Context())
	if !ok || granted != auth.OpAll {
		http.Error(w, "missing grant", http.StatusInternalServerError)
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.served = append(g.served, r.Method+" "+r.URL.Path)
	io.WriteString(w, "ok")
}

func newTestHandler(t *testing.T, serve http.Handler) *Handler {
	t.Helper()
	h, err := New(Config{
		Prefix:    "/access/",
		StateFile: filepath.Join(t.TempDir(), "tailcat.json"),
		Roots:     testRoots,
		Serve:     serve,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { h.Close() })
	return h
}

// postForm posts the Access page's form with the given action and values.
func postForm(t *testing.T, h *Handler, action string, vals url.Values) *httptest.ResponseRecorder {
	t.Helper()
	if vals == nil {
		vals = url.Values{}
	}
	vals.Set("action", action)
	vals.Set("token", xsrftoken.Generate(auth.Token(), "user", xsrfAction))
	r := httptest.NewRequest("POST", "/access/", strings.NewReader(vals.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

// getPage returns the body of the Client access page at path.
func getPage(t *testing.T, h *Handler, path string) string {
	t.Helper()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
	if w.Code != 200 {
		t.Fatalf("GET %s: %v: %s", path, w.Code, w.Body)
	}
	return w.Body.String()
}

// createKey creates an access key with the Client access page's form, and
// returns its ID and secret, and the page it redirects to.
func createKey(t *testing.T, h *Handler, name string, perms ...string) (id, secret, page string) {
	t.Helper()
	w := postForm(t, h, "create", url.Values{"name": {name}, "perm": perms})
	loc := w.Header().Get("Location")
	if w.Code != 302 || !strings.HasPrefix(loc, "/access/?show=") {
		t.Fatalf("creating key: %v, Location %q: %s", w.Code, loc, w.Body)
	}
	u, _ := url.Parse(loc)
	id = u.Query().Get("show")
	u.Fragment = "" // as browsers do
	page = getPage(t, h, u.String())
	m := regexp.MustCompile(`<div class="key" id="key-` + id + `">[\s\S]*?class="secret" readonly value="([^"]+)"`).FindStringSubmatch(page)
	if m == nil {
		t.Fatalf("no secret for key %s in page: %s", id, page)
	}
	return id, m[1], page
}

func TestKeys(t *testing.T) {
	g := &grantRecorder{}
	h := newTestHandler(t, g)

	// The form for a new key is on its own page, which errors return to.
	if page := getPage(t, h, "/access/"); strings.Contains(page, `name="name"`) || !strings.Contains(page, `href="/access/new"`) {
		t.Errorf("main page has the new key form, or no link to it: %s", page)
	}
	if page := getPage(t, h, "/access/new"); !strings.Contains(page, `name="name"`) {
		t.Errorf("new key page lacks its form: %s", page)
	}
	if w := postForm(t, h, "create", url.Values{"name": {"x"}, "perm": {"bogus"}}); w.Code != 400 || !strings.Contains(w.Body.String(), "<h1>New access key</h1>") {
		t.Errorf("bogus perm: %v: %s", w.Code, w.Body)
	}
	if w := postForm(t, h, "create", url.Values{"perm": {"upload"}}); w.Code != 400 || !strings.Contains(w.Body.String(), "needs a name") {
		t.Errorf("no name: %v: %s", w.Code, w.Body)
	}
	r := httptest.NewRequest("POST", "/access/", strings.NewReader("action=create&name=x&token=bad"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Errorf("bad XSRF token: %v", w.Code)
	}

	id, secret, page := createKey(t, h, "phone", "upload", "sign")
	// Not listening, a new key's secret is shown, and it has no QR code
	// or app link.
	if !strings.Contains(page, "<details open><summary><span class=\"link\">Show key</span></summary>") || strings.Contains(page, "showQR(document") || strings.Contains(page, "perkeep://") {
		t.Errorf("new key's page doesn't show just its secret: %s", page)
	}
	if page := getPage(t, h, "/access/"); strings.Contains(page, "<details open") || !strings.Contains(page, "<summary><span class=\"link\">Permissions</span>: Upload blobs, Make claims</summary>") {
		t.Errorf("page shows a secret by default, or lacks the key's permissions: %s", page)
	}

	do := func(method, path, pass string) int {
		r := httptest.NewRequest(method, path, nil)
		r = r.WithContext(auth.GrantCtxKey.WithValue(r.Context(), 0))
		if pass != "" {
			r.SetBasicAuth("anyone", pass)
		}
		w := httptest.NewRecorder()
		h.serveTailcat(w, r)
		return w.Code
	}
	for _, tt := range []struct {
		method, path, pass string
		want               int
	}{
		{"POST", "/bs/camli/upload", "", 401},
		{"POST", "/bs/camli/upload", "wrong", 401},
		{"POST", "/bs/camli/upload", secret, 200},
		{"POST", "/sighelper/camli/sig/sign", secret, 200},
		{"GET", "/?camli.mode=config", secret, 200},
		{"GET", "/bs/camli/" + testRef, secret, 403},
		{"POST", "/search/camli/search/query", secret, 403},
		// Without the search permission, but allowed for uploading.
		{"GET", "/search/camli/search/contentpermanodes?wholedigest=" + testRef, secret, 200},
		{"GET", "/search/camli/search/contentpermanodes?wholedigest=" + testRef, "", 401},
		{"GET", "/status/status.json", secret, 403},
		{"GET", "/debug/config", secret, 403},
	} {
		if got := do(tt.method, tt.path, tt.pass); got != tt.want {
			t.Errorf("%s %s with %q = %v; want %v", tt.method, tt.path, tt.pass, got, tt.want)
		}
	}

	// Windows files don't have Unix permission bits.
	if runtime.GOOS != "windows" {
		if fi, err := os.Stat(h.file); err != nil || fi.Mode().Perm() != 0600 {
			t.Errorf("state file mode = %v, %v; want 0600", fi.Mode(), err)
		}
	}

	// Without the listener, there's no QR code.
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/access/qr?id="+id, nil))
	if w.Code != http.StatusConflict {
		t.Errorf("QR code while not listening: %v", w.Code)
	}

	if w := postForm(t, h, "setperms", url.Values{"id": {id}, "perm": {"download", "stats"}}); w.Code != 302 {
		t.Fatalf("setperms: %v: %s", w.Code, w.Body)
	}
	if got := do("POST", "/bs/camli/upload", secret); got != 403 {
		t.Errorf("upload after removing its permission = %v; want 403", got)
	}
	if got := do("GET", "/bs/camli/"+testRef, secret); got != 200 {
		t.Errorf("download after adding its permission = %v; want 200", got)
	}
	if got := do("GET", "/search/camli/search/contentpermanodes?wholedigest="+testRef, secret); got != 403 {
		t.Errorf("content permanodes without upload, sign, or search = %v; want 403", got)
	}

	// A new handler loads the same keys.
	h2, err := New(Config{Prefix: "/access/", StateFile: h.file, Roots: testRoots, Serve: g})
	if err != nil {
		t.Fatal(err)
	}
	defer h2.Close()
	if k := h2.keyFor(basicAuthReq(secret)); k == nil || k.perm != PermDownload|PermStats {
		t.Errorf("reloaded key = %+v", k)
	}

	if w := postForm(t, h, "delete", url.Values{"id": {id}}); w.Code != 302 {
		t.Fatalf("delete: %v: %s", w.Code, w.Body)
	}
	if got := do("GET", "/bs/camli/"+testRef, secret); got != 401 {
		t.Errorf("download after deleting key = %v; want 401", got)
	}
}

// opsMode is an auth mode that allows ops to every request.
type opsMode auth.Operation

func (m opsMode) AllowedAccess(*http.Request) auth.Operation { return auth.Operation(m) }
func (opsMode) AddAuthHeader(*http.Request)                  {}

func TestDescribeClient(t *testing.T) {
	h, err := New(Config{
		Prefix:    "/access/",
		StateFile: filepath.Join(t.TempDir(), "tailcat.json"),
		Roots:     testRoots,
		Serve:     &grantRecorder{},
		BaseURL:   func() string { return "https://perkeep.example.ts.net" },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	_, secret, _ := createKey(t, h, "phone", "upload", "stats")
	t.Cleanup(func() { auth.SetMode(auth.None{}) })

	for _, tt := range []struct {
		name    string
		mode    auth.AuthMode
		pass    string
		wantKey string
		want    []string
	}{
		{"access key", opsMode(0), secret, "phone", []string{"upload", "stats"}},
		{"full credentials", opsMode(auth.OpAll), "", "", []string{"upload", "sign", "search", "download", "stats"}},
		{"vivify", opsMode(auth.OpVivify), "", "", []string{"upload", "download"}},
		{"nothing", opsMode(0), "", "", []string{}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			auth.SetMode(tt.mode)
			r := httptest.NewRequest("GET", "/?camli.mode=config", nil)
			if tt.pass != "" {
				r.SetBasicAuth("", tt.pass)
			}
			c := h.DescribeClient(r)
			if c.ServerName != "perkeep.example.ts.net" || c.AccessKeyName != tt.wantKey || !slices.Equal(c.Permissions, tt.want) || c.Permissions == nil {
				t.Errorf("DescribeClient = %+v; want key %q, permissions %q", c, tt.wantKey, tt.want)
			}
		})
	}
}

// TestPermNames locks in the permissions' names, which are their format
// in state files. Add new ones, but never change these.
func TestPermNames(t *testing.T) {
	want := map[Perm]string{
		PermUpload:   "upload",
		PermSign:     "sign",
		PermSearch:   "search",
		PermDownload: "download",
		PermStats:    "stats",
	}
	for p, name := range want {
		got, err := ParsePerms([]string{name})
		if err != nil || got != p {
			t.Errorf("ParsePerms(%q) = %v, %v; want %v", name, got, err, p)
		}
		if names := p.Names(); len(names) != 1 || names[0] != name {
			t.Errorf("Perm(%d).Names() = %q; want [%q]", p, names, name)
		}
	}
}

func TestLoadIgnoresKeysWithoutSecret(t *testing.T) {
	file := filepath.Join(t.TempDir(), "tailcat.json")
	const data = `{"keys": [
		{"id": "a", "name": "old", "hash": "abc", "perms": ["upload"]},
		{"id": "b", "name": "new", "secret": "s3cret", "perms": ["stats"]}
	]}`
	if err := os.WriteFile(file, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	h, err := New(Config{Prefix: "/access/", StateFile: file, Roots: testRoots, Serve: &grantRecorder{}})
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	if len(h.st.Keys) != 1 || h.st.Keys[0].Name != "new" || h.st.Keys[0].perm != PermStats {
		t.Errorf("keys = %+v; want just the one with a secret", h.st.Keys)
	}
}

func TestName(t *testing.T) {
	hostname, err := os.Hostname()
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct{ baseURL, want string }{
		{"https://perkeep.example.ts.net", "perkeep.example.ts.net"},
		{"https://perkeep.example.com:3179", "perkeep.example.com"},
		{"http://perkeep.example.com", hostname}, // no TLS certificate
		{"https://192.0.2.1:3179", hostname},     // no name in the certificate
		{"https://[2001:db8::1]:3179", hostname}, // nor here
		{"", hostname},
	} {
		h := &Handler{baseURL: func() string { return tt.baseURL }}
		if got := h.defaultName(); got != tt.want {
			t.Errorf("defaultName with base URL %q = %q; want %q", tt.baseURL, got, tt.want)
		}
	}

	h, err := New(Config{
		Prefix:    "/access/",
		StateFile: filepath.Join(t.TempDir(), "tailcat.json"),
		Roots:     testRoots,
		Serve:     &grantRecorder{},
		BaseURL:   func() string { return "https://perkeep.example.ts.net" },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	if page := getPage(t, h, "/access/"); !strings.Contains(page, "Clients show this server as <b>perkeep.example.ts.net</b>") {
		t.Errorf("page lacks the default name: %s", page)
	}
	if page := getPage(t, h, "/access/name"); !strings.Contains(page, `placeholder="perkeep.example.ts.net"`) {
		t.Errorf("name page lacks the default: %s", page)
	}
	if w := postForm(t, h, "setname", url.Values{"name": {" Family photos "}}); w.Code != 302 {
		t.Fatalf("setname: %v: %s", w.Code, w.Body)
	}
	if got := h.name(); got != "Family photos" {
		t.Errorf("name = %q; want %q", got, "Family photos")
	}
	if w := postForm(t, h, "setname", url.Values{"name": {strings.Repeat("x", 101)}}); w.Code != 400 || !strings.Contains(w.Body.String(), "<h1>Server name</h1>") {
		t.Errorf("too long name: %v: %s", w.Code, w.Body)
	}
	if w := postForm(t, h, "setname", nil); w.Code != 302 || h.name() != "perkeep.example.ts.net" {
		t.Errorf("resetting name: %v; name %q", w.Code, h.name())
	}
}

// TestHTTPSTarget tests the QR codes and links without the tailcat
// listener, which send clients to the server's HTTPS base URL.
func TestHTTPSTarget(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile := filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	if _, err := httputil.GenSelfTLSFiles("192.0.2.1", certFile, keyFile); err != nil {
		t.Fatal(err)
	}
	certPEM, err := os.ReadFile(certFile)
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(certPEM)
	sum := sha256.Sum256(block.Bytes)
	wantFP := hex.EncodeToString(sum[:])

	for _, tt := range []struct {
		name, baseURL, certFile string
		want                    target
	}{
		{"plain HTTP", "http://192.0.2.1:3179", "", target{}},
		{"HTTPS, verifiable", "https://perkeep.example.ts.net", "", target{url: "https://perkeep.example.ts.net"}},
		{"HTTPS, self-signed", "https://192.0.2.1:3179", certFile, target{url: "https://192.0.2.1:3179", certFP: wantFP}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h, err := New(Config{
				Prefix:      "/access/",
				StateFile:   filepath.Join(t.TempDir(), "tailcat.json"),
				Roots:       testRoots,
				Serve:       &grantRecorder{},
				BaseURL:     func() string { return tt.baseURL },
				TLSCertFile: tt.certFile,
			})
			if err != nil {
				t.Fatal(err)
			}
			defer h.Close()
			got, err := h.clientTarget()
			if err != nil || got != tt.want {
				t.Fatalf("clientTarget = %+v, %v; want %+v", got, err, tt.want)
			}
			id, secret, page := createKey(t, h, "phone", "upload")
			if tt.want == (target{}) {
				if !strings.Contains(page, "For QR codes and links, enable the tailcat listener, or serve the server over HTTPS.") || strings.Contains(page, "perkeep://") {
					t.Errorf("page offers QR codes or links: %s", page)
				}
				return
			}
			if !strings.Contains(page, "Their QR codes and links send clients to it at "+tt.baseURL) ||
				!strings.Contains(page, `showQR(document.getElementById("qr-link-`+id+`"))`) {
				t.Errorf("page lacks where its QR codes send clients, or the new key's QR code: %s", page)
			}
			h.mu.Lock()
			k := h.st.Keys[0]
			h.mu.Unlock()
			u, err := url.Parse(clientURL(got, k, h.name()))
			q := u.Query()
			if err != nil || q.Get("url") != tt.baseURL || q.Get("certfp") != tt.want.certFP || q.Get("key") != secret || q.Has("tailcat") {
				t.Errorf("clientURL = %q, %v", u, err)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest("GET", "/access/qr?id="+id, nil))
			if w.Code != 200 || w.Header().Get("Content-Type") != "image/png" {
				t.Errorf("QR code: %v, %q", w.Code, w.Header().Get("Content-Type"))
			}
		})
	}
}

// TestAuthMode tests access keys on the server's usual listeners, where
// the Handler is an auth mode.
func TestAuthMode(t *testing.T) {
	h := newTestHandler(t, &grantRecorder{})
	_, secret, _ := createKey(t, h, "phone", "upload")
	auth.SetMode(h)
	t.Cleanup(func() { auth.SetMode(auth.None{}) })

	req := func(method, path, pass string) *http.Request {
		r := httptest.NewRequest(method, path, nil)
		if pass != "" {
			r.SetBasicAuth("", pass)
		}
		return r
	}
	for _, tt := range []struct {
		method, path, pass string
		want               bool
	}{
		{"POST", "/bs/camli/upload", secret, true},
		{"GET", "/?camli.mode=config", secret, true},
		{"POST", "/bs/camli/upload", "", false},
		{"POST", "/bs/camli/upload", "wrong", false},
		{"GET", "/bs/camli/" + testRef, secret, false}, // no download permission
		{"GET", "/access/", secret, false},             // never the Client access page
		{"GET", "/debug/config", secret, false},
	} {
		if got := auth.Allowed(req(tt.method, tt.path, tt.pass), auth.OpAll); got != tt.want {
			t.Errorf("Allowed(%s %s, key %q) = %v; want %v", tt.method, tt.path, tt.pass, got, tt.want)
		}
	}
	// What an access key allows, it allows only as a ScopedMode, so
	// that discovery, say, doesn't give it the web UI's token.
	if auth.AllowedUnscoped(req("GET", "/?camli.mode=config", secret), auth.OpDiscovery) {
		t.Error("AllowedUnscoped allows an access key")
	}
}

func basicAuthReq(pass string) *http.Request {
	r := httptest.NewRequest("GET", "/", nil)
	r.SetBasicAuth("", pass)
	return r
}

func TestGrantOverridesModes(t *testing.T) {
	auth.SetMode(auth.None{})
	t.Cleanup(func() { auth.SetMode(auth.None{}) })
	r := httptest.NewRequest("GET", "/", nil)
	if !auth.Allowed(r, auth.OpAll) {
		t.Fatal("not allowed without a grant")
	}
	r = r.WithContext(auth.GrantCtxKey.WithValue(r.Context(), 0))
	if auth.Allowed(r, auth.OpGet) {
		t.Error("allowed with an empty grant")
	}
}

// runDERP runs a DERP server on localhost, without STUN, and returns its
// region.
func runDERP(t *testing.T) *tailcfg.DERPRegion {
	d := derpserver.New(key.NewNode(), logger.Discard)
	srv := httptest.NewUnstartedServer(derpserver.Handler(d))
	srv.Config.TLSNextProto = make(map[string]func(*http.Server, *tls.Conn, http.Handler))
	srv.StartTLS()
	t.Cleanup(func() {
		srv.CloseClientConnections()
		srv.Close()
		d.Close()
	})
	return &tailcfg.DERPRegion{
		RegionID:   1,
		RegionCode: "test",
		RegionName: "Test Town",
		Nodes: []*tailcfg.DERPNode{{
			Name:             "t1",
			RegionID:         1,
			HostName:         "127.0.0.1",
			IPv4:             "127.0.0.1",
			IPv6:             "none",
			STUNPort:         -1,
			DERPPort:         srv.Listener.Addr().(*net.TCPAddr).Port,
			InsecureForTests: true,
		}},
	}
}

// TestTailcat tests the listener end to end, through a local DERP server.
func TestTailcat(t *testing.T) {
	g := &grantRecorder{}
	mon := health.New()
	h, err := New(Config{
		Prefix:    "/access/",
		StateFile: filepath.Join(t.TempDir(), "tailcat.json"),
		Roots:     testRoots,
		Serve:     g,
		Health:    mon,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { h.Close() })
	checkState := func(want health.State) {
		t.Helper()
		st := mon.Status()
		if len(st.Checks) != 1 || st.Checks[0].State != want {
			t.Errorf("health checks = %+v; want one, %v", st.Checks, want)
		}
	}
	checkState(health.StateOK) // disabled

	h.testRegion = runDERP(t)
	h.st.Identity = &identity{
		Key:          key.NewNode(),
		PresharedKey: tailcat.NewPresharedKey(),
		RegionID:     1,
	}
	if w := postForm(t, h, "enable", nil); w.Code != 302 {
		t.Fatalf("enable: %v: %s", w.Code, w.Body)
	}
	id, secret, page := createKey(t, h, "test", "search")
	// Listening, a new key's QR code is shown, but not its secret.
	if !strings.Contains(page, `showQR(document.getElementById("qr-link-`+id+`"))`) || strings.Contains(page, "<details open") {
		t.Errorf("new key's page doesn't show just its QR code: %s", page)
	}
	if page := getPage(t, h, "/access/"); strings.Contains(page, "showQR(document") {
		t.Errorf("page shows a QR code by default: %s", page)
	}

	h.mu.Lock()
	addr, ts := h.addr, h.ts
	k := h.st.Keys[0]
	h.stopProbes() // the test records its own self-checks' results
	h.mu.Unlock()
	if addr == "" {
		t.Fatal("no tailcat address")
	}

	// The self-check reaches the listener through the DERP server.
	if err := h.probe(t.Context(), addr); err != nil {
		t.Fatalf("self-check: %v", err)
	}
	h.recordProbe(ts, nil)
	checkState(health.StateOK)
	page = getPage(t, h, "/access/")
	// html/template escapes the & in the link as &amp;.
	appLink := `<a class="action" href="` + strings.ReplaceAll(clientURL(target{tailcat: addr}, k, h.name()), "&", "&amp;") + `">Open in the Perkeep app</a>`
	if !strings.Contains(page, "🟢 <b>Listening, with tailcat DERP region 1 (Test Town); clients can reach it</b>") ||
		!strings.Contains(page, `data-qr="/access/qr?id=`+id+`"`) ||
		!strings.Contains(page, appLink) ||
		!strings.Contains(page, `<div class="addr"`) {
		t.Errorf("page lacks the status, QR code, app link %s, or address: %s", appLink, page)
	}
	h.recordProbe(ts, errors.New("boom"))
	checkState(health.StateWarning)
	h.recordProbe(ts, errors.New("boom"))
	checkState(health.StateFailed)
	if page := getPage(t, h, "/access/"); !strings.Contains(page, "🔴 <b>Listening, but boom</b>") {
		t.Errorf("page lacks the failed status: %s", page)
	}

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/access/qr?id="+id, nil))
	if w.Code != 200 || w.Header().Get("Content-Type") != "image/png" {
		t.Errorf("QR code: %v, %q", w.Code, w.Header().Get("Content-Type"))
	}
	u, err := url.Parse(clientURL(target{tailcat: addr}, k, "My Perkeep"))
	if err != nil || u.Scheme != "perkeep" || u.Host != "access" ||
		u.Query().Get("tailcat") != string(addr) || u.Query().Get("key") != secret ||
		u.Query().Get("name") != "My Perkeep" || u.Query().Has("url") || u.Query().Has("certfp") {
		t.Errorf("clientURL = %q, %v", u, err)
	}
	// While listening, tailcat is preferred to an HTTPS base URL.
	h.baseURL = func() string { return "https://perkeep.example.com" }
	if tg, err := h.clientTarget(); err != nil || tg != (target{tailcat: addr}) {
		t.Errorf("clientTarget = %+v, %v; want the tailcat address", tg, err)
	}
	cl := &tailcat.Client{Server: addr, Logf: logger.Discard}
	defer cl.Close()
	hc := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return cl.DialTCPPort(ctx, 80)
		},
	}}
	get := func(path, pass string) int {
		req, _ := http.NewRequestWithContext(t.Context(), "GET", "http://perkeep"+path, nil)
		if pass != "" {
			req.SetBasicAuth("", pass)
		}
		res, err := hc.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.StatusCode
	}
	if got := get("/search/camli/search/recent", ""); got != 401 {
		t.Errorf("without key = %v; want 401", got)
	}
	if got := get("/search/camli/search/recent", secret); got != 200 {
		t.Errorf("with key = %v; want 200", got)
	}
	if got := get("/bs/camli/"+testRef, secret); got != 403 {
		t.Errorf("download without permission = %v; want 403", got)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.served) != 1 {
		t.Errorf("served %q; want just the search", g.served)
	}

	if w := postForm(t, h, "disable", nil); w.Code != 302 {
		t.Fatalf("disable: %v: %s", w.Code, w.Body)
	}
	if h.ts != nil {
		t.Error("still listening after disable")
	}
	if w := postForm(t, h, "forget", nil); w.Code != 302 || h.st.Identity != nil {
		t.Errorf("forget: %v, identity %v", w.Code, h.st.Identity)
	}
}
