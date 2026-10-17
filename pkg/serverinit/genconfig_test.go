/*
Copyright 2014 The Perkeep Authors

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
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"go4.org/jsonconfig"
	"perkeep.org/pkg/types/serverconfig"
)

func TestParseUserHostPass(t *testing.T) {
	tests := []struct {
		in                   string
		user, host, password string
	}{
		{in: "foo"},
		{in: "foo@bar"},
		{"bob@server:pass", "bob", "server", "pass"},
		{"bob@server:3307:pass", "bob", "server:3307", "pass"},
		{"bob@server:pass:word", "bob", "server", "pass:word"},
		{"bob@server:9999999:word", "bob", "server", "9999999:word"},
		{"bob@server:123:123:word", "bob", "server:123", "123:word"},
		{"bob@server:123", "bob", "server", "123"},
		{"bob@server:123:", "bob", "server:123", ""},
	}
	for _, tt := range tests {
		user, host, password, ok := parseUserHostPass(tt.in)
		if ok != (user != "" || host != "" || password != "") {
			t.Errorf("For input %q, inconsistent output %q, %q, %q, %v", tt.in, user, host, password, ok)
			continue
		}
		if user != tt.user || host != tt.host || password != tt.password {
			t.Errorf("parseUserHostPass(%q) = %q, %q, %q; want %q, %q, %q", tt.in, user, host, password, tt.user, tt.host, tt.password)
		}
	}
}

func immichTestConfig(t *testing.T) *serverconfig.Config {
	t.Helper()
	return &serverconfig.Config{
		Auth:               "userpass:perkeep:separate-password",
		Identity:           "2931A67C26F5ABDA",
		IdentitySecretRing: "../jsonsign/testdata/test-secring.gpg",
		MemoryStorage:      true,
		MemoryIndex:        true,
		Immich: &serverconfig.Immich{
			Email:        "photos@example.com",
			Password:     `secret "photo" password`,
			MetadataFile: filepath.Join(t.TempDir(), "immich.leveldb"),
		},
	}
}

func TestImmichConfig(t *testing.T) {
	tests := []struct {
		name      string
		configure func(*serverconfig.Config)
		blobRoot  string
	}{
		{
			name:     "memory with corpus",
			blobRoot: "/bs-and-maybe-also-index/",
		},
		{
			name: "memory without corpus",
			configure: func(c *serverconfig.Config) {
				if err := json.Unmarshal([]byte("false"), &c.CopyIndexToMemory); err != nil {
					t.Fatal(err)
				}
			},
			blobRoot: "/bs-and-maybe-also-index/",
		},
		{
			name: "explicit runIndex",
			configure: func(c *serverconfig.Config) {
				if err := json.Unmarshal([]byte("true"), &c.RunIndex); err != nil {
					t.Fatal(err)
				}
			},
			blobRoot: "/bs-and-maybe-also-index/",
		},
		{
			name: "normal Perkeep auth",
			configure: func(c *serverconfig.Config) {
				c.Immich.Email = ""
				c.Immich.Password = ""
			},
			blobRoot: "/bs-and-maybe-also-index/",
		},
		{
			name: "normal Tailscale auth",
			configure: func(c *serverconfig.Config) {
				c.Auth = "tailscale:owner@example.com"
				c.Immich.Email = ""
				c.Immich.Password = ""
			},
			blobRoot: "/bs-and-maybe-also-index/",
		},
		{
			name: "no auth",
			configure: func(c *serverconfig.Config) {
				c.Auth = "none"
				c.Immich.Email = ""
				c.Immich.Password = ""
			},
			blobRoot: "/bs-and-maybe-also-index/",
		},
		{
			name: "upload limit",
			configure: func(c *serverconfig.Config) {
				c.Immich.MaxUploadBytes = 5 << 30
			},
			blobRoot: "/bs-and-maybe-also-index/",
		},
		{
			name: "local storage",
			configure: func(c *serverconfig.Config) {
				c.MemoryStorage = false
				c.BlobPath = t.TempDir()
			},
			blobRoot: "/bs-and-maybe-also-index/",
		},
		{
			name: "remote storage",
			configure: func(c *serverconfig.Config) {
				c.MemoryStorage = false
				c.S3 = "access:secret:photos"
			},
			blobRoot: "/bs-and-maybe-also-index/",
		},
		{
			name: "disabled",
			configure: func(c *serverconfig.Config) {
				c.Immich = nil
			},
		},
		{
			name: "disabled without index",
			configure: func(c *serverconfig.Config) {
				c.Immich = nil
				c.MemoryIndex = false
				if err := json.Unmarshal([]byte("false"), &c.RunIndex); err != nil {
					t.Fatal(err)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			high := immichTestConfig(t)
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
			v, enabled := prefixes["/immich/api/"]
			if high.Immich == nil {
				if enabled {
					t.Fatal("Immich was enabled without configuration")
				}
				return
			}
			if !enabled {
				t.Fatal("missing /immich/api/ prefix")
			}
			prefix := v.(map[string]any)
			if got := prefix["handler"]; got != "immich" {
				t.Errorf("handler = %v; want immich", got)
			}
			got := prefix["handlerArgs"].(map[string]any)
			want := map[string]any{
				"blobRoot":     tt.blobRoot,
				"jsonSignRoot": "/sighelper/",
				"searchRoot":   "/my-search/",
				"metadata": map[string]any{
					"type": "leveldb",
					"file": high.Immich.MetadataFile,
				},
			}
			if high.Immich.Email != "" {
				want["email"] = high.Immich.Email
				want["password"] = high.Immich.Password
			}
			if high.Immich.MaxUploadBytes != 0 {
				want["maxUploadBytes"] = float64(high.Immich.MaxUploadBytes)
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("handler args = %#v; want %#v", got, want)
			}
			root := prefixes["/"].(map[string]any)["handlerArgs"].(map[string]any)
			if got["blobRoot"] != root["blobRoot"] {
				t.Error("Immich does not use the server's primary ingestion storage")
			}
			if _, ok := prefixes["/sighelper/"]; !ok {
				t.Error("missing signer")
			}
			if _, ok := prefixes["/index/"]; ok != high.RunIndex.Get() {
				t.Errorf("index enabled = %v; want %v", ok, high.RunIndex.Get())
			}
			if got["searchRoot"] != root["searchRoot"] {
				t.Error("Immich does not use the server's native search handler")
			}
			searchPrefix, ok := prefixes["/my-search/"].(map[string]any)
			if !ok {
				t.Fatal("missing native search handler")
			}
			if searchPrefix["handler"] != "search" {
				t.Errorf("search handler = %v; want search", searchPrefix["handler"])
			}
			searchArgs := searchPrefix["handlerArgs"].(map[string]any)
			if searchArgs["index"] != "/index/" {
				t.Errorf("search index = %v; want /index/", searchArgs["index"])
			}
			if slurp, _ := searchArgs["slurpToMemory"].(bool); slurp != high.CopyIndexToMemory.Get() {
				t.Errorf("search corpus enabled = %v; want %v", slurp, high.CopyIndexToMemory.Get())
			}
			limitArgs := jsonconfig.Obj{}
			if limit, ok := got["maxUploadBytes"]; ok {
				limitArgs["maxUploadBytes"] = limit
			}
			limit := limitArgs.OptionalInt64("maxUploadBytes", 32<<30)
			if err := limitArgs.Validate(); err != nil {
				t.Fatalf("upload limit cannot be parsed by handler: %v", err)
			}
			wantLimit := high.Immich.MaxUploadBytes
			if wantLimit == 0 {
				wantLimit = 32 << 30
			}
			if limit != wantLimit {
				t.Errorf("upload limit = %d; want %d", limit, wantLimit)
			}
		})
	}
}

func TestImmichConfigValidation(t *testing.T) {
	tests := []struct {
		name      string
		configure func(*serverconfig.Config)
		wantError string
	}{
		{"email", func(c *serverconfig.Config) { c.Immich.Email = "" }, "immich requires both email and password"},
		{"password", func(c *serverconfig.Config) { c.Immich.Password = "" }, "immich requires both email and password"},
		{"metadata", func(c *serverconfig.Config) { c.Immich.MetadataFile = "" }, "immich requires metadataFile"},
		{"limit", func(c *serverconfig.Config) { c.Immich.MaxUploadBytes = -1 }, "immich maxUploadBytes"},
		{"identity", func(c *serverconfig.Config) { c.Identity = "" }, "immich requires a signer"},
		{"keyring", func(c *serverconfig.Config) { c.IdentitySecretRing = "" }, "immich requires a signer"},
		{
			"no index with standalone credentials",
			func(c *serverconfig.Config) {
				if err := json.Unmarshal([]byte("false"), &c.RunIndex); err != nil {
					t.Fatal(err)
				}
			},
			"immich requires runIndex=true",
		},
		{
			"no index with normal Perkeep auth",
			func(c *serverconfig.Config) {
				c.Immich.Email = ""
				c.Immich.Password = ""
				c.MemoryIndex = false
				if err := json.Unmarshal([]byte("false"), &c.RunIndex); err != nil {
					t.Fatal(err)
				}
			},
			"immich requires runIndex=true",
		},
		{
			"no index and no signer",
			func(c *serverconfig.Config) {
				if err := json.Unmarshal([]byte("false"), &c.RunIndex); err != nil {
					t.Fatal(err)
				}
				c.IdentitySecretRing = ""
			},
			"immich requires a signer",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			high := immichTestConfig(t)
			tt.configure(high)
			if _, err := genLowLevelConfig(high); err == nil || !strings.Contains(err.Error(), tt.wantError) {
				t.Fatalf("error = %v; want substring %q", err, tt.wantError)
			}
		})
	}
}

func TestLoadImmichHighLevelConfig(t *testing.T) {
	t.Setenv("CAMLI_CONFIG_DIR", t.TempDir())
	for _, standalone := range []bool{false, true} {
		name := "normal Perkeep auth"
		if standalone {
			name = "standalone credentials"
		}
		t.Run(name, func(t *testing.T) {
			high := immichTestConfig(t)
			if !standalone {
				high.Immich.Email = ""
				high.Immich.Password = ""
			}
			high.Immich.MaxUploadBytes = 32 << 30
			data, err := json.Marshal(high)
			if err != nil {
				t.Fatal(err)
			}
			low, err := Load(data)
			if err != nil {
				t.Fatal(err)
			}
			prefix, ok := low.jconf["prefixes"].(map[string]any)["/immich/api/"]
			if !ok {
				t.Fatal("high-level immich field was not loaded")
			}
			args := prefix.(map[string]any)["handlerArgs"].(map[string]any)
			if args["searchRoot"] != "/my-search/" {
				t.Errorf("searchRoot = %v; want /my-search/", args["searchRoot"])
			}
			for _, key := range []string{"email", "password"} {
				if _, ok := args[key]; ok != standalone {
					t.Errorf("%s presence = %v; want %v", key, ok, standalone)
				}
			}
		})
	}
}
