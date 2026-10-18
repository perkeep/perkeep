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
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/types/camtypes"
)

func TestCommandArguments(t *testing.T) {
	validRef := blob.RefFromString("file").String()
	for _, args := range [][]string{
		nil,
		{"runkeeper", "export.zip"},
		{"one.zip", "two.zip"},
		{"-fileref=not-a-ref"},
		{"-fileref=" + validRef, "export.zip"},
		{"-inspect", "-detach", "export.zip"},
		{"-unknown"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			var out, diagnostics bytes.Buffer
			err := run(context.Background(), args, &out, &diagnostics)
			if !errors.Is(err, errUsage) {
				t.Fatalf("run(%q) = %v; want usage error", args, err)
			}
			if out.Len() != 0 {
				t.Fatalf("unexpected stdout: %s", &out)
			}
		})
	}
}

func TestCommandHelp(t *testing.T) {
	var out, diagnostics bytes.Buffer
	err := run(context.Background(), []string{"-help"}, &out, &diagnostics)
	if !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("help = %v", err)
	}
	if out.Len() != 0 {
		t.Fatalf("unexpected stdout: %s", &out)
	}
	for _, want := range []string{
		"Usage: pk-import-runkeeper [options] <archive.zip>",
		"-fileref", "-inspect", "-detach", "-json", "-server",
	} {
		if !strings.Contains(diagnostics.String(), want) {
			t.Errorf("help missing %q: %s", want, &diagnostics)
		}
	}
}

func TestCommandInspectConfiguredServer(t *testing.T) {
	if os.Getenv("CAMLI_DISABLE_CLIENT_CONFIG_FILE") != "" {
		// devcam disables client config files for the whole suite. The client
		// caches that setting at package initialization, so changing it in
		// this process cannot enable the temporary config used by this test.
		cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestCommandInspectConfiguredServer$", "-test.v")
		for _, entry := range os.Environ() {
			if !strings.HasPrefix(entry, "CAMLI_DISABLE_CLIENT_CONFIG_FILE=") {
				cmd.Env = append(cmd.Env, entry)
			}
		}
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("config-enabled subprocess: %v\n%s", err, out)
		}
		t.Logf("config-enabled subprocess:\n%s", out)
		return
	}
	ref := blob.RefFromString("uploaded archive")
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if user, pass, ok := r.BasicAuth(); !ok || user != "tester" || pass != "secret" {
			t.Error("missing configured authentication")
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/prefix", "/prefix/":
			w.Header().Set("Content-Type", "text/javascript")
			json.NewEncoder(w).Encode(camtypes.Discovery{BlobRoot: "/prefix/blobs/", ImporterRoot: "/prefix/imports/"})
		case "/prefix/imports/runkeeper/archive":
			var req camtypes.ArchiveImportRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Error(err)
			}
			if r.Method != http.MethodPost || req.Action != "inspect" || req.FileRef != ref {
				t.Errorf("unexpected request: %s %+v", r.Method, req)
			}
			json.NewEncoder(w).Encode(camtypes.ArchiveImportResult{
				Identity: camtypes.ArchiveIdentity{UserName: "runner"},
			})
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()
	configDir := t.TempDir()
	t.Setenv("CAMLI_CONFIG_DIR", configDir)
	t.Setenv("CAMLI_SERVER", "")
	t.Setenv("CAMLI_AUTH", "")
	config := fmt.Sprintf(`{"servers":{"test":{"server":%q,"auth":"userpass:tester:secret","default":true}}}`, ts.URL+"/prefix/")
	if err := os.WriteFile(filepath.Join(configDir, "client-config.json"), []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	for _, server := range []string{"", "test", ts.URL + "/prefix/"} {
		t.Run("server="+server, func(t *testing.T) {
			args := []string{"-inspect", "-json", "-fileref=" + ref.String()}
			if server != "" {
				args = append(args, "-server="+server)
			}
			var out, diagnostics bytes.Buffer
			if err := run(context.Background(), args, &out, &diagnostics); err != nil {
				t.Fatalf("run = %v\n%s", err, &diagnostics)
			}
			var result camtypes.ArchiveImportResult
			if err := json.Unmarshal(out.Bytes(), &result); err != nil || result.Identity.UserName != "runner" {
				t.Fatalf("result = %s, error = %v", &out, err)
			}
		})
	}
}

type fakeArchiveStatus struct{}

func (fakeArchiveStatus) ArchiveImportStatus(context.Context, string, blob.Ref) (*camtypes.ArchiveImportStatus, error) {
	return nil, errors.New("unexpected poll")
}

func TestWaitArchiveImport(t *testing.T) {
	for _, state := range []string{"complete", "error", "interrupted", "unknown", "running"} {
		t.Run(state, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if state == "running" {
				cancel()
			}
			result := &camtypes.ArchiveImportResult{
				AccountRef: blob.RefFromString("account"),
				Status: &camtypes.ArchiveImportStatus{
					FileRef: blob.RefFromString("file"), State: state, Error: "test failure",
					Progress: camtypes.ArchiveImportProgress{
						ItemsTotal: 2, ItemsDone: 2, ItemsUnchanged: 1,
						Warnings: []string{"malformed track"}, WarningCount: 1,
					},
				},
			}
			var out bytes.Buffer
			status, err := waitArchiveImport(ctx, fakeArchiveStatus{}, "runkeeper", result, &out)
			if state == "complete" {
				if err != nil || status != result.Status {
					t.Fatalf("complete = %v, %v", status, err)
				}
			} else if err == nil {
				t.Fatal("expected non-success error")
			}
			if !strings.Contains(out.String(), "2/2 activities (1 unchanged)") || !strings.Contains(out.String(), "Warning: malformed track") {
				t.Errorf("output = %q", out.String())
			}
		})
	}
	if _, err := waitArchiveImport(context.Background(), fakeArchiveStatus{}, "runkeeper", &camtypes.ArchiveImportResult{}, &bytes.Buffer{}); err == nil {
		t.Fatal("accepted missing account/status")
	}
}
