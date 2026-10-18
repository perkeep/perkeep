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

package auth

import (
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestFromConfigSecretFiles(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	const password = "secret:with:colons:+localhost:vivify=not-an-option"
	const vivify = "vivi:fy:+localhost:vivify=literal"
	for name, contents := range map[string]string{
		"password":        " \t" + password + "\r\n",
		"vivify=password": "\r\n" + vivify + " \n",
		"empty":           "",
		"whitespace":      " \t\r\n",
		"at-prefix":       "@not-a-file\n",
		"escaped-prefix":  "@@not-a-file\n",
	} {
		if err := os.WriteFile(name, []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
	}
	stringPtr := func(s string) *string { return &s }
	type testCase struct {
		name   string
		config string
		want   AuthMode
	}
	tests := []testCase{
		{
			"userpass",
			"userpass:@literal-user:@./password",
			&UserPass{Username: "@literal-user", Password: password},
		},
		{
			"userpass-options",
			"userpass:alice:@password:+localhost:vivify=@./vivify=password",
			&UserPass{Username: "alice", Password: password, OrLocalhost: true, VivifyPass: stringPtr(vivify)},
		},
		{
			"userpass-options-reversed",
			"userpass:alice:@password:vivify=@./vivify=password:+localhost",
			&UserPass{Username: "alice", Password: password, OrLocalhost: true, VivifyPass: stringPtr(vivify)},
		},
		{
			"basic",
			"basic:@@literal-user:@./password",
			&UserPass{Username: "@@literal-user", Password: password},
		},
		{
			"devauth",
			"devauth:@" + filepath.Join(dir, "password"),
			&DevAuth{Password: password, VivifyPass: stringPtr("vivi" + password)},
		},
		{
			"userpass-escapes",
			"userpass:alice:@@password:vivify=@@vivify",
			&UserPass{Username: "alice", Password: "@password", VivifyPass: stringPtr("@vivify")},
		},
		{
			"basic-escape",
			"basic:alice:@@password",
			&UserPass{Username: "alice", Password: "@password"},
		},
		{
			"devauth-escape",
			"devauth:@@password",
			&DevAuth{Password: "@password", VivifyPass: stringPtr("vivi@password")},
		},
	}
	// Empty files retain the existing empty-password behavior, and @ in file
	// contents remains literal rather than being resolved or unescaped again.
	for _, tt := range []struct {
		file string
		want string
	}{
		{"empty", ""},
		{"whitespace", ""},
		{"at-prefix", "@not-a-file"},
		{"escaped-prefix", "@@not-a-file"},
	} {
		tests = append(tests,
			testCase{
				"userpass-" + tt.file,
				"userpass:alice:@" + tt.file,
				&UserPass{Username: "alice", Password: tt.want},
			},
			testCase{
				"vivify-" + tt.file,
				"userpass:alice:main-password:vivify=@" + tt.file,
				&UserPass{Username: "alice", Password: "main-password", VivifyPass: stringPtr(tt.want)},
			},
			testCase{
				"basic-" + tt.file,
				"basic:alice:@" + tt.file,
				&UserPass{Username: "alice", Password: tt.want},
			},
			testCase{
				"devauth-" + tt.file,
				"devauth:@" + tt.file,
				&DevAuth{Password: tt.want, VivifyPass: stringPtr("vivi" + tt.want)},
			},
		)
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := FromConfig(tt.config)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("FromConfig = %#v; want %#v", got, tt.want)
			}
			req, err := http.NewRequest("GET", "/", nil)
			if err != nil {
				t.Fatal(err)
			}
			got.AddAuthHeader(req)
			if access := got.AllowedAccess(req); access != OpAll {
				t.Errorf("main password access = %v; want %v", access, OpAll)
			}
			var vivifyPass *string
			var username string
			switch mode := got.(type) {
			case *UserPass:
				username, vivifyPass = mode.Username, mode.VivifyPass
			case *DevAuth:
				vivifyPass = mode.VivifyPass
			}
			if vivifyPass != nil {
				req.SetBasicAuth(username, *vivifyPass)
				if access := got.AllowedAccess(req); access != OpVivify {
					t.Errorf("vivify password access = %v; want %v", access, OpVivify)
				}
			}
		})
	}
}

func TestFromConfigSecretReadErrors(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	for _, path := range []string{"missing", ".", ""} {
		for _, tt := range []struct {
			name    string
			config  string
			context string
		}{
			{"userpass", "userpass:private-user:@" + path + ":vivify=private-vivify-secret", "userpass password"},
			{"vivify", "userpass:private-user:private-main-secret:+localhost:vivify=@" + path, "userpass vivify password"},
			{"basic", "basic:private-user:@" + path, "basic password"},
			{"devauth", "devauth:@" + path, "devauth password"},
		} {
			t.Run(tt.name+"/"+path, func(t *testing.T) {
				got, err := FromConfig(tt.config)
				if err == nil {
					t.Fatal("FromConfig succeeded; want read error")
				}
				if got != nil {
					t.Errorf("FromConfig returned %#v with an error; want nil", got)
				}
				if !strings.HasPrefix(err.Error(), tt.context+": ") {
					t.Errorf("error %q lacks context %q", err, tt.context)
				}
				var pathErr *os.PathError
				if !errors.As(err, &pathErr) || pathErr.Path != path {
					t.Errorf("error %v does not wrap *os.PathError for %q", err, path)
				}
				if path != "." && !errors.Is(err, fs.ErrNotExist) {
					t.Errorf("error %v does not wrap fs.ErrNotExist", err)
				}
				for _, sensitive := range []string{tt.config, "private-user", "private-main-secret", "private-vivify-secret"} {
					if strings.Contains(err.Error(), sensitive) {
						t.Errorf("error %q contains auth configuration or credentials %q", err, sensitive)
					}
				}
			})
		}
	}
}

func TestNewBasicAuthLiteralCredentials(t *testing.T) {
	for _, password := range []string{"@missing", "@@literal", " \tpassword/with/slashes\n"} {
		got := NewBasicAuth("@@literal-user", password)
		want := &UserPass{Username: "@@literal-user", Password: password}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("NewBasicAuth = %#v; want %#v", got, want)
		}
	}
}
