//go:build ignore

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
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestBuildTags(t *testing.T) {
	for _, tt := range []struct {
		arch string
		want string
	}{
		{"386", "nodynamic"},
		{"arm", "nodynamic"},
		{"mips", "nodynamic"},
		{"mipsle", "nodynamic"},
		{"amd64", ""},
		{"arm64", ""},
		{"mips64", ""},
		{"mips64le", ""},
		{"ppc64", ""},
		{"ppc64le", ""},
		{"riscv64", ""},
		{"s390x", ""},
		{"loong64", ""},
		{"wasm", ""},
	} {
		t.Run(tt.arch, func(t *testing.T) {
			if got := buildTags(tt.arch, false); got != tt.want {
				t.Errorf("buildTags(%q, false) = %q; want %q", tt.arch, got, tt.want)
			}
			want := "netgo,osusergo"
			if tt.want != "" {
				want += "," + tt.want
			}
			if got := buildTags(tt.arch, true); got != want {
				t.Errorf("buildTags(%q, true) = %q; want %q", tt.arch, got, want)
			}
		})
	}
}

func TestInstallCopyStorageWrapper(t *testing.T) {
	srcRoot, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		goos   string
		name   string
		server string
		mode   os.FileMode
	}{
		{"linux", "pk-copy-storage", "perkeepd", 0755},
		{"windows", "pk-copy-storage.cmd", "perkeepd.exe", 0644},
	} {
		t.Run(tt.goos, func(t *testing.T) {
			binDir := filepath.Join(t.TempDir(), "bin with spaces")
			if err := os.MkdirAll(binDir, 0755); err != nil {
				t.Fatal(err)
			}
			dst := filepath.Join(binDir, tt.name)
			// A CLI-only build must not install a dangling wrapper.
			if err := installCopyStorageWrapper(srcRoot, binDir, tt.goos); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(dst); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("wrapper without server: stat error = %v; want not exist", err)
			}
			// A preexisting daemon suffices, regardless of the build targets.
			if err := os.WriteFile(filepath.Join(binDir, tt.server), []byte("server"), 0755); err != nil {
				t.Fatal(err)
			}
			// Installing over an existing file must also correct its mode.
			if err := os.WriteFile(dst, []byte("old wrapper"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := installCopyStorageWrapper(srcRoot, binDir, tt.goos); err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(dst)
			if err != nil {
				t.Fatal(err)
			}
			want, err := os.ReadFile(filepath.Join(srcRoot, "cmd", "pk-copy-storage", tt.name))
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, want) {
				t.Errorf("installed wrapper = %q; want %q", got, want)
			}
			info, err := os.Stat(dst)
			if err != nil {
				t.Fatal(err)
			}
			if runtime.GOOS != "windows" && info.Mode().Perm() != tt.mode {
				t.Errorf("installed mode = %v; want %v", info.Mode().Perm(), tt.mode)
			}
			entries, err := os.ReadDir(binDir)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 2 {
				t.Errorf("found %d files; want server and %s", len(entries), tt.name)
			}
		})
	}
}

func TestInstallCopyStorageWrapperServerDirectory(t *testing.T) {
	binDir := t.TempDir()
	if err := os.Mkdir(filepath.Join(binDir, "perkeepd"), 0755); err != nil {
		t.Fatal(err)
	}
	err := installCopyStorageWrapper(".", binDir, "linux")
	if err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("install error = %v; want non-regular server error", err)
	}
	if _, err := os.Stat(filepath.Join(binDir, "pk-copy-storage")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("wrapper without server binary: stat error = %v; want not exist", err)
	}
}

func TestCopyStorageWrapperArgumentsAndStatus(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX wrapper requires /bin/sh")
	}
	srcRoot, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	binDir := filepath.Join(t.TempDir(), "bin with spaces")
	if err := os.MkdirAll(binDir, 0755); err != nil {
		t.Fatal(err)
	}
	server := "#!/bin/sh\nprintf '%s\\000' \"$@\"\nexit 37\n"
	if err := os.WriteFile(filepath.Join(binDir, "perkeepd"), []byte(server), 0755); err != nil {
		t.Fatal(err)
	}
	if err := installCopyStorageWrapper(srcRoot, binDir, "linux"); err != nil {
		t.Fatal(err)
	}
	args := []string{"-src-config", "a path/old.json", "", "quote'\"$;*.json", "--", "slash\\path"}
	out, err := exec.Command(filepath.Join(binDir, "pk-copy-storage"), args...).Output()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 37 {
		t.Fatalf("wrapper error = %v; want exit status 37", err)
	}
	want := strings.Join(append([]string{"admin", "copy-storage"}, args...), "\x00") + "\x00"
	if string(out) != want {
		t.Errorf("wrapper arguments = %q; want %q", out, want)
	}
}
