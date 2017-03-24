/*
Copyright 2013 Google Inc.

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

package integration

import (
	"bytes"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"camlistore.org/pkg/osutil"
	"camlistore.org/pkg/test"
)

// mkTmpFIFO makes a fifo in a temporary directory and returns the
// path it and a function to clean-up when done.
func mkTmpFIFO(t *testing.T) (path string, cleanup func()) {
	tdir, err := ioutil.TempDir("", "fifo-test-")
	if err != nil {
		t.Fatalf("iouti.TempDir(): %v", err)
	}
	cleanup = func() {
		os.RemoveAll(tdir)
	}

	path = filepath.Join(tdir, "fifo")
	err = osutil.Mkfifo(path, 0660)
	if err != nil {
		t.Fatalf("osutil.mkfifo(): %v", err)
	}

	return
}

// Test that `camput' can upload fifos correctly.
func TestCamputFIFO(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.SkipNow()
	}

	fifo, cleanup := mkTmpFIFO(t)
	defer cleanup()

	// Can we successfully upload a fifo?
	w := test.GetWorld(t)
	out := test.MustRunCmd(t, w.Cmd("camput", "file", fifo))

	br := strings.Split(out, "\n")[0]
	out = test.MustRunCmd(t, w.Cmd("camget", br))
	t.Logf("Retrieved stored fifo schema: %s", out)
}

// mkTmpSocket makes a socket in a temporary directory and returns the
// path to it and a function to clean-up when done.
func mkTmpSocket(t *testing.T) (path string, cleanup func()) {
	tdir, err := ioutil.TempDir("", "socket-test-")
	if err != nil {
		t.Fatalf("iouti.TempDir(): %v", err)
	}
	cleanup = func() {
		os.RemoveAll(tdir)
	}

	path = filepath.Join(tdir, "socket")
	err = osutil.Mksocket(path)
	if err != nil {
		t.Fatalf("osutil.Mksocket(): %v", err)
	}

	return
}

// Test that `camput' can upload sockets correctly.
func TestCamputSocket(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.SkipNow()
	}

	socket, cleanup := mkTmpSocket(t)
	defer cleanup()

	// Can we successfully upload a socket?
	w := test.GetWorld(t)
	out := test.MustRunCmd(t, w.Cmd("camput", "file", socket))

	br := strings.Split(out, "\n")[0]
	out = test.MustRunCmd(t, w.Cmd("camget", br))
	t.Logf("Retrieved stored socket schema: %s", out)
}

// Test that camput twice on the same file only uploads once.
func TestCamputUploadOnce(t *testing.T) {
	w := test.GetWorld(t)

	camputCmd := func() *exec.Cmd {
		// Use --contents_only because if test is run from devcam,
		// server-config.json is going to be the one from within the fake gopath,
		// hence with a different cTime and with a different blobRef everytime.
		// Also, CAMLI_DEBUG is needed for --contents_only flag.
		return w.CmdWithEnv("camput", append(os.Environ(), "CAMLI_DEBUG=1"), "file", "--contents_only=true", filepath.FromSlash("../testdata/server-config.json"))
	}
	wantBlobRef := "sha1-b6943ef8fa1a7595385a1f9300ce144525d6938a"
	cmd := camputCmd()
	out := test.MustRunCmd(t, cmd)
	out = strings.TrimSpace(out)
	if out != wantBlobRef {
		t.Fatalf("wrong camput output; wanted %v, got %v", wantBlobRef, out)
	}

	cmd = camputCmd()
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("second camput failed: %v, stdout: %v, stderr: %v", err, output, stderr.String())
	}
	out = strings.TrimSpace(string(output))
	if out != wantBlobRef {
		t.Fatalf("wrong 2nd camput output; wanted %v, got %v", wantBlobRef, out)
	}
	wantStats := `[uploadRequests=[blobs=0 bytes=0] uploads=[blobs=0 bytes=0]]`
	if !strings.Contains(stderr.String(), wantStats) {
		t.Fatalf("Wrong stats for 2nd camput upload; wanted %v, got %v", wantStats, out)
	}
}
