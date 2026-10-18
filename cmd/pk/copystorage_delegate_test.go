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
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"perkeep.org/pkg/cmdmain"
)

const (
	copyStorageDelegateMarker = "--test-copy-storage-delegate"
	copyStorageDelegateStdout = "copy-storage helper stdout\n"
	copyStorageDelegateStderr = "copy-storage helper stderr\n"
	copyStorageDelegateInput  = "stdin for copy-storage\nwith another line\n"
)

type copyStorageDelegateReport struct {
	Args       []string
	ConfigDir  string
	Stdin      string
	Executable string
}

// Re-exec the test executable rather than building a second pk. The private
// argument is removed before cmdmain parses the real command line, so these
// tests exercise Main, command registration, lookup, and argument forwarding.
// No extra environment variables (or production configuration) are needed.
func TestMain(m *testing.M) {
	name := strings.TrimSuffix(strings.ToLower(filepath.Base(os.Args[0])), ".exe")
	switch name {
	case "perkeepd":
		os.Exit(runCopyStorageDelegateHelper())
	case "go":
		// LookPathGopath runs "go env GOPATH" even when GOPATH is set.
		// Keep that lookup hermetic, including when testing a missing helper.
		if reflect.DeepEqual(os.Args[1:], []string{"env", "GOPATH"}) {
			fmt.Fprintln(os.Stdout, os.Getenv("GOPATH"))
			os.Exit(0)
		}
		fmt.Fprintf(os.Stderr, "unexpected fake go arguments: %q\n", os.Args[1:])
		os.Exit(99)
	}
	if len(os.Args) > 1 && os.Args[1] == copyStorageDelegateMarker {
		os.Args = append(os.Args[:1], os.Args[2:]...)
		cmdmain.Main()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func runCopyStorageDelegateHelper() int {
	input, err := io.ReadAll(os.Stdin)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 99
	}
	executable, err := os.Executable()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 99
	}
	fmt.Fprint(os.Stdout, copyStorageDelegateStdout)
	if err := json.NewEncoder(os.Stdout).Encode(copyStorageDelegateReport{
		Args:       os.Args[1:],
		ConfigDir:  os.Getenv("CAMLI_CONFIG_DIR"),
		Stdin:      string(input),
		Executable: executable,
	}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 99
	}
	fmt.Fprint(os.Stderr, copyStorageDelegateStderr)
	for _, arg := range os.Args[1:] {
		if value, ok := strings.CutPrefix(arg, "--test-copy-storage-exit="); ok {
			code, err := strconv.Atoi(value)
			if err != nil || code < 0 || code > 255 {
				return 99
			}
			return code
		}
	}
	return 0
}

type copyStorageDelegateTest struct {
	executable string
	dir        string
	pathBin    string
	goPath     string
	configDir  string
}

func newCopyStorageDelegateTest(t *testing.T) *copyStorageDelegateTest {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	d := &copyStorageDelegateTest{
		executable: executable,
		dir:        dir,
		pathBin:    filepath.Join(dir, "path bin"),
		goPath:     filepath.Join(dir, "go path"),
		configDir:  filepath.Join(dir, "config marker"),
	}
	for _, dir := range []string{d.pathBin, filepath.Join(d.goPath, "bin"), d.configDir} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	// In particular, do not allow fallback to the developer's real GOPATH.
	d.install(t, d.pathBin, "go")
	return d
}

// Hard links avoid copying a large test executable for every subprocess test.
// They can fail across filesystems or on Windows, so keep a copy fallback.
func (d *copyStorageDelegateTest) install(t *testing.T, dir, name string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	dst := filepath.Join(dir, name)
	if err := os.Link(d.executable, dst); err == nil {
		return dst
	}
	in, err := os.Open(d.executable)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0755)
	if err != nil {
		t.Fatal(err)
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		t.Fatal(copyErr)
	}
	if closeErr != nil {
		t.Fatal(closeErr)
	}
	return dst
}

func (d *copyStorageDelegateTest) run(t *testing.T, args ...string) (stdout, stderr string, exitCode int) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), d.executable, append([]string{copyStorageDelegateMarker}, args...)...)
	cmd.Dir = d.dir
	cmd.Env = append(os.Environ(),
		"PATH="+d.pathBin,
		"GOPATH="+d.goPath,
		"CAMLI_CONFIG_DIR="+d.configDir,
	)
	cmd.Stdin = strings.NewReader(copyStorageDelegateInput)
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	err := cmd.Run()
	if err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			t.Fatalf("running pk: %v\nstdout: %s\nstderr: %s", err, &out, &errOut)
		}
		exitCode = exitErr.ExitCode()
	}
	return out.String(), errOut.String(), exitCode
}

func (d *copyStorageDelegateTest) checkHelper(t *testing.T, executable string, wantArgs []string, wantExit int, args ...string) {
	t.Helper()
	wantArgs = append([]string{"admin", "copy-storage"}, wantArgs...)
	stdout, stderr, exitCode := d.run(t, args...)
	if exitCode != wantExit {
		t.Errorf("exit code = %d; want %d\nstdout: %s\nstderr: %s", exitCode, wantExit, stdout, stderr)
	}
	if stderr != copyStorageDelegateStderr {
		t.Errorf("stderr = %q; want %q", stderr, copyStorageDelegateStderr)
	}
	if !strings.HasPrefix(stdout, copyStorageDelegateStdout) {
		t.Fatalf("helper stdout marker missing from %q", stdout)
	}
	var got copyStorageDelegateReport
	if err := json.Unmarshal([]byte(strings.TrimPrefix(stdout, copyStorageDelegateStdout)), &got); err != nil {
		t.Fatalf("decoding helper report: %v\nstdout: %s", err, stdout)
	}
	if !reflect.DeepEqual(got.Args, wantArgs) {
		t.Errorf("helper args = %q; want %q", got.Args, wantArgs)
	}
	if got.ConfigDir != d.configDir {
		t.Errorf("CAMLI_CONFIG_DIR = %q; want %q", got.ConfigDir, d.configDir)
	}
	// Do not skip these assertions on Windows: cmdmain's non-exec fallback
	// must preserve stdin and the helper's exit status just like syscall.Exec.
	if got.Stdin != copyStorageDelegateInput {
		t.Errorf("helper stdin = %q; want %q", got.Stdin, copyStorageDelegateInput)
	}
	gotPath, err := filepath.EvalSymlinks(got.Executable)
	if err != nil {
		t.Fatal(err)
	}
	wantPath, err := filepath.EvalSymlinks(executable)
	if err != nil {
		t.Fatal(err)
	}
	samePath := gotPath == wantPath
	if runtime.GOOS == "windows" {
		samePath = strings.EqualFold(gotPath, wantPath)
	}
	if !samePath {
		t.Errorf("helper executable = %q; want %q", gotPath, wantPath)
	}
}

func TestCopyStorageDelegateArguments(t *testing.T) {
	for _, tt := range []struct {
		name       string
		beforeMode []string
		modeArgs   []string
		wantArgs   []string
		exitCode   int
	}{
		{
			name:       "global and unknown mode flags",
			beforeMode: []string{"--verbose"},
			modeArgs: []string{
				"--test-copy-storage-unknown=not a pk flag",
				"-n", "-workers=3", "-limit", "7", "/source prefix/",
			},
		},
		{
			name:       "global end of flags is not forwarded",
			beforeMode: []string{"--"},
			modeArgs:   []string{"--dst-config=destination config.json", "/store/"},
			wantArgs:   []string{"--dst-config=destination config.json", "/store/"},
		},
		{
			name:       "global end of flags preserves mode end of flags",
			beforeMode: []string{"--verbose", "--"},
			modeArgs:   []string{"--dst-config=destination config.json", "--", "-prefix"},
			wantArgs:   []string{"--verbose", "--dst-config=destination config.json", "--", "-prefix"},
		},
		{
			name:     "help belongs to helper",
			modeArgs: []string{"--help"},
		},
		{
			name:     "short help belongs to helper",
			modeArgs: []string{"-h"},
		},
		{
			name:     "end of flags and positional arguments",
			modeArgs: []string{"--", "/source prefix/", "another argument", "-not-a-flag"},
		},
		{
			name:     "nonzero helper exit",
			modeArgs: []string{"--test-copy-storage-exit=23", "/source/"},
			exitCode: 23,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			d := newCopyStorageDelegateTest(t)
			helper := d.install(t, d.pathBin, "perkeepd")
			args := append([]string{}, tt.beforeMode...)
			args = append(args, "copy-storage")
			args = append(args, tt.modeArgs...)
			wantArgs := tt.wantArgs
			if wantArgs == nil {
				wantArgs = append(append([]string{}, tt.beforeMode...), tt.modeArgs...)
			}
			d.checkHelper(t, helper, wantArgs, tt.exitCode, args...)
		})
	}
}

func TestCopyStorageDelegatePathsWithSpaces(t *testing.T) {
	d := newCopyStorageDelegateTest(t)
	helper := d.install(t, d.pathBin, "perkeepd")
	args := []string{
		"--verbose",
		"-src-config", filepath.Join(d.dir, "source config.json"),
		"-dst-config=" + filepath.Join(d.dir, "destination config.json"),
		"-dst-prefix", "/destination prefix/",
		"/source prefix/",
	}
	pkArgs := append([]string{"--verbose", "copy-storage"}, args[1:]...)
	d.checkHelper(t, helper, args, 0, pkArgs...)
}

func TestCopyStorageDelegateLookup(t *testing.T) {
	t.Run("PATH before GOPATH bin", func(t *testing.T) {
		d := newCopyStorageDelegateTest(t)
		pathHelper := d.install(t, d.pathBin, "perkeepd")
		d.install(t, filepath.Join(d.goPath, "bin"), "perkeepd")
		d.checkHelper(t, pathHelper, []string{"/source/"}, 0, "copy-storage", "/source/")
	})
	t.Run("GOPATH bin fallback", func(t *testing.T) {
		d := newCopyStorageDelegateTest(t)
		helper := d.install(t, filepath.Join(d.goPath, "bin"), "perkeepd")
		d.checkHelper(t, helper, []string{"/source/"}, 0, "copy-storage", "/source/")
	})
	t.Run("missing helper", func(t *testing.T) {
		d := newCopyStorageDelegateTest(t)
		stdout, stderr, exitCode := d.run(t, "copy-storage", "/source/")
		if exitCode != 2 {
			t.Errorf("exit code = %d; want 2\nstderr: %s", exitCode, stderr)
		}
		if stdout != "" {
			t.Errorf("stdout = %q; want empty", stdout)
		}
		if !strings.Contains(stderr, "Error:") || !strings.Contains(stderr, "perkeepd") {
			t.Errorf("missing-helper diagnostic must identify perkeepd: %q", stderr)
		}
		if !strings.Contains(stderr, filepath.Join(d.goPath, "bin", "perkeepd")) {
			t.Errorf("missing-helper diagnostic must identify the attempted GOPATH/bin location: %q", stderr)
		}
		if strings.Contains(stderr, "panic:") || strings.Contains(stderr, "Usage:") {
			t.Errorf("missing helper should report a lookup error, not panic or parse mode arguments: %q", stderr)
		}
	})
}

func TestCopyStorageDelegateListedWithoutHelper(t *testing.T) {
	d := newCopyStorageDelegateTest(t)
	stdout, stderr, exitCode := d.run(t, "--help")
	// cmdmain's global help, like its other usage output, exits with status 1.
	if exitCode != 1 {
		t.Errorf("exit code = %d; want 1\nstderr: %s", exitCode, stderr)
	}
	if stdout != "" {
		t.Errorf("stdout = %q; want empty", stdout)
	}
	if !strings.Contains(stderr, "Usage:") || !strings.Contains(stderr, "  copy-storage: ") {
		t.Errorf("global help must list copy-storage without looking up its helper:\n%s", stderr)
	}
	if strings.Contains(stderr, "Error:") || strings.Contains(stderr, "panic:") {
		t.Errorf("global help should not fail to find the helper:\n%s", stderr)
	}
}
