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
	"strings"
	"testing"
	"time"
)

func runDBInitProcess(t *testing.T, root string, status int, args ...string) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, args...)
	cmd.Dir = root
	// Reuse the existing TestMain CLI entry point without inheriting any
	// production configuration, paths, credentials, or devcam settings.
	cmd.Env = []string{
		"PERKEEPD_ADMIN_TEST_MAIN=1", "GOMAXPROCS=2",
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
	if got := cmd.ProcessState.ExitCode(); got != status || stdout.Len() != 0 {
		t.Fatalf("%q: status %d, want %d (error %v); stdout %q, stderr %q", args, got, status, err, &stdout, &stderr)
	}
	return stderr.String()
}

func TestAdminDBInitProcessArguments(t *testing.T) {
	for _, tt := range []struct {
		args   []string
		status int
		text   string
	}{
		{[]string{"admin", "dbinit", "--help"}, 0, "Usage: perkeepd admin dbinit"},
		{[]string{"admin", "help", "dbinit"}, 0, "Usage: perkeepd admin dbinit"},
		{[]string{"admin", "dbinit"}, 2, "--dbname flag required"},
		{[]string{"admin", "dbinit", "--dbname"}, 2, "flag needs an argument"},
		{[]string{"admin", "dbinit", "--dbname=unused", "--dbtype=invalid"}, 2, "--dbtype flag"},
		{[]string{"admin", "dbinit", "--dbtype=sqlite", "--dbname=unused", "extra"}, 2, "does not accept positional arguments"},
		{[]string{"admin", "dbinit", "--listen=:0"}, 2, "flag provided but not defined"},
		{[]string{"admin", "dbinit", "--configfile=unused"}, 2, "flag provided but not defined"},
		{[]string{"admin", "dbinit", "--dbtype=sqlite", "--dbname=missing/index.sqlite"}, 1, "perkeepd admin dbinit: Error connecting"},
	} {
		t.Run(strings.Join(tt.args, " "), func(t *testing.T) {
			root := t.TempDir()
			stderr := runDBInitProcess(t, root, tt.status, tt.args...)
			if !strings.Contains(stderr, tt.text) {
				t.Errorf("stderr = %q; want %q", stderr, tt.text)
			}
			if entries, err := os.ReadDir(root); err != nil || len(entries) != 0 {
				t.Errorf("CLI created files during help/argument/error handling: %v, %v", entries, err)
			}
		})
	}
}

func TestAdminDBInitProcessSQLite(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "index.sqlite")
	args := []string{"admin", "dbinit", "--dbtype=sqlite", "--dbname=" + file}
	if stderr := runDBInitProcess(t, root, 0, args...); stderr != "" {
		t.Fatalf("creation stderr: %s", stderr)
	}
	checkDBInitSQLite(t, file, 0, 0)
	db := openDBInitSQLite(t, file)
	if _, err := db.Exec("INSERT INTO rows VALUES ('keep-me', 'unchanged')"); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO meta VALUES ('reindexing', '1')"); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if stderr := runDBInitProcess(t, root, 2, args...); !strings.Contains(stderr, "already exists, but --wipe not given") {
		t.Fatalf("refusal stderr: %s", stderr)
	}
	checkDBInitSQLite(t, file, 1, 1)
	if stderr := runDBInitProcess(t, root, 0, append(append([]string{}, args...), "--wipe", "--ignoreexists")...); stderr != "" {
		t.Fatalf("ignoreexists stderr: %s", stderr)
	}
	checkDBInitSQLite(t, file, 1, 1)
	if stderr := runDBInitProcess(t, root, 0, append(args, "--wipe")...); stderr != "" {
		t.Fatalf("wipe stderr: %s", stderr)
	}
	checkDBInitSQLite(t, file, 0, 0)
	if _, err := os.Stat(filepath.Join(root, "config")); !os.IsNotExist(err) {
		t.Errorf("dbinit accessed server configuration: stat error %v", err)
	}
}
