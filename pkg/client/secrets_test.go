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

package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"perkeep.org/pkg/auth"
)

func secretTestClient(t *testing.T, url string) *Client {
	t.Helper()
	c, err := New(OptionNoExternalConfig(), OptionServer(url), OptionAuthMode(&auth.UserPass{Username: "owner", Password: "owner-password"}))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestSecretsUnsupported(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/javascript")
		fmt.Fprint(w, `{"blobRoot":"/bs/"}`)
	}))
	defer s.Close()
	c := secretTestClient(t, s.URL)
	if _, err := c.ListSecrets(context.Background()); !errors.Is(err, ErrNoSecretRoot) {
		t.Fatalf("older server: %v", err)
	}
}

func TestSecretsUseOwnerCredentials(t *testing.T) {
	const value = "secret-only-in-json-body"
	var writes int
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != "owner" || pass != "owner-password" {
			t.Error("client did not use normal owner credentials")
		}
		if strings.Contains(r.URL.String(), value) {
			t.Error("secret value in URL")
		}
		w.Header().Set("Content-Type", "text/javascript")
		if r.URL.Path == "/" {
			fmt.Fprint(w, `{"blobRoot":"/bs/","secretRoot":"/secrets/","authToken":"ui-token-not-owner"}`)
			return
		}
		if r.URL.Query().Get("name") != "service/account/token" {
			t.Errorf("name: %q", r.URL.Query().Get("name"))
		}
		if r.Method == "PUT" {
			writes++
			if r.Header.Get("Content-Type") != "application/json" {
				t.Error("JSON content type missing")
			}
			var body struct {
				Value            string
				ExpectedRevision int64
				ClearMigration   bool
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if body.Value != value || body.ExpectedRevision != 7 || !body.ClearMigration {
				t.Error("request did not preserve value/options")
			}
			fmt.Fprint(w, `{"name":"service/account/token","revision":8,"migratedFromPermanode":false,"updated":"2026-01-01T00:00:00Z"}`)
			return
		}
		if r.Method == "DELETE" {
			var body struct{ ExpectedRevision int64 }
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if body.ExpectedRevision != 8 {
				t.Error("delete CAS missing")
			}
			w.WriteHeader(204)
		}
	}))
	defer s.Close()
	c := secretTestClient(t, s.URL)
	meta, err := c.PutSecret(context.Background(), "service/account/token", value, SecretPutOptions{ExpectedRevision: 7, ClearMigration: true})
	if err != nil || meta.Revision != 8 || writes != 1 {
		t.Fatalf("put: %+v, %v", meta, err)
	}
	if err := c.DeleteSecret(context.Background(), "service/account/token", 8); err != nil {
		t.Fatal(err)
	}
	if _, err := c.GetSecret(context.Background(), ""); err == nil {
		t.Fatal("empty get name accepted")
	}
}

func TestSecretErrorsAreRedactedAndNoRedirects(t *testing.T) {
	var leaked bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaked = true }))
	defer target.Close()
	for _, status := range []int{307, 308, 400, 401, 404, 409, 413, 500} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/javascript")
				if r.URL.Path == "/" {
					fmt.Fprint(w, `{"blobRoot":"/bs/","secretRoot":"/secrets/"}`)
					return
				}
				w.Header().Set("Location", target.URL+"/leak")
				w.WriteHeader(status)
				fmt.Fprint(w, "do-not-echo-secret")
			}))
			defer s.Close()
			c := secretTestClient(t, s.URL)
			_, err := c.PutSecret(context.Background(), "service/token", "do-not-echo-secret", SecretPutOptions{ExpectedRevision: -1})
			var he *SecretHTTPError
			if !errors.As(err, &he) || he.StatusCode != status {
				t.Fatalf("error: %v", err)
			}
			if strings.Contains(err.Error(), "do-not-echo-secret") || strings.Contains(err.Error(), s.URL) {
				t.Fatalf("unredacted error: %v", err)
			}
		})
	}
	if leaked {
		t.Error("secret redirect was followed")
	}
}

func TestSecretRootMustStayOnServer(t *testing.T) {
	for _, root := range []string{"https://evil.example/secrets/", "//evil.example/secrets/", "/secrets/?token=bad", "/secrets/?", "/secrets/#bad", "/secrets/#", "/secrets"} {
		t.Run(root, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/javascript")
				json.NewEncoder(w).Encode(map[string]string{"blobRoot": "/bs/", "secretRoot": root})
			}))
			defer s.Close()
			if _, err := secretTestClient(t, s.URL).SecretRoot(); err == nil {
				t.Error("unsafe secret root accepted")
			}
		})
	}
}

func TestSecretResponseDecodeRedaction(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/javascript")
		if r.URL.Path == "/" {
			fmt.Fprint(w, `{"blobRoot":"/bs/","secretRoot":"/secrets/"}`)
			return
		}
		fmt.Fprint(w, "plaintext-secret-must-not-escape")
	}))
	defer s.Close()
	_, err := secretTestClient(t, s.URL).GetSecret(context.Background(), "test")
	if err == nil || strings.Contains(err.Error(), "plaintext-secret") {
		t.Fatalf("decode error: %v", err)
	}
}

func TestSecretRequestCancellation(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/javascript")
		fmt.Fprint(w, `{"blobRoot":"/bs/","secretRoot":"/secrets/"}`)
	}))
	defer s.Close()
	c := secretTestClient(t, s.URL)
	if _, err := c.SecretRoot(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.GetSecret(ctx, "name"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled request: %v", err)
	}
}

func TestSecretAbsoluteRootValidation(t *testing.T) {
	for _, tt := range []struct {
		name string
		root func(string) string
		ok   bool
	}{
		{"same origin", func(base string) string { return base + "/secrets/" }, true},
		{"same origin protocol relative", func(base string) string { return strings.TrimPrefix(base, "https:") + "/secrets/" }, true},
		{"scheme downgrade", func(base string) string { return strings.Replace(base, "https:", "http:", 1) + "/secrets/" }, false},
		{"cross origin", func(string) string { return "https://evil.example/secrets/" }, false},
		{"userinfo", func(base string) string {
			return strings.Replace(base, "https://", "https://user:password@", 1) + "/secrets/"
		}, false},
		{"empty path", func(base string) string { return base }, false},
		{"missing trailing slash", func(base string) string { return base + "/secrets" }, false},
		{"query", func(base string) string { return base + "/secrets/?token=bad" }, false},
		{"empty query", func(base string) string { return base + "/secrets/?" }, false},
		{"fragment", func(base string) string { return base + "/secrets/#bad" }, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var advertised string
			s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/javascript")
				json.NewEncoder(w).Encode(map[string]string{"blobRoot": "/bs/", "secretRoot": advertised})
			}))
			defer s.Close()
			advertised = tt.root(s.URL)
			c := secretTestClient(t, s.URL)
			c.SetHTTPClient(s.Client())
			root, err := c.SecretRoot()
			if (err == nil) != tt.ok {
				t.Fatalf("SecretRoot() = %q, %v; want accepted %v", root, err, tt.ok)
			}
			if tt.ok && root != s.URL+"/secrets/" {
				t.Fatalf("resolved root = %q", root)
			}
		})
	}
}

func TestSecretPutRejectsInvalidUTF8BeforeRequest(t *testing.T) {
	calls := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++ }))
	defer s.Close()
	c := secretTestClient(t, s.URL)
	_, err := c.PutSecret(context.Background(), "name", string([]byte{0xff}), SecretPutOptions{ExpectedRevision: -1})
	if err == nil || err.Error() != "client: secret value must be valid UTF-8" {
		t.Fatalf("invalid UTF-8: %v", err)
	}
	if calls != 0 {
		t.Fatal("invalid UTF-8 value triggered HTTP request before rejection")
	}
}
