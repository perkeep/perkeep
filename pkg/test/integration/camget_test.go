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
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"camlistore.org/pkg/test"
	"camlistore.org/pkg/test/asserts"
)

// Test that `camget -o' can restore a symlink correctly.
func TestCamgetSymlink(t *testing.T) {
	w := test.GetWorld(t)

	srcDir, err := ioutil.TempDir("", "camget-test-")
	if err != nil {
		t.Fatalf("ioutil.TempDir(): %v", err)
	}
	defer os.RemoveAll(srcDir)

	targetBase := "a"
	target := filepath.Join(srcDir, targetBase)
	targetFD, err := os.Create(target)
	if err != nil {
		t.Fatalf("os.Create(): %v", err)
	}
	targetFD.Close()

	subdirBase := "child"
	subdirName := filepath.Join(srcDir, subdirBase)
	linkBase := "b"
	linkName := filepath.Join(subdirName, linkBase)
	err = os.Mkdir(subdirName, 0777)
	if err != nil {
		t.Fatalf("os.Mkdir(): %v", err)
	}

	err = os.Symlink("../"+targetBase, linkName)
	if err != nil {
		t.Fatalf("os.Symlink(): %v", err)
	}

	out := test.MustRunCmd(t, w.Cmd("camput", "file", srcDir))
	// TODO(mpl): rm call and delete pkg.
	asserts.ExpectBool(t, true, out != "", "camput")
	br := strings.Split(out, "\n")[0]
	dstDir, err := ioutil.TempDir("", "camget-test-")
	if err != nil {
		t.Fatalf("ioutil.TempDir(): %v", err)
	}
	defer os.RemoveAll(dstDir)

	// Now restore the symlink
	_ = test.MustRunCmd(t, w.Cmd("camget", "-o", dstDir, br))

	symlink := filepath.Join(dstDir, filepath.Base(srcDir), subdirBase,
		linkBase)
	link, err := os.Readlink(symlink)
	if err != nil {
		t.Fatalf("os.Readlink(): %v", err)
	}
	expected := "../a"
	if expected != link {
		t.Fatalf("os.Readlink(): Expected: %s, got %s", expected,
			link)
	}

	// Ensure that the link is not broken
	_, err = os.Stat(symlink)
	if err != nil {
		t.Fatalf("os.Stat(): %v", err)
	}
}

// Test that `camget -o' can restore a fifo correctly.
func TestCamgetFIFO(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.SkipNow()
	}

	fifo, cleanup := mkTmpFIFO(t)
	defer cleanup()

	// Upload the fifo
	w := test.GetWorld(t)
	out := test.MustRunCmd(t, w.Cmd("camput", "file", fifo))
	br := strings.Split(out, "\n")[0]

	// Try and get it back
	tdir, err := ioutil.TempDir("", "fifo-test-")
	if err != nil {
		t.Fatalf("ioutil.TempDir(): %v", err)
	}
	defer os.RemoveAll(tdir)
	test.MustRunCmd(t, w.Cmd("camget", "-o", tdir, br))

	// Ensure it is actually a fifo
	name := filepath.Join(tdir, filepath.Base(fifo))
	fi, err := os.Lstat(name)
	if err != nil {
		t.Fatalf("os.Lstat(): %v", err)
	}
	if mask := fi.Mode() & os.ModeNamedPipe; mask == 0 {
		t.Fatalf("Retrieved file %s: Not a FIFO", name)
	}
}

// Test that `camget -o' can restore a socket correctly.
func TestCamgetSocket(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.SkipNow()
	}

	socket, cleanup := mkTmpSocket(t)
	defer cleanup()

	// Upload the socket
	w := test.GetWorld(t)
	out := test.MustRunCmd(t, w.Cmd("camput", "file", socket))
	br := strings.Split(out, "\n")[0]

	// Try and get it back
	tdir, err := ioutil.TempDir("", "socket-test-")
	if err != nil {
		t.Fatalf("ioutil.TempDir(): %v", err)
	}
	defer os.RemoveAll(tdir)
	test.MustRunCmd(t, w.Cmd("camget", "-o", tdir, br))

	// Ensure it is actually a socket
	name := filepath.Join(tdir, filepath.Base(socket))
	fi, err := os.Lstat(name)
	if err != nil {
		t.Fatalf("os.Lstat(): %v", err)
	}
	if mask := fi.Mode() & os.ModeSocket; mask == 0 {
		t.Fatalf("Retrieved file %s: Not a socket", name)
	}
}

// Test that:
// 1) `camget -contents' can restore a regular file correctly.
// 2) if the file already exists, and has the same size as the one held by the server,
// stop early and do not even fetch it from the server.
func TestCamgetFile(t *testing.T) {
	dirName, err := ioutil.TempDir("", "camli-TestCamgetFile")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dirName)
	f, err := os.Create(filepath.Join(dirName, "test.txt"))
	if err != nil {
		t.Fatal(err)
	}
	filename := f.Name()
	contents := "not empty anymore"
	if _, err := f.Write([]byte(contents)); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	outDir := filepath.Join(dirName, "fetched")
	if err := os.Mkdir(outDir, 0700); err != nil {
		t.Fatal(err)
	}

	w := test.GetWorld(t)
	out := test.MustRunCmd(t, w.Cmd("camput", "file", filename))

	br := strings.Split(out, "\n")[0]
	_ = test.MustRunCmd(t, w.Cmd("camget", "-o", outDir, "-contents", br))

	fetchedName := filepath.Join(outDir, "test.txt")
	b, err := ioutil.ReadFile(fetchedName)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != contents {
		t.Fatalf("fetched file different from original file, got contents %q, wanted %q", b, contents)
	}

	var stderr bytes.Buffer
	c := w.Cmd("camget", "-o", outDir, "-contents", "-verbose", br)
	c.Stderr = &stderr
	if err := c.Run(); err != nil {
		t.Fatalf("running second camget: %v", err)
	}
	if !strings.Contains(stderr.String(), fmt.Sprintf("Skipping %s; already exists.", fetchedName)) {
		t.Fatal(errors.New("Was expecting info message about local file already existing"))
	}
}
