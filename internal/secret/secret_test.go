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

package secret

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveLiterals(t *testing.T) {
	tests := []struct {
		value string
		want  string
	}{
		{"", ""},
		{"password", "password"},
		{" \tpassword\r\n", " \tpassword\r\n"},
		{"path/to/password", "path/to/password"},
		{"/absolute/path", "/absolute/path"},
		{"./relative/path", "./relative/path"},
		{"password@host", "password@host"},
		{" @not-a-file", " @not-a-file"},
		{"@@", "@"},
		{"@@missing", "@missing"},
		{"@@/missing/path", "@/missing/path"},
		{"@@@missing", "@@missing"},
		{"@@ password\n", "@ password\n"},
	}
	for _, tt := range tests {
		t.Run(tt.value, func(t *testing.T) {
			got, err := Resolve(tt.value)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("Resolve(%q) = %q; want %q", tt.value, got, tt.want)
			}
		})
	}
}

func TestResolveFiles(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	tests := []struct {
		name     string
		contents string
		want     string
	}{
		{"plain", "password", "password"},
		{"newline", "password\n", "password"},
		{"whitespace", "\u2003 \t\r\npass word\tinside\r\n \u2003", "pass word\tinside"},
		{"empty", "", ""},
		{"only-whitespace", " \t\r\n\u2003", ""},
		{"at-prefix", "@missing\n", "@missing"},
		{"escaped-prefix", "@@missing\n", "@@missing"},
		{"auth-options", "pass:word:+localhost:vivify=other\n", "pass:word:+localhost:vivify=other"},
	}
	for _, tt := range tests {
		if err := os.WriteFile(tt.name, []byte(tt.contents), 0600); err != nil {
			t.Fatal(err)
		}
		for _, path := range []string{tt.name, "./" + tt.name, filepath.Join(dir, tt.name)} {
			t.Run(path, func(t *testing.T) {
				got, err := Resolve("@" + path)
				if err != nil {
					t.Fatal(err)
				}
				if got != tt.want {
					t.Errorf("Resolve(%q) = %q; want %q", "@"+path, got, tt.want)
				}
			})
		}
	}
}

func TestResolveReadErrors(t *testing.T) {
	dir := t.TempDir()
	for _, path := range []string{filepath.Join(dir, "missing"), dir, ""} {
		t.Run(path, func(t *testing.T) {
			got, err := Resolve("@" + path)
			if err == nil {
				t.Fatal("Resolve succeeded; want read error")
			}
			if got != "" {
				t.Errorf("Resolve returned %q with an error; want empty", got)
			}
			var pathErr *os.PathError
			if !errors.As(err, &pathErr) {
				t.Fatalf("error %v does not wrap *os.PathError", err)
			}
			if pathErr.Path != path {
				t.Errorf("error path = %q; want %q", pathErr.Path, path)
			}
			if path != dir && !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("error %v does not wrap fs.ErrNotExist", err)
			}
			if !strings.HasPrefix(err.Error(), "reading secret file ") {
				t.Errorf("error %q lacks read context", err)
			}
		})
	}
}
