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
	"strings"
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
	if e.stdin == nil {
		cmdmain.Stdin = strings.NewReader("")
	} else {
		cmdmain.Stdin = e.stdin
	}
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

// TestCommandUsage tests that we output a command-specific usage message and return
// with a non-zero exit status.
func TestCommandUsage(t *testing.T) {
	var e env
	out, err, code := e.Run("attr")
	if code != 1 {
		t.Errorf("exit code = %d; want 1", code)
	}
	if len(out) != 0 {
		t.Errorf("wanted nothing on stdout; got:\n%s", out)
	}
	sub := "Attr takes 3 args: <permanode> <attr> <value>"
	if !bytes.Contains(err, []byte(sub)) {
		t.Errorf("stderr doesn't contain substring %q. Got:\n%s", sub, err)
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

func testWithTempDir(t *testing.T, fn func(tempDir string)) {
	tempDir, err := ioutil.TempDir("", "")
	if err != nil {
		t.Errorf("error creating temp dir: %v", err)
		return
	}
	defer os.RemoveAll(tempDir)

	confDir := filepath.Join(tempDir, "conf")
	mustMkdir(t, confDir, 0700)
	defer os.Setenv("CAMLI_CONFIG_DIR", os.Getenv("CAMLI_CONFIG_DIR"))
	os.Setenv("CAMLI_CONFIG_DIR", confDir)
	if err := ioutil.WriteFile(filepath.Join(confDir, "client-config.json"), []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}

	debugFlagOnce.Do(registerDebugFlags)

	fn(tempDir)
}

// Tests that uploads of deep directory trees don't deadlock.
// See commit ee4550bff453526ebae460da1ad59f6e7f3efe77 for backstory
func TestUploadDirectories(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping test in short mode.")
	}

	testWithTempDir(t, func(tempDir string) {
		uploadRoot := filepath.Join(tempDir, "to_upload") // read from here
		mustMkdir(t, uploadRoot, 0700)

		blobDestDir := filepath.Join(tempDir, "blob_dest") // write to here
		mustMkdir(t, blobDestDir, 0700)

		// There are 10 stat cache workers. Simulate a slow lookup in
		// the file-based ones (similar to reality), so the
		// directory-based nodes make it to the upload worker first
		// (where it would currently/previously deadlock waiting on
		// children that are starved out) See
		// ee4550bff453526ebae460da1ad59f6e7f3efe77.
		testHookStatCache = func(el interface{}, ok bool) {
			if !ok {
				return
			}
			if ok && strings.HasSuffix(el.(*node).fullPath, ".txt") {
				time.Sleep(50 * time.Millisecond)
			}
		}
		defer func() { testHookStatCache = nil }()

		dirIter := uploadRoot
		for i := 0; i < 2; i++ {
			dirPath := filepath.Join(dirIter, "dir")
			mustMkdir(t, dirPath, 0700)
			for _, baseFile := range []string{"file.txt", "FILE.txt"} {
				filePath := filepath.Join(dirPath, baseFile)
				if err := ioutil.WriteFile(filePath, []byte("some file contents "+filePath), 0600); err != nil {
					t.Fatalf("error writing to %s: %v", filePath, err)
				}
				t.Logf("Wrote file %s", filePath)
			}
			dirIter = dirPath
		}

		// Now set statCacheWorkers greater than uploadWorkers, so the
		// sleep above can re-arrange the order that files get
		// uploaded in, so the directory comes before the file. This
		// was the old deadlock.
		defer setAndRestore(&uploadWorkers, 1)()
		defer setAndRestore(&statCacheWorkers, 5)()

		e := &env{
			Timeout: 5 * time.Second,
		}
		stdout, stderr, exit := e.Run(
			"--blobdir="+blobDestDir,
			"--havecache=false",
			"--verbose=false", // useful to set true for debugging
			"file",
			uploadRoot)
		if exit != 0 {
			t.Fatalf("Exit status %d: stdout=[%s], stderr=[%s]", exit, stdout, stderr)
		}
	})
}

func TestCamputBlob(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping test in short mode.")
	}

	testWithTempDir(t, func(tempDir string) {
		blobDestDir := filepath.Join(tempDir, "blob_dest") // write to here
		mustMkdir(t, blobDestDir, 0700)

		e := &env{
			Timeout: 5 * time.Second,
			stdin:   strings.NewReader("foo"),
		}
		stdout, stderr, exit := e.Run(
			"--blobdir="+blobDestDir,
			"--havecache=false",
			"--verbose=false", // useful to set true for debugging
			"blob", "-")
		if exit != 0 {
			t.Fatalf("Exit status %d: stdout=[%s], stderr=[%s]", exit, stdout, stderr)
		}
		if got, want := string(stdout), "sha1-0beec7b5ea3f0fdbc95d0dd47f3c5bc275da8a33\n"; got != want {
			t.Errorf("Stdout = %q; want %q", got, want)
		}
	})
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
