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

package main

import (
	"encoding/json"
	"flag"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"perkeep.org/internal/osutil"
	"perkeep.org/pkg/auth"
	"perkeep.org/pkg/client"
	"perkeep.org/pkg/types/clientconfig"
)

func TestInitClientConfigFromServer(t *testing.T) {
	oldServer := client.ExplicitServer()
	t.Cleanup(func() {
		if err := flag.Set("server", oldServer); err != nil {
			t.Error(err)
		}
	})
	for _, tt := range []struct {
		name       string
		password   string
		serverAuth string
		badDefault string
	}{
		{name: "file-reference", password: "actual-password", serverAuth: "userpass:server:@./server-only-password"},
		{name: "escape", password: "@not-a-file", serverAuth: "userpass:server:@@not-a-file"},
		{name: "double-escape", password: "@@literal", serverAuth: "basic:server:@./server-only-password"},
		{name: "empty-password", serverAuth: "userpass:server:@./server-only-password"},
		{name: "missing-servers", password: "actual-password", badDefault: "missing-servers"},
		{name: "missing-default", password: "actual-password", badDefault: "missing-default"},
		{name: "null-default", password: "actual-password", badDefault: "null-default"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("CAMLI_CONFIG_DIR", t.TempDir())
			const username = "@literal-user"
			const identity = "2931A67C26F5ABDA"
			downloaded := clientconfig.Config{
				Identity:           identity,
				IdentitySecretRing: "/server/identity-secring.gpg",
				Servers: map[string]*clientconfig.Server{
					"default": {Auth: tt.serverAuth, IsDefault: true, TrustedCerts: []string{"trusted-cert"}},
					"other":   {Server: "https://other.example", Auth: "userpass:other:@./other-password"},
				},
			}
			var discovery, help atomic.Int32
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				user, pass, ok := r.BasicAuth()
				if !ok || user != username || pass != tt.password {
					t.Error("request did not use supplied literal bootstrap credentials")
					http.Error(w, "unauthorized", http.StatusUnauthorized)
					return
				}
				switch r.URL.Path {
				case "/":
					discovery.Add(1)
					w.Header().Set("Content-Type", "text/javascript")
					json.NewEncoder(w).Encode(map[string]string{"blobRoot": "/bs/", "helpRoot": "/help/"})
				case "/help/":
					help.Add(1)
					if r.URL.Query().Get("clientConfig") != "true" {
						t.Error("help request did not ask for client config")
					}
					w.Header().Set("Content-Type", "application/json")
					json.NewEncoder(w).Encode(downloaded)
				default:
					t.Errorf("unexpected request path %q", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer ts.Close()
			downloaded.Servers["default"].Server = ts.URL
			switch tt.badDefault {
			case "missing-servers":
				downloaded.Servers = nil
			case "missing-default":
				downloaded.Servers = map[string]*clientconfig.Server{"other": downloaded.Servers["other"]}
			case "null-default":
				downloaded.Servers["default"] = nil
			}
			var e env
			stdout, stderr, code := e.Run("--server="+ts.URL, "init", "--userpass="+username+":"+tt.password)
			if discovery.Load() != 1 || help.Load() != 1 {
				t.Fatalf("discovery/help requests = %d/%d; want 1/1", discovery.Load(), help.Load())
			}
			if tt.badDefault != "" {
				if code == 0 || !strings.Contains(string(stderr), "without a default server") {
					t.Fatalf("exit %d, stderr %q; want missing-default error", code, stderr)
				}
				if _, err := os.Stat(osutil.UserClientConfigPath()); !os.IsNotExist(err) {
					t.Errorf("invalid downloaded config was saved: %v", err)
				}
				return
			}
			if code != 0 {
				t.Fatalf("exit %d: stdout %q, stderr %q", code, stdout, stderr)
			}
			saved, err := os.ReadFile(osutil.UserClientConfigPath())
			if err != nil {
				t.Fatal(err)
			}
			var got clientconfig.Config
			if err := json.Unmarshal(saved, &got); err != nil {
				t.Fatal(err)
			}
			defaultServer := got.Servers["default"]
			if defaultServer == nil {
				t.Fatal("saved config has no default server")
			}
			escaped := tt.password
			if strings.HasPrefix(escaped, "@") {
				escaped = "@" + escaped
			}
			wantAuth := "userpass:" + username + ":" + escaped
			if defaultServer.Auth != wantAuth {
				t.Errorf("saved auth = %q; want %q", defaultServer.Auth, wantAuth)
			}
			mode, err := auth.FromConfig(defaultServer.Auth)
			if err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest(http.MethodGet, ts.URL, nil)
			mode.AddAuthHeader(req)
			user, pass, ok := req.BasicAuth()
			if !ok || user != username || pass != tt.password {
				t.Error("saved auth does not reproduce supplied literal credentials")
			}
			want := downloaded
			// Only the default server's auth may change during bootstrap.
			want.Servers["default"].Auth = wantAuth
			if !reflect.DeepEqual(got, want) {
				t.Errorf("saved config changed fields other than default auth: got %#v; want %#v", got, want)
			}
		})
	}
}
