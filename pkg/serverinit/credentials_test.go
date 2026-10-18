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

package serverinit

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"go4.org/jsonconfig"
	"perkeep.org/pkg/types/serverconfig"
)

func writeCredential(t *testing.T, name, value string) string {
	t.Helper()
	if err := os.WriteFile(name, []byte(value), 0600); err != nil {
		t.Fatal(err)
	}
	return "@./" + name
}

func credentialBuilder(replica, packed bool) *lowBuilder {
	b := &lowBuilder{
		high: &serverconfig.Config{
			MemoryStorage: true,
			MemoryIndex:   true,
			PackRelated:   packed,
		},
		low: jsonconfig.Obj{"prefixes": make(map[string]any)},
	}
	if replica {
		b.addPrefix("/bs/", "storage-memory", nil)
	}
	return b
}

func configValue(t *testing.T, m map[string]any, keys ...string) any {
	t.Helper()
	var v any = m
	for _, key := range keys {
		obj, ok := v.(map[string]any)
		if !ok {
			t.Fatalf("expected object before %q; got %T", key, v)
		}
		var found bool
		v, found = obj[key]
		if !found {
			t.Fatalf("missing %q in config", key)
		}
	}
	return v
}

func TestS3CredentialFiles(t *testing.T) {
	t.Chdir(t.TempDir())
	access := writeCredential(t, "access.key", " \taccess:from/file\n")
	secret := writeCredential(t, "secret.key", "\r\n@secret:from/file= \n")
	for _, vendor := range []string{"s3", "b2"} {
		for _, replica := range []bool{false, true} {
			for _, packed := range []bool{false, true} {
				for _, tt := range []struct{ name, access, secret, wantAccess, wantSecret string }{
					{"files", access, secret, "access:from/file", "@secret:from/file="},
					{"literal", "access", "valid/secret+key=", "access", "valid/secret+key="},
					{"escaped", "@@access", "@@secret", "@access", "@secret"},
				} {
					t.Run(fmt.Sprintf("%s/replica=%v/packed=%v/%s", vendor, replica, packed, tt.name), func(t *testing.T) {
						b := credentialBuilder(replica, packed)
						if err := b.addS3Config(tt.access+":"+tt.secret+":bucket/subdir:example.com:9000", vendor); err != nil {
							t.Fatal(err)
						}
						prefix := "/bs"
						if replica {
							prefix = "/sto-" + vendor
						}
						suffixes := []string{""}
						if packed {
							if replica {
								prefix += "-bs"
							}
							suffixes = []string{"-loose", "-packed"}
						}
						for _, suffix := range suffixes {
							args := configValue(t, b.low, "prefixes", prefix+suffix+"/", "handlerArgs").(map[string]any)
							if args["aws_access_key"] != tt.wantAccess || args["aws_secret_access_key"] != tt.wantSecret {
								t.Errorf("credentials = %v, %v; want %q, %q", args["aws_access_key"], args["aws_secret_access_key"], tt.wantAccess, tt.wantSecret)
							}
							bucket := "bucket/subdir"
							if packed {
								bucket += "/" + strings.TrimPrefix(suffix, "-")
							}
							if args["bucket"] != bucket || args["hostname"] != "example.com:9000" {
								t.Errorf("bucket/hostname changed: %v", args)
							}
						}
					})
				}
			}
		}
		for _, input := range []string{"@./missing:do-not-log-me:bucket", "access:@./missing:bucket"} {
			err := credentialBuilder(false, false).addS3Config(input, vendor)
			if !errors.Is(err, os.ErrNotExist) || !strings.Contains(err.Error(), vendor) {
				t.Errorf("missing credential file: got %v", err)
			}
			if err != nil && strings.Contains(err.Error(), "do-not-log-me") {
				t.Errorf("credential leaked in error: %v", err)
			}
		}
	}
}

func TestOtherCredentialFiles(t *testing.T) {
	t.Chdir(t.TempDir())
	ref := writeCredential(t, "credential", " \tvalue:with/slashes\n")
	for _, backend := range []string{"googledrive", "googlecloudstorage"} {
		t.Run(backend, func(t *testing.T) {
			b := credentialBuilder(false, false)
			add := b.addGoogleDriveConfig
			if backend == "googlecloudstorage" {
				add = b.addGoogleCloudStorageConfig
			}
			if err := add(ref + ":" + ref + ":" + ref + ":destination/subdir"); err != nil {
				t.Fatal(err)
			}
			auth := configValue(t, b.low, "prefixes", "/bs/", "handlerArgs", "auth").(map[string]any)
			for _, key := range []string{"client_id", "client_secret", "refresh_token"} {
				if auth[key] != "value:with/slashes" {
					t.Errorf("%s = %v", key, auth[key])
				}
			}
			for _, input := range []string{
				"@./missing:secret:refresh:bucket",
				"client:@./missing:refresh:bucket",
				"client:secret:@./missing:bucket",
			} {
				if err := add(input); !errors.Is(err, os.ErrNotExist) {
					t.Errorf("missing credential file: got %v", err)
				}
			}
		})
	}
	for _, backend := range []string{"mysql", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			b := credentialBuilder(false, false)
			b.high.DBUnique = "test"
			args, err := b.dbIndexStorage(backend, "alice@db.example:3306:"+ref, dbIndex)
			if err != nil {
				t.Fatal(err)
			}
			if args["password"] != "value:with/slashes" || args["host"] != "db.example:3306" || args["user"] != "alice" {
				t.Errorf("unexpected database args: %v", args)
			}
			if _, err := b.dbIndexStorage(backend, "alice@db.example:@./missing", dbIndex); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("missing password file: got %v", err)
			}
		})
	}
}

func TestHighLevelCredentialFiles(t *testing.T) {
	base := immichTestConfig(t)
	var err error
	base.IdentitySecretRing, err = filepath.Abs(base.IdentitySecretRing)
	if err != nil {
		t.Fatal(err)
	}
	baseJSON, err := json.Marshal(base)
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(t.TempDir())
	ref := writeCredential(t, "credential", " \tvalue:with/slashes\n")
	for _, tt := range []struct {
		name string
		set  func(*serverconfig.Config, string)
		path []string
	}{
		{"s3", func(c *serverconfig.Config, v string) { c.S3 = "access:" + v + ":bucket" }, []string{"/sto-s3/", "handlerArgs", "aws_secret_access_key"}},
		{"b2", func(c *serverconfig.Config, v string) { c.B2 = "access:" + v + ":bucket:example.com" }, []string{"/sto-b2/", "handlerArgs", "aws_secret_access_key"}},
		{"googledrive", func(c *serverconfig.Config, v string) { c.GoogleDrive = "client:" + v + ":refresh:parent" }, []string{"/sto-googledrive/", "handlerArgs", "auth", "client_secret"}},
		{"googlecloudstorage", func(c *serverconfig.Config, v string) { c.GoogleCloudStorage = "client:" + v + ":refresh:bucket" }, []string{"/sto-googlecloudstorage/", "handlerArgs", "auth", "client_secret"}},
		{"mysql", func(c *serverconfig.Config, v string) {
			c.MemoryIndex = false
			c.MySQL = "alice@db:" + v
		}, []string{"/index/", "handlerArgs", "storage", "password"}},
		{"postgres", func(c *serverconfig.Config, v string) {
			c.MemoryIndex = false
			c.PostgreSQL = "alice@db:" + v
		}, []string{"/index/", "handlerArgs", "storage", "password"}},
		{"immich.password", func(c *serverconfig.Config, v string) { c.Immich.Password = v }, []string{"/immich/api/", "handlerArgs", "password"}},
		{"flickr", func(c *serverconfig.Config, v string) { c.Flickr = v }, []string{"/importer/", "handlerArgs", "flickr", "clientSecret"}},
		{"picasa", func(c *serverconfig.Config, v string) { c.Picasa = v }, []string{"/importer/", "handlerArgs", "picasa", "clientSecret"}},
		{"instapaper", func(c *serverconfig.Config, v string) { c.Instapaper = v }, []string{"/importer/", "handlerArgs", "instapaper", "clientSecret"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var high serverconfig.Config
			if err := json.Unmarshal(baseJSON, &high); err != nil {
				t.Fatal(err)
			}
			tt.set(&high, ref)
			before, err := json.Marshal(&high)
			if err != nil {
				t.Fatal(err)
			}
			low, err := genLowLevelConfig(&high)
			if err != nil {
				t.Fatal(err)
			}
			keys := append([]string{"prefixes"}, tt.path...)
			if got := configValue(t, low.jconf, keys...); got != "value:with/slashes" {
				t.Errorf("credential = %v; want value:with/slashes", got)
			}
			checkConfigUnchanged(t, before, &high)
			tt.set(&high, "@./missing")
			if _, err := genLowLevelConfig(&high); !errors.Is(err, os.ErrNotExist) || !strings.Contains(err.Error(), tt.name) {
				t.Errorf("missing credential file: got %v", err)
			}
		})
	}
	base.Immich.Password = writeCredential(t, "empty", "\n")
	if _, err := genLowLevelConfig(base); err == nil || !strings.Contains(err.Error(), "both email and password") {
		t.Errorf("empty Immich password file: got %v", err)
	}
}

func checkConfigUnchanged(t *testing.T, before []byte, high *serverconfig.Config) {
	t.Helper()
	after, err := json.Marshal(high)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Error("generating a config modified its input")
	}
}
