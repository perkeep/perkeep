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

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/tailscale/tailcat"
)

func TestParseAccessURL(t *testing.T) {
	// A real tailcat address, as a server would make.
	ci := tailcat.NewPrivateKey().Public
	ci.RegionID = 1
	tc := ci.Addr()
	fp := strings.Repeat("ab", 32)
	q := func(kv ...string) string {
		v := url.Values{}
		for i := 0; i < len(kv); i += 2 {
			v.Set(kv[i], kv[i+1])
		}
		return "perkeep://access?" + v.Encode()
	}
	tests := []struct {
		in   string
		want accessInfo // zero if an error is expected
	}{
		{q("key", "K", "name", "home", "url", "https://pk.example.com"),
			accessInfo{Key: "K", Name: "home", URL: "https://pk.example.com"}},
		{q("key", "K", "name", "home", "url", "https://10.0.0.5", "certfp", fp, "future", "x"),
			accessInfo{Key: "K", Name: "home", URL: "https://10.0.0.5", CertFP: fp}},
		{q("key", "K", "name", "home", "tailcat", string(tc)),
			accessInfo{Key: "K", Name: "home", Tailcat: string(tc)}},
		{q("name", "home", "url", "https://pk.example.com"), accessInfo{}},            // no key
		{q("key", "K", "url", "https://pk.example.com"), accessInfo{}},                // no name
		{q("key", "K", "name", "home"), accessInfo{}},                                 // no server
		{q("key", "K", "name", "home", "url", "http://pk.example.com"), accessInfo{}}, // not https
		{q("key", "K", "name", "home", "url", "https://x", "certfp", "AB"), accessInfo{}},
		{q("key", "K", "name", "home", "url", "https://x", "certfp", strings.ToUpper(fp)), accessInfo{}},
		{q("key", "K", "name", "home", "tailcat", "tcnope"), accessInfo{}},
		{q("key", "K", "name", "home", "tailcat", string(tc), "url", "https://x"), accessInfo{}},
		{q("key", "K", "name", "home", "tailcat", string(tc), "certfp", fp), accessInfo{}},
		{"https://pk.example.com/?key=K&name=home", accessInfo{}},
		{"perkeep://other?key=K&name=home&url=https://x", accessInfo{}},
	}
	for _, tt := range tests {
		got, err := parseAccessURL(tt.in)
		if tt.want == (accessInfo{}) {
			if err == nil {
				t.Errorf("parseAccessURL(%q) = %+v; want error", tt.in, got)
			}
			continue
		}
		if err != nil || got != tt.want {
			t.Errorf("parseAccessURL(%q) = %+v, %v; want %+v", tt.in, got, err, tt.want)
		}
	}

	var got accessInfo
	do(t, newServer(), "POST", "/parseaccessurl", map[string]string{"url": tests[0].in}, http.StatusOK, &got)
	if got != tests[0].want {
		t.Errorf("POST /parseaccessurl = %+v", got)
	}
	do(t, newServer(), "POST", "/parseaccessurl", map[string]string{"url": "perkeep://access"}, http.StatusBadRequest, nil)
}

// fakePerkeep serves a discovery document to requests with the given
// basic auth password.
func fakePerkeep(password string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, pw, _ := r.BasicAuth(); pw != password {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if r.URL.Path != "/" || r.Header.Get("Accept") != "text/x-camli-configuration" {
			http.Error(w, "not discovery", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/javascript")
		w.Write([]byte(`{"blobRoot": "/bs/", "ownerName": "Test Owner",
			"client": {"serverName": "pk.example.com", "accessKeyName": "My phone", "permissions": ["upload", "stats"]},
			"storage": {"approxBlobs": 1234, "approxBytes": 5678}}`))
	})
}

func TestCheck(t *testing.T) {
	ts := httptest.NewTLSServer(fakePerkeep("sekrit"))
	defer ts.Close()
	sum := sha256.Sum256(ts.Certificate().Raw)
	fp := hex.EncodeToString(sum[:])
	html := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("<html>hi</html>"))
	}))
	defer html.Close()

	s := newServer()
	tests := []struct {
		name        string
		c           conn
		wantOK      bool
		wantStatus  int
		wantMessage string
	}{
		{"pinned", conn{Server: ts.URL, CertFP: fp, Password: "sekrit"}, true, 200, "Connected."},
		{"wrong pin", conn{Server: ts.URL, CertFP: strings.Repeat("00", 32), Password: "sekrit"}, false, 0, "fingerprint"},
		{"unpinned self-signed", conn{Server: ts.URL, Password: "sekrit"}, false, 0, "certificate"},
		{"wrong key", conn{Server: ts.URL, CertFP: fp, Password: "nope"}, false, 401, "didn't accept"},
		{"not perkeep", conn{Server: html.URL}, false, 200, "not like a Perkeep server"},
		{"bad URL", conn{Server: "ftp://x"}, false, 0, "http or https"},
	}
	for _, tt := range tests {
		var got checkResult
		do(t, s, "POST", "/check", tt.c, http.StatusOK, &got)
		if got.OK != tt.wantOK || got.Status != tt.wantStatus || !strings.Contains(got.Message, tt.wantMessage) {
			t.Errorf("%s: got %+v; want ok=%v, status %d, message containing %q", tt.name, got, tt.wantOK, tt.wantStatus, tt.wantMessage)
		}
		if tt.wantOK {
			if got.OwnerName != "Test Owner" {
				t.Errorf("%s: owner = %q", tt.name, got.OwnerName)
			}
			if c := got.Client; c == nil || c.ServerName != "pk.example.com" || c.AccessKeyName != "My phone" || strings.Join(c.Permissions, ",") != "upload,stats" {
				t.Errorf("%s: client = %+v", tt.name, got.Client)
			}
			if st := got.Storage; st == nil || st.ApproxBlobs != 1234 || st.ApproxBytes != 5678 {
				t.Errorf("%s: storage = %+v", tt.name, got.Storage)
			}
		}
	}
}

func TestCheckTimeout(t *testing.T) {
	unblock := make(chan struct{})
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-unblock
	}))
	defer ts.Close()
	defer close(unblock)

	var got checkResult
	do(t, newServer(), "POST", "/check", map[string]any{"server": ts.URL, "timeout": 0.05}, http.StatusOK, &got)
	if got.OK || !strings.Contains(got.Message, "within 50ms") {
		t.Errorf("got %+v; want timeout after 50ms", got)
	}
}

func TestTailcatAccountFields(t *testing.T) {
	s := newTestServer(t, t.TempDir())
	var a Account
	do(t, s, "POST", "/accounts", accountFields{Tailcat: true}, http.StatusCreated, &a)
	if !a.Tailcat || a.Server != "" || a.Name == "" {
		t.Errorf("tailcat account = %+v", a)
	}
	do(t, s, "POST", "/accounts", accountFields{Tailcat: true, Server: "https://x"}, http.StatusBadRequest, nil)
	do(t, s, "POST", "/accounts", accountFields{Server: "http://x", CertFP: strings.Repeat("ab", 32)}, http.StatusBadRequest, nil)
	do(t, s, "POST", "/accounts", accountFields{Server: "https://x", CertFP: strings.Repeat("ab", 32)}, http.StatusCreated, &a)
	if a.CertFP != strings.Repeat("ab", 32) {
		t.Errorf("certfp = %q", a.CertFP)
	}
}
