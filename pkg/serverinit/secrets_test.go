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

package serverinit

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"go4.org/jsonconfig"
	"perkeep.org/pkg/auth"
	"perkeep.org/pkg/client"
	"perkeep.org/pkg/types/camtypes"
	"perkeep.org/pkg/types/serverconfig"
)

func TestSecretsGeneratedConfig(t *testing.T) {
	file := filepath.Join(t.TempDir(), "private", "credentials.sqlite")
	tests := []struct {
		name      string
		configure func(*serverconfig.Config)
		wantArgs  map[string]any
	}{
		{
			name:     "memory server",
			wantArgs: map[string]any{"memory": true},
		},
		{
			name: "local blobs with memory index still use disk secrets",
			configure: func(c *serverconfig.Config) {
				c.MemoryStorage = false
				c.BlobPath = t.TempDir()
			},
			wantArgs: map[string]any{},
		},
		{
			name: "remote blobs still use disk secrets",
			configure: func(c *serverconfig.Config) {
				c.MemoryStorage = false
				c.S3 = "access:secret:bucket"
			},
			wantArgs: map[string]any{},
		},
		{
			name: "explicit file overrides memory storage",
			configure: func(c *serverconfig.Config) {
				c.SecretsFile = file
			},
			wantArgs: map[string]any{"file": file},
		},
		{
			name: "explicit file with local blobs",
			configure: func(c *serverconfig.Config) {
				c.MemoryStorage = false
				c.BlobPath = t.TempDir()
				c.SecretsFile = file
			},
			wantArgs: map[string]any{"file": file},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			high := immichTestConfig(t)
			high.Immich = nil
			if tt.configure != nil {
				tt.configure(high)
			}
			low, err := genLowLevelConfig(high)
			if err != nil {
				t.Fatal(err)
			}
			if err := (&jsonconfig.ConfigParser{}).CheckTypes(low.jconf); err != nil {
				t.Fatalf("generated config has invalid JSON types: %v", err)
			}
			prefixes := low.jconf["prefixes"].(map[string]any)
			store, ok := prefixes["/secrets/"].(map[string]any)
			if !ok {
				t.Fatal("missing /secrets/ handler")
			}
			if store["handler"] != "secrets" {
				t.Fatalf("/secrets/ handler = %v; want secrets", store["handler"])
			}
			if got := store["handlerArgs"]; !reflect.DeepEqual(got, tt.wantArgs) {
				t.Errorf("secret handler args = %#v; want %#v", got, tt.wantArgs)
			}
			for _, prefix := range []string{"/", "/importer/"} {
				args := prefixes[prefix].(map[string]any)["handlerArgs"].(map[string]any)
				if got := args["secretRoot"]; got != "/secrets/" {
					t.Errorf("%s secretRoot = %v; want /secrets/", prefix, got)
				}
			}
		})
	}
}

func TestEnsureSecretStore(t *testing.T) {
	tests := []struct {
		name, config, wantErr string
		wantRoot              string
		wantInjected          bool
	}{
		{
			name:   "no importers",
			config: `{"/":{"handler":"root"}}`,
		},
		{
			name:   "disabled importer",
			config: `{"/":{"handler":"root"},"/importer/":{"handler":"importer","enabled":false}}`,
		},
		{
			name:         "old config gets durable default",
			config:       `{"/":{"handler":"root"},"/importer/":{"handler":"importer"}}`,
			wantRoot:     "/secrets/",
			wantInjected: true,
		},
		{
			name:     "existing custom store",
			config:   `{"/":{"handler":"root","handlerArgs":{"secretRoot":""}},"/importer/":{"handler":"importer"},"/credentials/":{"handler":"secrets","handlerArgs":{"file":"custom.sqlite"}}}`,
			wantRoot: "/credentials/",
		},
		{
			name:    "disabled default",
			config:  `{"/importer/":{"handler":"importer"},"/secrets/":{"handler":"secrets","enabled":false}}`,
			wantErr: "/secrets/ is disabled or used by another handler",
		},
		{
			name:    "default prefix collision",
			config:  `{"/importer/":{"handler":"importer"},"/secrets/":{"handler":"root"}}`,
			wantErr: "/secrets/ is disabled or used by another handler",
		},
		{
			name:    "ambiguous importer",
			config:  `{"/":{"handler":"root","handlerArgs":{"secretRoot":"/one/"}},"/importer/":{"handler":"importer"},"/one/":{"handler":"secrets"},"/two/":{"handler":"secrets"}}`,
			wantErr: "multiple secret stores",
		},
		{
			name:    "ambiguous discovery root",
			config:  `{"/":{"handler":"root"},"/importer/":{"handler":"importer","handlerArgs":{"secretRoot":"/two/"}},"/one/":{"handler":"secrets"},"/two/":{"handler":"secrets"}}`,
			wantErr: "multiple secret stores",
		},
		{
			name:   "multiple stores with explicit references",
			config: `{"/":{"handler":"root","handlerArgs":{"secretRoot":"/one/"}},"/importer/":{"handler":"importer","handlerArgs":{"secretRoot":"/two/"}},"/one/":{"handler":"secrets"},"/two/":{"handler":"secrets"}}`,
		},
		{
			name:    "malformed importer args",
			config:  `{"/importer/":{"handler":"importer","handlerArgs":"not an object"}}`,
			wantErr: "handlerArgs is not an object",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var prefixes jsonconfig.Obj
			if err := json.Unmarshal([]byte(tt.config), &prefixes); err != nil {
				t.Fatal(err)
			}
			before, err := json.Marshal(prefixes)
			if err != nil {
				t.Fatal(err)
			}
			err = ensureSecretStore(prefixes)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("ensureSecretStore error = %v; want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if tt.wantRoot == "" {
				after, err := json.Marshal(prefixes)
				if err != nil {
					t.Fatal(err)
				}
				if string(after) != string(before) {
					t.Errorf("unnecessary config changes: got %s; want %s", after, before)
				}
				return
			}
			for _, prefix := range []string{"/", "/importer/"} {
				args := prefixes[prefix].(map[string]any)["handlerArgs"].(map[string]any)
				if got := args["secretRoot"]; got != tt.wantRoot {
					t.Errorf("%s secretRoot = %v; want %s", prefix, got, tt.wantRoot)
				}
			}
			if tt.wantInjected {
				want := map[string]any{"handler": "secrets", "handlerArgs": map[string]any{}}
				if got := prefixes["/secrets/"]; !reflect.DeepEqual(got, want) {
					t.Errorf("injected handler = %#v; want durable default %#v", got, want)
				}
			} else if _, exists := prefixes["/secrets/"]; exists {
				t.Error("injected an unnecessary default store alongside the custom store")
			}
		})
	}
}

// startSecretsServer exercises the real configuration loader, handler wiring,
// discovery, and HTTP client, rather than constructing the API handler directly.
func startSecretsServer(t *testing.T, data []byte) (*client.Client, *httptest.Server, *Config, func()) {
	t.Helper()
	conf, err := Load(data)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	ts := httptest.NewServer(mux)
	shutdown, err := conf.InstallHandlers(mux, ts.URL)
	if err != nil {
		ts.Close()
		t.Fatal(err)
	}
	var once sync.Once
	closeServer := func() {
		once.Do(func() {
			ts.Close()
			if err := shutdown.Close(); err != nil {
				t.Errorf("shutting down handlers: %v", err)
			}
		})
	}
	t.Cleanup(closeServer)
	c, err := client.New(client.OptionNoExternalConfig(), client.OptionServer(ts.URL),
		client.OptionAuthMode(auth.NewBasicAuth("perkeep", "separate-password")))
	if err != nil {
		t.Fatal(err)
	}
	return c, ts, conf, closeServer
}

func secretsServerConfig(t *testing.T) *serverconfig.Config {
	t.Helper()
	t.Setenv("CAMLI_CONFIG_DIR", t.TempDir())
	t.Setenv("CAMLI_VAR_DIR", filepath.Join(t.TempDir(), "private"))
	t.Cleanup(func() { auth.SetMode(auth.None{}) })
	high := immichTestConfig(t)
	high.Immich = nil
	high.Listen = ":3179"
	return high
}

func secretConfigJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestSecretsPersistAcrossHandlerRestart(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		name := "explicit secretsFile with memory blobs"
		if legacy {
			name = "legacy low level importer gets durable default"
		}
		t.Run(name, func(t *testing.T) {
			high := secretsServerConfig(t)
			file := filepath.Join(t.TempDir(), "private", "credentials.sqlite")
			high.SecretsFile = file
			data := secretConfigJSON(t, high)
			if legacy {
				low, err := genLowLevelConfig(high)
				if err != nil {
					t.Fatal(err)
				}
				prefixes := low.jconf["prefixes"].(map[string]any)
				delete(prefixes, "/secrets/")
				for _, prefix := range []string{"/", "/importer/"} {
					delete(prefixes[prefix].(map[string]any)["handlerArgs"].(map[string]any), "secretRoot")
				}
				low.jconf["handlerConfig"] = true
				data = secretConfigJSON(t, low.jconf)
				file = filepath.Join(os.Getenv("CAMLI_VAR_DIR"), "secrets.sqlite")
			}
			c, _, conf, closeServer := startSecretsServer(t, data)
			if legacy {
				prefixes := conf.jconf["prefixes"].(map[string]any)
				for _, prefix := range []string{"/", "/importer/"} {
					args := prefixes[prefix].(map[string]any)["handlerArgs"].(map[string]any)
					if args["secretRoot"] != "/secrets/" {
						t.Fatalf("%s not wired to the injected durable store", prefix)
					}
				}
			}
			ctx := context.Background()
			const secretName = "server/test-token"
			const secretValue = "restart-secret\nwith-unicode-\u2603"
			meta, err := c.PutSecret(ctx, secretName, secretValue, client.SecretPutOptions{ExpectedRevision: 0})
			if err != nil {
				t.Fatal(err)
			}
			closeServer()
			if fi, err := os.Stat(file); err != nil || fi.Size() == 0 {
				t.Fatalf("durable secret database missing or empty: %v", err)
			}
			c, ts, _, _ := startSecretsServer(t, data)
			if root, err := c.SecretRoot(); err != nil || root != ts.URL+"/secrets/" {
				t.Fatalf("SecretRoot = %q, %v; want restarted server's /secrets/", root, err)
			}
			got, err := c.GetSecret(ctx, secretName)
			if err != nil {
				t.Fatal(err)
			}
			if got.Value != secretValue || !reflect.DeepEqual(got.SecretMetadata, meta) {
				t.Error("secret value or revision metadata changed across handler shutdown/restart")
			}
			list, err := c.ListSecrets(ctx)
			if err != nil || len(list) != 1 || !reflect.DeepEqual(list[0], meta) {
				t.Fatalf("restarted metadata list = %+v, %v; want original entry", list, err)
			}
		})
	}
}

func TestSecretsExplicitMemoryIsTransient(t *testing.T) {
	high := secretsServerConfig(t)
	data := secretConfigJSON(t, high)
	c, _, _, closeServer := startSecretsServer(t, data)
	ctx := context.Background()
	if _, err := c.PutSecret(ctx, "server/memory-test", "transient-value", client.SecretPutOptions{ExpectedRevision: 0}); err != nil {
		t.Fatal(err)
	}
	closeServer()
	c, _, _, _ = startSecretsServer(t, data)
	list, err := c.ListSecrets(ctx)
	if err != nil || len(list) != 0 {
		t.Fatalf("restarted explicit memory store has %d entries, %v; want empty", len(list), err)
	}
	if _, err := os.Stat(filepath.Join(os.Getenv("CAMLI_VAR_DIR"), "secrets.sqlite")); !os.IsNotExist(err) {
		t.Fatalf("explicit memory config unexpectedly opened the default database: %v", err)
	}
}

// Deliberately grant even owner bits from a scoped mode: configuration must
// still not advertise or allow the owner-only API to such a credential.
type scopedSecretsAuth struct{}

func (scopedSecretsAuth) ScopedOnly() {}
func (scopedSecretsAuth) AddAuthHeader(r *http.Request) {
	r.Header.Set("Authorization", "test-scoped-secret-access")
}
func (scopedSecretsAuth) AllowedAccess(r *http.Request) auth.Operation {
	if r.Header.Get("Authorization") == "test-scoped-secret-access" {
		return auth.OpOwner
	}
	return 0
}

func TestSecretsDiscoveryAndAuthorization(t *testing.T) {
	high := secretsServerConfig(t)
	c, ts, _, _ := startSecretsServer(t, secretConfigJSON(t, high))
	auth.AddMode(scopedSecretsAuth{})
	ctx := context.Background()
	const value = "must-not-leak-in-discovery-or-denials"
	if _, err := c.PutSecret(ctx, "server/auth-test", value, client.SecretPutOptions{ExpectedRevision: 0}); err != nil {
		t.Fatal(err)
	}
	request := func(method, path, body string, mode auth.AuthMode) (int, []byte) {
		t.Helper()
		req, err := http.NewRequest(method, ts.URL+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		if mode != nil {
			mode.AddAuthHeader(req)
		}
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		res, err := ts.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		data, err := io.ReadAll(res.Body)
		if err != nil {
			t.Fatal(err)
		}
		return res.StatusCode, data
	}
	owner := auth.NewBasicAuth("perkeep", "separate-password")
	status, body := request(http.MethodGet, "/?camli.mode=config", "", owner)
	var discovery camtypes.Discovery
	if err := json.Unmarshal(body, &discovery); err != nil {
		t.Fatal(err)
	}
	if status != http.StatusOK || discovery.SecretRoot != "/secrets/" || discovery.AuthToken == "" {
		t.Fatal("owner discovery did not advertise the secret root and ordinary UI token")
	}
	token, err := auth.NewTokenAuth(discovery.AuthToken)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name             string
		mode             auth.AuthMode
		owner, discovery bool
	}{
		{name: "owner", mode: owner, owner: true, discovery: true},
		{name: "discovery UI token", mode: token, discovery: true},
		{name: "scoped access", mode: scopedSecretsAuth{}, discovery: true},
		{name: "no credentials"},
		{name: "incorrect owner password", mode: auth.NewBasicAuth("perkeep", "incorrect")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, body := request(http.MethodGet, "/?camli.mode=config", "", tt.mode)
			if tt.discovery {
				var d camtypes.Discovery
				if err := json.Unmarshal(body, &d); err != nil {
					t.Fatal(err)
				}
				if status != http.StatusOK {
					t.Fatalf("discovery status = %d; want 200", status)
				}
				if (d.SecretRoot != "") != tt.owner {
					t.Errorf("secretRoot advertised = %v; want %v", d.SecretRoot != "", tt.owner)
				}
				if !tt.owner && strings.Contains(string(body), `"secretRoot"`) {
					t.Error("non-owner discovery contains a secretRoot field")
				}
			} else if status != http.StatusUnauthorized {
				t.Errorf("unauthorized discovery status = %d; want 401", status)
			}
			if strings.Contains(string(body), value) {
				t.Error("discovery leaked a secret value")
			}
			for _, req := range []struct {
				method, path, body string
				ownerStatus        int
			}{
				{http.MethodGet, "/secrets/", "", http.StatusOK},
				{http.MethodGet, "/secrets/value?name=server%2Fauth-test", "", http.StatusOK},
				{http.MethodPut, "/secrets/value?name=server%2Fauth-test", `{"value":"replacement","expectedRevision":-1}`, http.StatusOK},
				{http.MethodDelete, "/secrets/value?name=server%2Fauth-test", `{"expectedRevision":-1}`, http.StatusNoContent},
			} {
				status, body := request(req.method, req.path, req.body, tt.mode)
				want := http.StatusUnauthorized
				if tt.owner {
					want = req.ownerStatus
				}
				if status != want {
					t.Errorf("%s %s status = %d; want %d", req.method, req.path, status, want)
				}
				if !tt.owner && strings.Contains(string(body), value) {
					t.Error("unauthorized request leaked a secret value")
				}
			}
			if tt.discovery && !tt.owner {
				c, err := client.New(client.OptionNoExternalConfig(), client.OptionServer(ts.URL), client.OptionAuthMode(tt.mode))
				if err != nil {
					t.Fatal(err)
				}
				if _, err := c.SecretRoot(); !errors.Is(err, client.ErrNoSecretRoot) {
					t.Errorf("non-owner client SecretRoot error = %v; want ErrNoSecretRoot", err)
				}
			}
		})
	}
	// A listener grant must remain scoped even if ordinary owner credentials
	// accompany it, and even if it mistakenly includes the owner bits.
	for _, path := range []string{"/?camli.mode=config", "/secrets/"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		owner.AddAuthHeader(req)
		req = req.WithContext(auth.GrantCtxKey.WithValue(req.Context(), auth.OpOwner))
		w := httptest.NewRecorder()
		ts.Config.Handler.ServeHTTP(w, req)
		if path == "/secrets/" {
			if w.Code != http.StatusUnauthorized {
				t.Errorf("listener grant secret status = %d; want 401", w.Code)
			}
		} else if w.Code != http.StatusOK || strings.Contains(w.Body.String(), `"secretRoot"`) {
			t.Error("listener grant discovery must succeed without advertising secretRoot")
		}
	}
}
