/*
Copyright 2011 Google Inc.

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
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"

	"camlistore.org/pkg/cmdmain"
)

// env is the environment that a camput test runs within.
type env struct {
	// stdin is the standard input, or /dev/null if nil
	stdin io.Reader

	// Timeout optionally specifies the timeout on the command.
	Timeout time.Duration

	// TODO(bradfitz): vfs files.
}

func (e *env) timeout() time.Duration {
	if e.Timeout != 0 {
		return e.Timeout
	}
	return 15 * time.Second

}
func (e *env) Run(args ...string) (out, err []byte, exitCode int) {
	outbuf := new(bytes.Buffer)
	errbuf := new(bytes.Buffer)
	os.Args = append(os.Args[:1], args...)
	cmdmain.Stdout, cmdmain.Stderr = outbuf, errbuf
	exitc := make(chan int, 1)
	cmdmain.Exit = func(code int) {
		exitc <- code
		runtime.Goexit()
	}
	go func() {
		cmdmain.Main()
		cmdmain.Exit(0)
	}()
	select {
	case exitCode = <-exitc:
	case <-time.After(e.timeout()):
		panic("timeout running command")
	}
	out = outbuf.Bytes()
	err = errbuf.Bytes()
	return
}

// TestUsageOnNoargs tests that we output a usage message when given no args, and return
// with a non-zero exit status.
func TestUsageOnNoargs(t *testing.T) {
	var e env
	out, err, code := e.Run()
	if code != 1 {
		t.Errorf("exit code = %d; want 1", code)
	}
	if len(out) != 0 {
		t.Errorf("wanted nothing on stdout; got:\n%s", out)
	}
	if !bytes.Contains(err, []byte("Usage: camput")) {
		t.Errorf("stderr doesn't contain usage. Got:\n%s", err)
	}
}

func TestUploadingChangingDirectory(t *testing.T) {
	// TODO(bradfitz):
	//    $ mkdir /tmp/somedir
	//    $ cp dev-camput /tmp/somedir
	//    $ ./dev-camput  -file /tmp/somedir/ 2>&1 | tee /tmp/somedir/log
	// ... verify it doesn't hang.
	t.Logf("TODO")
}

// Tests that uploads of deep directory trees don't deadlock.
// See commit ee4550bff453526ebae460da1ad59f6e7f3efe77 for backstory
func TestUploadDirectories(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping test in short mode.")
	}
	if v, _ := strconv.ParseBool(os.Getenv("RUN_UPLOAD_DEADLOCK_TEST")); !v {
		// Temporary. For now the test isn't working (failing) reliably.
		// Once the test fails reliably, then we fix.
		t.Skip("skipping test without RUN_UPLOAD_DEADLOCK_TEST=1 in environment")
	}

	debugFlagOnce.Do(registerDebugFlags)

	baseDir, err := ioutil.TempDir("", "")
	if err != nil {
		t.Errorf("error creating temp dir: %v", err)
		return
	}
	defer os.RemoveAll(baseDir)

	uploadRoot := filepath.Join(baseDir, "to_upload")
	mustMkdir(t, uploadRoot, 0700)
	// TODO: make wider trees under uploadRoot, taking care to
	// name dirs and files such that alphabetic statting causes a
	// deadlock.  This isn't sufficient yet:
	dirIter := baseDir
	for i := 0; i < 10; i++ {
		dirPath := filepath.Join(dirIter, "dir")
		mustMkdir(t, dirPath, 0700)
		for _, baseFile := range []string{"file", "FILE.txt"} {
			filePath := filepath.Join(dirPath, baseFile)
			if err := ioutil.WriteFile(filePath, []byte("some file contents "+filePath), 0600); err != nil {
				t.Fatalf("error writing to %s: %v", filePath, err)
			}
			t.Logf("Wrote file %s", filePath)
		}
		dirIter = dirPath
	}
	firstDir := filepath.Join(baseDir, "dir")
	t.Logf("Will upload %s", firstDir)

	defer setAndRestore(&uploadWorkers, 1)()

	e := &env{
		Timeout: 5 * time.Second,
	}
	stdout, stderr, exit := e.Run(
		"--blobdir="+baseDir,
		"--havecache=false",
		"--verbose",
		"file",
		firstDir)
	if exit != 0 {
		t.Fatalf("Exit status %d: stdout=[%s], stderr=[%s]", exit, stdout, stderr)
	}
}

func mustMkdir(t *testing.T, fn string, mode int) {
	if err := os.Mkdir(fn, 0700); err != nil {
		t.Errorf("error creating dir %s: %v", fn, err)
	}
}

func setAndRestore(dst *int, v int) func() {
	old := *dst
	*dst = v
	return func() { *dst = old }
}

func setAndRestoreBool(dst *bool, v bool) func() {
	old := *dst
	*dst = v
	return func() { *dst = old }
}
