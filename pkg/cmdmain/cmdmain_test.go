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

package cmdmain

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func TestMain(m *testing.M) {
	if len(os.Args) == 3 && os.Args[1] == "--cmdmain-test-helper" {
		code, err := strconv.Atoi(os.Args[2])
		if err != nil {
			panic(err)
		}
		fmt.Fprintln(os.Stderr, "helper stderr")
		if _, err := io.Copy(os.Stdout, os.Stdin); err != nil {
			panic(err)
		}
		os.Exit(code)
	}
	os.Exit(m.Run())
}

type testExecRunner struct {
	path string
	err  error
}

func (c testExecRunner) LookPath() (string, error) { return c.path, c.err }
func (testExecRunner) Usage()                      {}
func (testExecRunner) RunCommand([]string) error   { panic("unexpected RunCommand") }

type testExecArgsRunner struct{ testExecRunner }

func (testExecArgsRunner) ExecArgs(args []string) []string {
	return append([]string{"admin", "copy-storage"}, args...)
}

func TestRunAsNewCommandArgs(t *testing.T) {
	saveExecGlobals(t)
	os.Args = []string{"pk", "--verbose", "--", "copy-storage", "--dst-config=x", "--", "/source/"}
	sysExec = func(bin string, args, env []string) error {
		if bin != "perkeepd" {
			t.Errorf("binary = %q; want perkeepd", bin)
		}
		want := []string{"perkeepd", "admin", "copy-storage", "--verbose", "--dst-config=x", "--", "/source/"}
		if !reflect.DeepEqual(args, want) {
			t.Errorf("args = %q; want %q", args, want)
		}
		return nil
	}
	runAsNewCommand(testExecArgsRunner{testExecRunner{path: "perkeepd"}}, "copy-storage")
}

// These tests replace process-wide command state and must not run in parallel.
func saveExecGlobals(t *testing.T) {
	t.Helper()
	oldArgs, oldExec, oldExit := os.Args, sysExec, Exit
	oldStdin, oldStdout, oldStderr := Stdin, Stdout, Stderr
	t.Cleanup(func() {
		os.Args, sysExec, Exit = oldArgs, oldExec, oldExit
		Stdin, Stdout, Stderr = oldStdin, oldStdout, oldStderr
	})
}

func TestRunAsNewCommandFallback(t *testing.T) {
	bin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, code := range []int{0, 7} {
		t.Run(strconv.Itoa(code), func(t *testing.T) {
			saveExecGlobals(t)
			sysExec = nil // Exercise the Windows fallback on every platform.
			os.Args = []string{"pk", "delegate", "--cmdmain-test-helper", strconv.Itoa(code)}
			const input = "delegated stdin\n"
			Stdin = strings.NewReader(input)
			var stdout, stderr bytes.Buffer
			Stdout, Stderr = &stdout, &stderr
			var exits []int
			Exit = func(code int) { exits = append(exits, code) }

			runAsNewCommand(testExecRunner{path: bin}, "delegate")

			if got := stdout.String(); got != input {
				t.Errorf("stdout = %q; want %q", got, input)
			}
			if got, want := stderr.String(), "helper stderr\n"; got != want {
				t.Errorf("stderr = %q; want %q", got, want)
			}
			var wantExits []int
			if code != 0 {
				wantExits = []int{code}
			}
			if !reflect.DeepEqual(exits, wantExits) {
				t.Errorf("Exit calls = %v; want %v", exits, wantExits)
			}
		})
	}
}

func TestRunAsNewCommandLookupError(t *testing.T) {
	saveExecGlobals(t)
	sysExec = func(string, []string, []string) error {
		t.Fatal("exec called after failed lookup")
		return nil
	}
	var stderr bytes.Buffer
	Stderr = &stderr
	var exits []int
	Exit = func(code int) { exits = append(exits, code) }

	runAsNewCommand(testExecRunner{err: errors.New("command not found")}, "delegate")

	if got, want := stderr.String(), "Error: command not found\n"; got != want {
		t.Errorf("stderr = %q; want %q", got, want)
	}
	if !reflect.DeepEqual(exits, []int{2}) {
		t.Errorf("Exit calls = %v; want [2]", exits)
	}
}

func TestRunAsNewCommandExecError(t *testing.T) {
	saveExecGlobals(t)
	sysExec = nil
	os.Args = []string{"pk", "delegate"}
	bin := filepath.Join(t.TempDir(), "missing-command")
	var stderr bytes.Buffer
	Stderr = &stderr
	var exits []int
	Exit = func(code int) { exits = append(exits, code) }

	runAsNewCommand(testExecRunner{path: bin}, "delegate")

	if got, want := stderr.String(), "Error: running "+bin+": "; !strings.HasPrefix(got, want) {
		t.Errorf("stderr = %q; want prefix %q", got, want)
	}
	if !reflect.DeepEqual(exits, []int{2}) {
		t.Errorf("Exit calls = %v; want [2]", exits)
	}
}

func TestRunExecSysExec(t *testing.T) {
	saveExecGlobals(t)
	bin := filepath.Join("some", "command")
	args := []string{"--flag", "value"}
	env := newCopyEnv()
	wantErr := errors.New("exec failed")
	sysExec = func(gotBin string, gotArgs, gotEnv []string) error {
		if gotBin != bin {
			t.Errorf("binary = %q; want %q", gotBin, bin)
		}
		if want := append([]string{filepath.Base(bin)}, args...); !reflect.DeepEqual(gotArgs, want) {
			t.Errorf("argv = %q; want %q", gotArgs, want)
		}
		if !reflect.DeepEqual(gotEnv, env.flat()) {
			t.Error("exec environment differs from supplied environment")
		}
		return wantErr
	}
	if err := runExec(bin, args, env); err != wantErr {
		t.Errorf("runExec error = %v; want %v", err, wantErr)
	}
}

func TestShiftFlags(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want []string
	}{
		{
			name: "global flags",
			args: []string{"pk", "-verbose", "copy-storage", "--dst-config=x", "/src/"},
			want: []string{"-verbose", "--dst-config=x", "/src/"},
		},
		{
			name: "global terminator",
			args: []string{"pk", "--", "copy-storage", "--dst-config=x", "/src/"},
			want: []string{"--dst-config=x", "/src/"},
		},
		{
			name: "global flags and terminator",
			args: []string{"pk", "-verbose", "--", "copy-storage", "--dst-config=x", "/src/"},
			want: []string{"-verbose", "--dst-config=x", "/src/"},
		},
		{
			name: "command terminator",
			args: []string{"pk", "copy-storage", "--", "-source"},
			want: []string{"--", "-source"},
		},
		{
			name: "global and command terminators",
			args: []string{"pk", "-verbose", "--", "copy-storage", "--", "-source"},
			want: []string{"-verbose", "--", "-source"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			saveExecGlobals(t)
			os.Args = tt.args
			if got := shiftFlags("copy-storage"); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("shiftFlags = %q; want %q", got, tt.want)
			}
		})
	}
}
