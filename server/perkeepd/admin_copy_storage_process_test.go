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
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver"
	"perkeep.org/pkg/blobserver/localdisk"
	"perkeep.org/pkg/test"
)

func runAdminCopyStorageCLI(t *testing.T, root string, wantStatus int, args ...string) (stdout, stderr string) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, append([]string{"admin", "copy-storage"}, args...)...)
	cmd.Dir = root
	// Reuse TestMain in admin_process_test.go to run the actual daemon main.
	// Do not inherit credentials, devcam settings, or paths to real user files.
	// The ordinary path overrides also keep default-config lookup isolated.
	cmd.Env = []string{
		"PERKEEPD_ADMIN_TEST_MAIN=1",
		"PATH=" + os.Getenv("PATH"), "SystemRoot=" + os.Getenv("SystemRoot"),
		"HOME=" + filepath.Join(root, "home"), "USERPROFILE=" + filepath.Join(root, "home"),
		"HOMEDRIVE=" + filepath.VolumeName(root), "HOMEPATH=" + strings.TrimPrefix(filepath.Join(root, "home"), filepath.VolumeName(root)),
		"APPDATA=" + filepath.Join(root, "config"), "LOCALAPPDATA=" + filepath.Join(root, "cache"),
		"XDG_CONFIG_HOME=" + filepath.Join(root, "config"), "XDG_CACHE_HOME=" + filepath.Join(root, "cache"),
		"CAMLI_CONFIG_DIR=" + filepath.Join(root, "config"), "CAMLI_INCLUDE_PATH=" + root,
		"CAMLI_CACHE_DIR=" + filepath.Join(root, "cache"), "PERKEEP_CACHE_DIR=" + filepath.Join(root, "cache"),
		"CAMLI_VAR_DIR=" + filepath.Join(root, "var"), "CAMLI_SECRET_RING=" + filepath.Join(root, "keyring"),
		"TMPDIR=" + root, "TMP=" + root, "TEMP=" + root,
	}
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	err = cmd.Run()
	if ctx.Err() != nil {
		t.Fatalf("CLI timed out (possibly entered daemon startup): %v\nstdout: %s\nstderr: %s", ctx.Err(), &out, &errOut)
	}
	if cmd.ProcessState == nil {
		t.Fatalf("starting CLI: %v", err)
	}
	if status := cmd.ProcessState.ExitCode(); status != wantStatus {
		t.Fatalf("args %q: status %d, want %d (error %v)\nstdout: %s\nstderr: %s", args, status, wantStatus, err, &out, &errOut)
	}
	return out.String(), errOut.String()
}

func TestAdminCopyStorageProcessHelp(t *testing.T) {
	for _, flag := range []string{"--help", "-help", "-h"} {
		t.Run(flag, func(t *testing.T) {
			root := t.TempDir()
			out, stderr := runAdminCopyStorageCLI(t, root, 0, flag)
			want := "Usage: perkeepd admin copy-storage"
			if out != "" || !strings.Contains(stderr, want) {
				t.Fatalf("stdout = %q, stderr = %q; want only stderr containing %q", out, stderr, want)
			}
			entries, err := os.ReadDir(root)
			if err != nil || len(entries) != 0 {
				t.Fatalf("help touched isolated user directories: %v, %v", entries, err)
			}
		})
	}
}

func TestAdminCopyStorageProcessErrorStatuses(t *testing.T) {
	root := t.TempDir()
	src, dst := filepath.Join(root, "missing-source.json"), filepath.Join(root, "missing-destination.json")
	for _, tt := range []struct {
		name   string
		args   []string
		status int
		want   string
	}{
		{"unknown flag", []string{"--no-such-flag"}, 2, "flag provided but not defined"},
		{"server version flag", []string{"--version"}, 2, "flag provided but not defined"},
		{"server listen flag", []string{"--listen=:0"}, 2, "flag provided but not defined"},
		{"bad flag value", []string{"--workers=invalid"}, 2, "invalid value"},
		{"missing flag value", []string{"--dst-config"}, 2, "flag needs an argument"},
		{"missing prefix", nil, 2, "want one PREFIX argument"},
		{"extra prefix", []string{"/source/", "/extra/"}, 2, "want one PREFIX argument"},
		{"missing destination", []string{"/source/"}, 2, "-dst-config is required"},
		{"zero workers", []string{"--dst-config=" + dst, "--workers=0", "/source/"}, 2, "-workers must be at least 1"},
		{"negative workers", []string{"--dst-config=" + dst, "--workers=-1", "/source/"}, 2, "-workers must be at least 1"},
		{"same storage", []string{"--src-config=" + src, "--dst-config=" + src, "/source/"}, 2, "source and destination are the same storage"},
		{"missing default source file", []string{"--dst-config=" + dst, "/source/"}, 1, "perkeepd admin copy-storage: source:"},
		{"missing source file", []string{"--src-config=" + src, "--dst-config=" + dst, "/source/"}, 1, "perkeepd admin copy-storage: source:"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			out, stderr := runAdminCopyStorageCLI(t, root, tt.status, tt.args...)
			if out != "" || !strings.Contains(stderr, tt.want) {
				t.Fatalf("stdout = %q, stderr = %q; want only stderr containing %q", out, stderr, tt.want)
			}
			if tt.status == 2 && !strings.Contains(stderr, "Usage: perkeepd admin copy-storage") {
				t.Errorf("usage error did not print usage: %s", stderr)
			}
		})
	}
}

func TestAdminCopyStorageProcessFilesystem(t *testing.T) {
	root := t.TempDir()
	newStorage := func(name string) *localdisk.DiskStorage {
		t.Helper()
		path := filepath.Join(root, name)
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
		sto, err := localdisk.New(path)
		if err != nil {
			t.Fatal(err)
		}
		return sto
	}
	src, dst := newStorage("source"), newStorage("destination")
	blobs := []*test.Blob{{Contents: "first"}, {Contents: "other"}}
	for _, b := range blobs {
		b.MustUpload(t, src)
	}
	writeConfig := func(file, prefix, storage string) {
		t.Helper()
		data, err := json.Marshal(map[string]any{
			"handlerConfig": true,
			"prefixes": map[string]any{prefix: map[string]any{
				"handler": "storage-filesystem", "handlerArgs": map[string]any{"path": filepath.Join(root, storage)},
			}},
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	// Omitting --src-config must use the isolated default server config.
	writeConfig(filepath.Join(root, "config", "server-config.json"), "/source/", "source")
	dstConfig := filepath.Join(root, "destination.json")
	writeConfig(dstConfig, "/destination/", "destination")
	if out, stderr := runAdminCopyStorageCLI(t, root, 1, "--dst-config="+filepath.Join(root, "missing.json"), "/source/"); out != "" || !strings.Contains(stderr, "perkeepd admin copy-storage: destination:") {
		t.Fatalf("missing destination file: stdout = %q, stderr = %q", out, stderr)
	}
	baseArgs := []string{"--dst-config=" + dstConfig, "--dst-prefix=/destination/", "--workers=2"}
	for _, tt := range []struct {
		name, want string
		extra      []string
		count      int
	}{
		{"dry run sample", "1 source blobs; 1 (5 bytes) missing from the destination\n", []string{"--n", "--limit=1"}, 0},
		{"copy", "2 source blobs; 2 (10 bytes) missing from the destination; copied 2 (10 bytes); 0 failed\n", nil, 2},
		{"rerun", "2 source blobs; 0 (0 bytes) missing from the destination; copied 0 (0 bytes); 0 failed\n", nil, 2},
	} {
		t.Run(tt.name, func(t *testing.T) {
			args := append(append([]string{}, baseArgs...), tt.extra...)
			out, stderr := runAdminCopyStorageCLI(t, root, 0, append(args, "/source/")...)
			if out != tt.want || stderr != "" {
				t.Fatalf("stdout = %q, stderr = %q; want stdout %q", out, stderr, tt.want)
			}
			checkAdminCopyStorage(t, src, blobs)
			checkAdminCopyStorage(t, dst, blobs[:tt.count])
		})
	}
}

func checkAdminCopyStorage(t *testing.T, sto blobserver.Storage, want []*test.Blob) {
	t.Helper()
	count := 0
	if err := blobserver.EnumerateAll(t.Context(), sto, func(sb blob.SizedRef) error {
		count++
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if count != len(want) {
		t.Fatalf("storage has %d blobs, want %d", count, len(want))
	}
	for _, b := range want {
		rc, size, err := sto.Fetch(t.Context(), b.BlobRef())
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(rc)
		closeErr := rc.Close()
		if err != nil || closeErr != nil || size != b.Size() || string(data) != b.Contents || blob.RefFromBytes(data) != b.BlobRef() {
			t.Fatalf("Fetch(%v) = %q, size %d, errors %v/%v; want %q", b.BlobRef(), data, size, err, closeErr, b.Contents)
		}
	}
}
