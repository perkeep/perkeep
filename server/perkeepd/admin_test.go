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
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const adminLowConfig = `{"handlerConfig":true,"auth":"userpass:alice:secret","prefixes":{"/bs/":{"handler":"storage-memory"}}}`

const adminLowDump = `{
  "auth": "userpass:alice:secret",
  "handlerConfig": true,
  "prefixes": {
    "/bs/": {
      "handler": "storage-memory"
    }
  }
}`

func TestRunAdminArguments(t *testing.T) {
	// Help and syntax errors must not even look up the default config.
	// osutil rejects unconfigured host-directory lookups in test binaries.
	t.Setenv("CAMLI_CONFIG_DIR", "")
	t.Setenv("CAMLI_INCLUDE_PATH", "")
	for _, tt := range []struct {
		args string
		code int
		text string
	}{
		{"-h", 0, "Commands:"},
		{"-help", 0, "Commands:"},
		{"--help", 0, "Commands:"},
		{"help", 0, "Commands:"},
		{"help dumpconfig", 0, "not redacted"},
		{"dumpconfig -h", 0, "not redacted"},
		{"dumpconfig -help", 0, "not redacted"},
		{"dumpconfig --help", 0, "not redacted"},
		{"", 2, "Usage:"},
		{"unknown", 2, "unknown command"},
		{"-unknown", 2, "flag provided but not defined"},
		{"help unknown", 2, "unknown command"},
		{"help dumpconfig extra", 2, "at most one command"},
		{"dumpconfig -unknown", 2, "flag provided but not defined"},
		{"dumpconfig -configfile", 2, "flag needs an argument"},
		{"dumpconfig one two", 2, "at most one configuration file"},
		{"dumpconfig -configfile=one two", 2, "not both"},
		{"dumpconfig -configfile= two", 2, "not both"},
		{"-listen=:0 dumpconfig", 2, "flag provided but not defined"},
		{"-configfile=one dumpconfig", 2, "flag provided but not defined"},
		{"dumpconfig -listen=:0", 2, "flag provided but not defined"},
		{"dumpconfig -reindex", 2, "flag provided but not defined"},
	} {
		t.Run(tt.args, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if got := runAdmin(strings.Fields(tt.args), &stdout, &stderr); got != tt.code {
				t.Errorf("status = %d; want %d; stderr: %s", got, tt.code, &stderr)
			}
			if stdout.Len() != 0 || !strings.Contains(stderr.String(), tt.text) {
				t.Errorf("stdout = %q, stderr = %q; want empty stdout and %q in stderr", &stdout, &stderr, tt.text)
			}
		})
	}
}

func adminConfigDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("CAMLI_CONFIG_DIR", dir)
	t.Setenv("CAMLI_INCLUDE_PATH", "")
	return dir
}

func writeAdminConfig(t *testing.T, file, contents string) {
	t.Helper()
	if err := os.WriteFile(file, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestRunAdminDumpConfig(t *testing.T) {
	dir := adminConfigDir(t)
	file := filepath.Join(dir, "server-config.json")
	writeAdminConfig(t, file, adminLowConfig)
	t.Chdir(t.TempDir())
	// The same basename in the working directory must not resolve in dir.
	writeAdminConfig(t, "server-config.json", strings.ReplaceAll(adminLowConfig, "alice:secret", "bob:local-secret"))
	localDump := strings.ReplaceAll(adminLowDump, "alice:secret", "bob:local-secret")
	for _, tt := range []struct {
		args []string
		want string
	}{
		{[]string{"-configfile", "server-config.json"}, localDump},
		{[]string{"server-config.json"}, localDump},
		{nil, adminLowDump},
		{[]string{"-configfile="}, adminLowDump},
		{[]string{"-configfile=" + file}, adminLowDump},
		{[]string{"server-config.json"}, localDump},
	} {
		// Repeated invocations must not retain flag values or Visit state.
		var stdout, stderr bytes.Buffer
		if got := runAdmin(append([]string{"dumpconfig"}, tt.args...), &stdout, &stderr); got != 0 {
			t.Fatalf("dumpconfig %q: status = %d; stderr: %s", tt.args, got, &stderr)
		}
		// Exact bytes also check indentation, no trailing newline, preservation
		// of auth secrets, and removal of jsonconfig's _knownkeys metadata.
		if stdout.String() != tt.want || stderr.Len() != 0 {
			t.Errorf("dumpconfig %q: stdout = %q, stderr = %q; want %q and empty stderr", tt.args, &stdout, &stderr, tt.want)
		}
	}
}

func TestRunAdminDumpConfigErrors(t *testing.T) {
	for _, name := range []string{"missing-default", "missing-explicit", "malformed"} {
		t.Run(name, func(t *testing.T) {
			dir := adminConfigDir(t)
			file := filepath.Join(dir, "server-config.json")
			args := []string{"dumpconfig"}
			if name != "missing-default" {
				file = filepath.Join(dir, "explicit.json")
				args = append(args, file)
			}
			if name == "malformed" {
				writeAdminConfig(t, file, "{")
			}
			var stdout, stderr bytes.Buffer
			if got := runAdmin(args, &stdout, &stderr); got != 1 {
				t.Errorf("status = %d; want 1; stderr: %s", got, &stderr)
			}
			if stdout.Len() != 0 || !strings.Contains(stderr.String(), "perkeepd admin dumpconfig:") {
				t.Errorf("stdout = %q, stderr = %q; want an operation error only on stderr", &stdout, &stderr)
			}
			entries, err := os.ReadDir(dir)
			if err != nil {
				t.Fatal(err)
			}
			wantEntries := 0
			if name == "malformed" {
				wantEntries = 1
				if data, err := os.ReadFile(file); err != nil || string(data) != "{" {
					t.Errorf("malformed input changed: %q, %v", data, err)
				}
			}
			if len(entries) != wantEntries {
				t.Errorf("configuration error created files: %v", entries)
			}
		})
	}
}

func TestDumpConfigHighLevel(t *testing.T) {
	dir := adminConfigDir(t)
	blobPath := filepath.Join(dir, "blobs")
	ring := filepath.Join(dir, "nonexistent-secring.gpg")
	data, err := json.Marshal(map[string]any{
		"auth":               "userpass:alice:secret",
		"blobPath":           blobPath,
		"identity":           "2931A67C26F5ABDA",
		"identitySecretRing": ring,
		"runIndex":           false,
	})
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "high.json")
	writeAdminConfig(t, file, string(data))
	var stdout bytes.Buffer
	if err := dumpConfig(file, &stdout); err != nil {
		t.Fatal(err)
	}
	var low struct {
		HandlerConfig bool
		Auth          string
		Prefixes      map[string]struct {
			Handler     string
			HandlerArgs map[string]any
		}
	}
	if err := json.Unmarshal(stdout.Bytes(), &low); err != nil {
		t.Fatal(err)
	}
	if !low.HandlerConfig || low.Auth != "userpass:alice:secret" || strings.Contains(stdout.String(), `"_knownkeys"`) {
		t.Errorf("invalid low-level dump: %s", &stdout)
	}
	for prefix, path := range map[string]string{"/bs/": blobPath, "/cache/": filepath.Join(blobPath, "cache")} {
		handler := low.Prefixes[prefix]
		if handler.Handler != "storage-filesystem" || handler.HandlerArgs["path"] != path {
			t.Errorf("%s handler = %+v; want filesystem at %q", prefix, handler, path)
		}
	}
	// Conversion may create its cache directory, but must not install the
	// signing handler: its required secret ring deliberately does not exist.
	if h := low.Prefixes["/sighelper/"]; h.Handler != "jsonsign" || h.HandlerArgs["secretRing"] != ring {
		t.Errorf("signing handler = %+v", h)
	}
	if _, err := os.Stat(ring); !os.IsNotExist(err) {
		t.Errorf("secret ring should remain absent; stat error = %v", err)
	}
}

type adminErrorWriter struct{ err error }

func (w adminErrorWriter) Write([]byte) (int, error) { return 0, w.err }

func TestDumpConfigWriterError(t *testing.T) {
	file := filepath.Join(adminConfigDir(t), "low.json")
	writeAdminConfig(t, file, adminLowConfig)
	want := errors.New("dump writer failed")
	if err := dumpConfig(file, adminErrorWriter{want}); !errors.Is(err, want) {
		t.Errorf("dumpConfig error = %v; want %v", err, want)
	}
}
