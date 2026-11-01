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

package secrets

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"go4.org/jsonconfig"
	"perkeep.org/pkg/auth"
	"perkeep.org/pkg/blobserver"
	"perkeep.org/pkg/client"
	"perkeep.org/pkg/secretstore"
)

func testHandler(t *testing.T) *Handler {
	t.Helper()
	store, err := secretstore.NewMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	auth.SetMode(&auth.UserPass{Username: "owner", Password: "password"})
	t.Cleanup(func() { auth.SetMode(auth.None{}) })
	return New(store)
}

func request(h http.Handler, method, path, body string, headers map[string]string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://example.test"+path, strings.NewReader(body))
	r.SetBasicAuth("owner", "password")
	if method == "PUT" || method == "DELETE" {
		r.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestRoundTrip(t *testing.T) {
	h := testHandler(t)
	mux := http.NewServeMux()
	mux.Handle("/secrets/", h)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/javascript")
		fmt.Fprint(w, `{"blobRoot":"/bs/","secretRoot":"/secrets/","authToken":"not-owner-credentials"}`)
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()
	c, err := client.New(client.OptionNoExternalConfig(), client.OptionServer(ts.URL), client.OptionAuthMode(&auth.UserPass{Username: "owner", Password: "password"}))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	name, value := "importer/sha224-abc/token", "raw-secret\n\u0000<&>"
	m, err := c.PutSecret(ctx, name, value, client.SecretPutOptions{ExpectedRevision: 0})
	if err != nil {
		t.Fatal(err)
	}
	if m.Revision <= 0 || m.MigratedFromPermanode || m.Updated.IsZero() {
		t.Fatalf("metadata: %+v", m)
	}
	list, err := c.ListSecrets(ctx)
	if err != nil || len(list) != 1 || list[0].Name != name || !list[0].Updated.Equal(m.Updated) {
		t.Fatalf("list: %+v, %v", list, err)
	}
	entry, err := c.GetSecret(ctx, name)
	if err != nil || entry.Value != value || !entry.Updated.Equal(m.Updated) {
		t.Fatalf("get value mismatch; error %v", err)
	}
	if _, err := c.PutSecret(ctx, name, "wrong", client.SecretPutOptions{ExpectedRevision: 0}); err == nil {
		t.Fatal("expected conflict")
	}
	if err := c.DeleteSecret(ctx, name, m.Revision+1); err == nil {
		t.Fatal("expected delete conflict")
	}
	if err := c.DeleteSecret(ctx, name, m.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err := c.GetSecret(ctx, name); err == nil {
		t.Fatal("deleted secret returned")
	}
}

func TestMetadataAndMigration(t *testing.T) {
	h := testHandler(t)
	ctx := context.Background()
	if err := h.store.Migrate(ctx, []secretstore.LegacyEntry{{Name: "test/name", Value: "NEVER-IN-LIST"}}); err != nil {
		t.Fatal(err)
	}
	w := request(h, "GET", "/secrets/", "", nil)
	if w.Code != 200 || strings.Contains(w.Body.String(), "NEVER-IN-LIST") || strings.Contains(w.Body.String(), `"value"`) || !strings.Contains(w.Body.String(), `"migratedFromPermanode":true`) {
		t.Fatalf("metadata list: %d %s", w.Code, w.Body.String())
	}
	for _, path := range []string{"/secrets/", "/secrets/value?name=test/name"} {
		w = request(h, "GET", path, "", nil)
		if w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Fatal("missing anti-cache headers")
		}
	}
	for _, body := range []string{
		`{"value":"replacement","expectedRevision":-1}`,
		`{"value":"replacement","expectedRevision":-1,"clearMigration":true}`,
	} {
		w = request(h, "PUT", "/secrets/value?name=test/name", body, nil)
		if w.Code != 200 {
			t.Fatalf("put: %d %s", w.Code, w.Body.String())
		}
		var meta secretstore.Metadata
		if err := json.Unmarshal(w.Body.Bytes(), &meta); err != nil {
			t.Fatal(err)
		}
		if meta.Updated.IsZero() {
			t.Fatal("successful write returned a zero update time")
		}
		if meta.MigratedFromPermanode == strings.Contains(body, `"clearMigration":true`) {
			t.Fatal("migration warning not sticky/explicitly clearable")
		}
	}
}

func TestRequestSecurity(t *testing.T) {
	h := testHandler(t)
	tests := []struct {
		method, path, body string
		headers            map[string]string
		status             int
	}{
		{"GET", "/secrets/", "", map[string]string{"Authorization": ""}, 401},
		{"GET", "/secrets/", "", map[string]string{"Authorization": "Token " + auth.Token()}, 401},
		{"GET", "/secrets/", "", map[string]string{"Origin": "https://evil.test"}, 403},
		{"GET", "/secrets/", "", map[string]string{"Origin": "null"}, 403},
		{"GET", "/secrets/", "", map[string]string{"Sec-Fetch-Site": "cross-site"}, 403},
		{"GET", "/secrets/", "", map[string]string{"Origin": "http://example.test"}, 200},
		{"GET", "/secrets/", "", map[string]string{"Origin": "https://example.test"}, 403},
		{"GET", "/secrets/", "", map[string]string{"Origin": "http://example.test/"}, 403},
		{"GET", "/secrets/?value=SECRET-MARKER", "", nil, 400},
		{"GET", "/secrets/value?name=test&value=SECRET-MARKER", "", nil, 400},
		{"GET", "/secrets/value?name=test&name=other", "", nil, 400},
		{"GET", "/secrets/value?name=../bad", "", nil, 400},
		{"PUT", "/secrets/value?name=test", `{"value":"SECRET-MARKER","expectedRevision":0}`, map[string]string{"Content-Type": "text/plain"}, 415},
		{"PUT", "/secrets/value?name=test", `{"value":"SECRET-MARKER"}`, nil, 400},
		{"PUT", "/secrets/value?name=test", `{"value":"SECRET-MARKER","expectedRevision":-2}`, nil, 400},
		{"PUT", "/secrets/value?name=test", `{"value":"SECRET-MARKER","expectedRevision":0,"migratedFromPermanode":true}`, nil, 400},
		{"PUT", "/secrets/value?name=test", `{"value":"SECRET-MARKER","expectedRevision":0} {}`, nil, 400},
		{"PUT", "/secrets/value?name=test", `{"value":"SECRET-MARKER`, nil, 400},
		{"DELETE", "/secrets/value?name=test", `{"expectedRevision":-1,"value":"SECRET-MARKER"}`, nil, 400},
		{"POST", "/secrets/", "SECRET-MARKER", nil, 405},
		{"GET", "/secrets", "", nil, 404}, // Handler never redirects.
	}
	for i, tt := range tests {
		w := request(h, tt.method, tt.path, tt.body, tt.headers)
		if w.Code != tt.status {
			t.Errorf("case %d: status %d, want %d (%s)", i, w.Code, tt.status, w.Body.String())
		}
		if strings.Contains(w.Body.String(), "SECRET-MARKER") {
			t.Errorf("case %d echoed input", i)
		}
		if w.Header().Get("Location") != "" {
			t.Errorf("case %d redirected", i)
		}
	}
	tooLarge := `{"value":"` + strings.Repeat("x", secretstore.MaxValueSize+1) + `","expectedRevision":0}`
	if w := request(h, "PUT", "/secrets/value?name=test", tooLarge, nil); w.Code != 413 {
		t.Errorf("value limit: %d", w.Code)
	}
	tooLarge = `{"value":"` + strings.Repeat("x", maxRequestSize) + `","expectedRevision":0}`
	if w := request(h, "PUT", "/secrets/value?name=test", tooLarge, nil); w.Code != 413 {
		t.Errorf("body limit: %d", w.Code)
	}
}

type scopedOwner struct{ auth.AuthMode }

func (scopedOwner) ScopedOnly() {}

func TestScopedAndGrantDenied(t *testing.T) {
	h := testHandler(t)
	auth.SetMode(scopedOwner{&auth.UserPass{Username: "owner", Password: "password"}})
	if w := request(h, "GET", "/secrets/", "", nil); w.Code != 401 {
		t.Fatalf("scoped mode: %d", w.Code)
	}
	auth.SetMode(auth.None{})
	r := httptest.NewRequest("GET", "http://example.test/secrets/", nil)
	r = r.WithContext(auth.GrantCtxKey.WithValue(r.Context(), auth.OpOwner))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatalf("grant accepted: %d", w.Code)
	}
}

func TestConfiguredOrigin(t *testing.T) {
	h := testHandler(t)
	h.baseURL = func() string { return "https://public.example.test/base" }
	for origin, want := range map[string]int{"https://public.example.test": 200, "http://public.example.test": 403, "https://evil.test": 403} {
		if w := request(h, "GET", "/secrets/", "", map[string]string{"Origin": origin, "X-Forwarded-Host": "evil.test"}); w.Code != want {
			t.Errorf("origin %q: %d", origin, w.Code)
		}
	}
}

type configLoader struct {
	blobserver.Loader
	baseCalled bool
}

func (l *configLoader) MyPrefix() string { return "/custom/" }
func (l *configLoader) BaseURL() string  { l.baseCalled = true; return "https://public.example.test" }

func TestConfigAndOwnership(t *testing.T) {
	l := new(configLoader)
	handler, err := newFromConfig(l, jsonconfig.Obj{"memory": true})
	if err != nil {
		t.Fatal(err)
	}
	h := handler.(*Handler)
	if l.baseCalled {
		t.Fatal("constructor called blocking BaseURL")
	}
	if h.prefix != "/custom/" || h.SecretStore() == nil {
		t.Fatal("configuration not applied")
	}
	if err := h.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := h.store.List(context.Background()); err == nil {
		t.Fatal("owned store not closed")
	}
	if _, err := newFromConfig(l, jsonconfig.Obj{"memory": true, "file": "ignored"}); err == nil {
		t.Fatal("file and memory accepted")
	}
	if _, err := newFromConfig(l, jsonconfig.Obj{"unknown": true}); err == nil {
		t.Fatal("unknown option accepted")
	}
	h = testHandler(t)
	if err := h.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := h.store.List(context.Background()); err != nil {
		t.Fatal("caller-owned store closed")
	}
}

type operationAuth auth.Operation

func (a operationAuth) AllowedAccess(*http.Request) auth.Operation { return auth.Operation(a) }
func (operationAuth) AddAuthHeader(*http.Request)                  {}

func TestPerMethodAuthorization(t *testing.T) {
	h := testHandler(t)
	for _, tt := range []struct {
		ops                   auth.Operation
		listStatus, putStatus int
	}{
		{auth.OpAll, 401, 401},
		{auth.OpSecretRead, 200, 401},
		{auth.OpSecretWrite, 401, 200},
		{auth.OpOwner, 200, 200},
	} {
		auth.SetMode(operationAuth(tt.ops))
		if w := request(h, "GET", "/secrets/", "", nil); w.Code != tt.listStatus {
			t.Errorf("ops %v list: %d", tt.ops, w.Code)
		}
		if w := request(h, "PUT", "/secrets/value?name=test", `{"value":"value","expectedRevision":-1}`, nil); w.Code != tt.putStatus {
			t.Errorf("ops %v put: %d", tt.ops, w.Code)
		}
	}
}

func TestDeleteStrictFieldsAndClosedStore(t *testing.T) {
	h := testHandler(t)
	for _, body := range []string{
		`{"expectedRevision":-1,"clearMigration":false}`,
		`{"expectedRevision":-1,"value":null}`,
		`{"expectedRevision":null}`,
		`null`,
	} {
		if w := request(h, "DELETE", "/secrets/value?name=test", body, nil); w.Code != 400 {
			t.Errorf("delete body %q accepted: %d", body, w.Code)
		}
	}
	if err := h.store.Close(); err != nil {
		t.Fatal(err)
	}
	if w := request(h, "GET", "/secrets/", "", nil); w.Code != 500 || strings.Contains(w.Body.String(), "closed") {
		t.Fatalf("unredacted closed-store error: %d %s", w.Code, w.Body.String())
	}
}

func TestConfiguredDurableStore(t *testing.T) {
	file := filepath.Join(t.TempDir(), "private", "secrets.sqlite")
	loader := new(configLoader)
	conf := jsonconfig.Obj{"file": file}
	handler, err := newFromConfig(loader, conf)
	if err != nil {
		t.Fatal(err)
	}
	h := handler.(*Handler)
	if _, err := h.store.Put(context.Background(), "service/token", "durable-value", secretstore.PutOptions{ExpectedRevision: 0}); err != nil {
		t.Fatal(err)
	}
	if err := h.Close(); err != nil {
		t.Fatal(err)
	}
	handler, err = newFromConfig(loader, jsonconfig.Obj{"file": file})
	if err != nil {
		t.Fatal(err)
	}
	h = handler.(*Handler)
	defer h.Close()
	entry, err := h.store.Get(context.Background(), "service/token")
	if err != nil || entry.Value != "durable-value" {
		t.Fatal("configured store did not survive reopen")
	}
	if _, err := newFromConfig(loader, jsonconfig.Obj{"file": t.TempDir()}); err == nil || err.Error() != "could not open secret store" {
		t.Fatalf("store errors must fail closed without filesystem details: %v", err)
	}
}

func TestRejectMalformedUTF8Request(t *testing.T) {
	h := testHandler(t)
	body := `{"value":"sensitive-prefix` + string([]byte{0xff}) + `","expectedRevision":0}`
	w := request(h, "PUT", "/secrets/value?name=test", body, nil)
	if w.Code != 400 || strings.Contains(w.Body.String(), "sensitive-prefix") {
		t.Fatalf("malformed UTF-8: %d %s", w.Code, w.Body.String())
	}
	if _, err := h.store.Get(context.Background(), "test"); err != secretstore.ErrNotFound {
		t.Fatal("malformed UTF-8 was written")
	}
	w = httptest.NewRecorder()
	storeError(w, secretstore.ErrInvalidValue)
	if w.Code != 400 || strings.Contains(w.Body.String(), "secretstore") {
		t.Fatal("invalid-value store error not mapped/redacted")
	}
}
