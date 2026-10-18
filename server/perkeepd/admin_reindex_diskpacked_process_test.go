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
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"perkeep.org/pkg/sorted"
)

func runAdminReindexDiskpackedCLI(t *testing.T, root string, status int, args ...string) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, args...)
	cmd.Dir = root
	// Reuse the existing TestMain re-exec path, without inheriting credentials,
	// developer settings, or paths to a real user's configuration and storage.
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
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err = cmd.Run()
	if ctx.Err() != nil {
		t.Fatalf("CLI timed out (possibly entered daemon startup): %v\nstderr: %s", ctx.Err(), &stderr)
	}
	if cmd.ProcessState == nil {
		t.Fatalf("starting CLI: %v", err)
	}
	if got := cmd.ProcessState.ExitCode(); got != status {
		t.Fatalf("args %q: status = %d, want %d (error %v)\nstdout: %s\nstderr: %s", args, got, status, err, &stdout, &stderr)
	}
	if stdout.Len() != 0 {
		t.Errorf("unexpected stdout: %s", &stdout)
	}
	return stderr.String()
}

func TestAdminReindexDiskpackedProcessArguments(t *testing.T) {
	for _, tt := range []struct {
		args string
		code int
		want string
	}{
		{"admin reindex-diskpacked --help", 0, "Usage: perkeepd admin reindex-diskpacked"},
		{"admin reindex-diskpacked -help", 0, "Usage: perkeepd admin reindex-diskpacked"},
		{"admin reindex-diskpacked -h", 0, "Usage: perkeepd admin reindex-diskpacked"},
		{"admin help reindex-diskpacked", 0, "Usage: perkeepd admin reindex-diskpacked"},
		{"admin reindex-diskpacked --unknown", 2, "flag provided but not defined"},
		{"admin reindex-diskpacked --listen=:0", 2, "flag provided but not defined"},
		{"admin reindex-diskpacked --configfile=other.json", 2, "flag provided but not defined"},
		{"admin reindex-diskpacked --overwrite=invalid", 2, "invalid boolean value"},
		{"admin reindex-diskpacked one two", 2, "at most one path"},
		{"admin reindex-diskpacked", 1, "server-config.json"},
		{"admin reindex-diskpacked unconfigured", 1, "server-config.json"},
		{"reindex-diskpacked", 2, `unexpected argument "reindex-diskpacked"`},
	} {
		t.Run(tt.args, func(t *testing.T) {
			root := t.TempDir()
			stderr := runAdminReindexDiskpackedCLI(t, root, tt.code, strings.Fields(tt.args)...)
			if !strings.Contains(stderr, tt.want) {
				t.Errorf("stderr = %q; want %q", stderr, tt.want)
			}
			entries, err := os.ReadDir(root)
			if err != nil || len(entries) != 0 {
				t.Fatalf("argument handling or missing configuration touched user directories: %v, %v", entries, err)
			}
		})
	}
}

func TestAdminReindexDiskpackedProcessFilesystem(t *testing.T) {
	root := t.TempDir()
	packs := filepath.Join(root, "packs")
	want := writeAdminDiskpackedPack(t, packs)
	index := filepath.Join(packs, "index."+sorted.DefaultKVFileType)
	initial := adminDiskpackedIndexRows(t, index, nil)
	configFile := filepath.Join(root, "config", "server-config.json")
	// A relative handler path remains relative to the working directory, and
	// an unrelated invalid handler proves no handlers are installed.
	prefixes := map[string]any{
		"/packs/": adminDiskpackedHandler("packs", nil),
		"/bad/":   map[string]any{"handler": "does-not-exist"},
	}
	writeAdminDiskpackedConfig(t, configFile, prefixes)
	stderr := runAdminReindexDiskpackedCLI(t, root, 1, "admin", "reindex-diskpacked")
	if !strings.Contains(stderr, "index does not match data") {
		t.Fatalf("check failed for the wrong reason: %s", stderr)
	}
	if got := adminDiskpackedIndexRows(t, index, nil); !reflect.DeepEqual(got, initial) {
		t.Fatal("default check wrote index rows")
	}
	runAdminReindexDiskpackedCLI(t, root, 0, "admin", "reindex-diskpacked", "--overwrite", "packs")
	if got := adminDiskpackedIndexRows(t, index, nil); !reflect.DeepEqual(got, want) {
		t.Fatalf("rebuilt rows = %v; want %v", got, want)
	}
	runAdminReindexDiskpackedCLI(t, root, 0, "admin", "reindex-diskpacked", "--overwrite=false")
	// The path is a selector, not permission to reindex an arbitrary directory.
	stderr = runAdminReindexDiskpackedCLI(t, root, 1, "admin", "reindex-diskpacked", "--overwrite", "unconfigured")
	if !strings.Contains(stderr, "doesn't specify a disk-packed") {
		t.Fatalf("unconfigured path error: %s", stderr)
	}
	// Recovery must not create daemon state or generate another config.
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 2 {
		t.Errorf("unexpected user files: %v, %v", entries, err)
	}
}
