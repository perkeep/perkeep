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
	"io/ioutil"
	"os"
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
