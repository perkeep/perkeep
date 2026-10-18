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
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Re-execute the test binary as the real CLI, after all package initialization
// but before testing parses its own flags.
func TestMain(m *testing.M) {
	if os.Getenv("PERKEEPD_ADMIN_TEST_MAIN") == "1" {
		// If dispatch regresses into daemon startup, keep any listener ephemeral
		// and don't launch a browser. Filesystem paths are isolated by the caller.
		*flagListen = "127.0.0.1:0"
		*flagOpenBrowser = false
		main()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestAdminProcess(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		args   []string
		status int
		stderr string
		json   bool
	}{
		{"group-help", []string{"admin", "-help"}, 0, "Commands:", false},
		{"command-help", []string{"admin", "dumpconfig", "-h"}, 0, "Usage: perkeepd admin dumpconfig", false},
		{"help-command", []string{"admin", "help", "dumpconfig"}, 0, "Usage: perkeepd admin dumpconfig", false},
		{"copy-storage-help", []string{"admin", "copy-storage", "--help"}, 0, "Usage: perkeepd admin copy-storage", false},
		{"copy-storage-help-command", []string{"admin", "help", "copy-storage"}, 0, "Usage: perkeepd admin copy-storage", false},
		{"dbinit-help", []string{"admin", "dbinit", "--help"}, 0, "Usage: perkeepd admin dbinit", false},
		{"dbinit-help-command", []string{"admin", "help", "dbinit"}, 0, "Usage: perkeepd admin dbinit", false},
		{"dbinit-server-flag", []string{"admin", "dbinit", "--listen=:0"}, 2, "flag provided but not defined", false},
		{"googinit-help", []string{"admin", "googinit", "--help"}, 0, "Usage: perkeepd admin googinit", false},
		{"googinit-help-command", []string{"admin", "help", "googinit"}, 0, "Usage: perkeepd admin googinit", false},
		{"googinit-missing-type", []string{"admin", "googinit"}, 2, "Invalid storage type", false},
		{"googinit-invalid-type", []string{"admin", "googinit", "--type=other"}, 2, "Invalid storage type", false},
		{"googinit-missing-flag-value", []string{"admin", "googinit", "--type"}, 2, "flag needs an argument", false},
		{"googinit-extra-argument", []string{"admin", "googinit", "--type=cloud", "extra"}, 2, "does not accept positional arguments", false},
		{"googinit-server-flag", []string{"admin", "googinit", "--listen=:0"}, 2, "flag provided but not defined", false},
		{"missing-command", []string{"admin"}, 2, "Usage: perkeepd admin", false},
		{"unknown-command", []string{"admin", "typo"}, 2, `unknown command "typo"`, false},
		{"unknown-flag", []string{"admin", "dumpconfig", "-listen=:0"}, 2, "flag provided but not defined", false},
		{"missing-default-config", []string{"admin", "dumpconfig"}, 1, "server-config.json", false},
		{"missing-explicit-config", []string{"admin", "dumpconfig", "missing.json"}, 1, "missing.json", false},
		{"dump", []string{"admin", "dumpconfig", "input.json"}, 0, "", true},
		{"dump-flag", []string{"admin", "dumpconfig", "-configfile=input.json"}, 0, "", true},
		{"root-typo", []string{"admn", "dumpconfig"}, 2, `unexpected argument "admn"`, false},
		{"root-dumpconfig", []string{"dumpconfig"}, 2, `unexpected argument "dumpconfig"`, false},
		{"root-googinit", []string{"googinit"}, 2, `unexpected argument "googinit"`, false},
		{"root-copy-storage", []string{"copy-storage"}, 2, `unexpected argument "copy-storage"`, false},
		{"root-dbinit", []string{"dbinit"}, 2, `unexpected argument "dbinit"`, false},
		{"server-flags-before-admin", []string{"-configfile=input.json", "admin", "dumpconfig"}, 2, `unexpected argument "admin"`, false},
		{"recovery-before-admin", []string{"-recovery=99", "admin", "dumpconfig"}, 2, `unexpected argument "admin"`, false},
		{"root-delimiter", []string{"--", "admin", "dumpconfig"}, 2, `unexpected argument "admin"`, false},
		{"root-help", []string{"-help"}, 0, "perkeepd admin <command>", false},
		{"root-short-help", []string{"-h"}, 0, "Server options:", false},
		{"root-version", []string{"-version"}, 0, "perkeepd version:", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			// An invalid handler proves that dumping doesn't install handlers.
			config := `{"handlerConfig":true,"auth":"userpass:private:secret","prefixes":{"/bad/":{"handler":"does-not-exist"}}}`
			if err := os.WriteFile(filepath.Join(dir, "input.json"), []byte(config), 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, exe, tt.args...)
			cmd.Dir = dir
			cmd.Env = append(os.Environ(),
				"PERKEEPD_ADMIN_TEST_MAIN=1",
				"CAMLI_CONFIG_DIR="+dir,
				"CAMLI_VAR_DIR="+filepath.Join(dir, "var"),
				"PERKEEP_CACHE_DIR="+filepath.Join(dir, "cache"),
				"CAMLI_CACHE_DIR="+filepath.Join(dir, "cache"),
				"CAMLI_SECRET_RING="+filepath.Join(dir, "secret.gpg"),
				"CAMLI_INCLUDE_PATH=",
				"CAMLI_DEV_CAMLI_ROOT=",
				"HOME="+filepath.Join(dir, "home"),
				"USERPROFILE="+filepath.Join(dir, "home"),
			)
			var stdout, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			err := cmd.Run()
			if ctx.Err() != nil {
				t.Fatalf("CLI timed out (possibly entered daemon startup): %v\nstderr: %s", ctx.Err(), &stderr)
			}
			if cmd.ProcessState == nil {
				t.Fatalf("starting CLI: %v", err)
			}
			if got := cmd.ProcessState.ExitCode(); got != tt.status {
				t.Fatalf("exit status = %d; want %d (error %v)\nstdout: %s\nstderr: %s", got, tt.status, err, &stdout, &stderr)
			}
			if !strings.Contains(stderr.String(), tt.stderr) {
				t.Errorf("stderr = %q; want substring %q", &stderr, tt.stderr)
			}
			if tt.json {
				if !json.Valid(stdout.Bytes()) || !strings.Contains(stdout.String(), `"auth": "userpass:private:secret"`) {
					t.Errorf("stdout is not the unredacted low-level configuration: %s", &stdout)
				}
				if stderr.Len() != 0 {
					t.Errorf("unexpected stderr: %s", &stderr)
				}
			} else if stdout.Len() != 0 {
				t.Errorf("unexpected stdout: %s", &stdout)
			}
			entries, err := os.ReadDir(dir)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 1 || entries[0].Name() != "input.json" {
				t.Errorf("CLI created files or directories during dispatch: %v", entries)
			}
		})
	}
}
