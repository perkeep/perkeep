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
	"encoding/hex"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"camlistore.org/pkg/test"
)

var nonUTF8 = "416c697ae965202d204d6f69204c6f6c6974612e6d7033" // hex-encoding

func tempDir(t *testing.T) (path string, cleanup func()) {
	path, err := ioutil.TempDir("", "camtest-")
	if err != nil {
		t.Fatalf("ioutil.TempDir(): %v", err)
	}

	cleanup = func() {
		os.RemoveAll(path)
	}

	return
}

// Test that we can camput and camget a file whose name is not utf8,
// that we don't panic in the process and that the results are
// correct.
func TestNonUTF8FileName(t *testing.T) {
	srcDir, cleanup := tempDir(t)
	defer cleanup()

	base, err := hex.DecodeString(nonUTF8)
	if err != nil {
		t.Fatalf("hex.DecodeString(): %v", err)
	}

	fd, err := os.Create(filepath.Join(srcDir, string(base)))
	if err != nil {
		t.Fatalf("os.Create(): %v", err)
	}
	fd.Close()

	w := test.GetWorld(t)
	out := test.MustRunCmd(t, w.Cmd("camput", "file", fd.Name()))
	br := strings.Split(out, "\n")[0]

	// camput was a success. Can we get the file back in another directory?
	dstDir, cleanup := tempDir(t)
	defer cleanup()

	_ = test.MustRunCmd(t, w.Cmd("camget", "-o", dstDir, br))
	_, err = os.Lstat(filepath.Join(dstDir, string(base)))
	if err != nil {
		t.Fatalf("Failed to stat file %s in directory %s",
			fd.Name(), dstDir)
	}
}

// Test that we can camput and camget a symbolic link whose target is
// not utf8, that we do no panic in the process and that the results
// are correct.
func TestNonUTF8SymlinkTarget(t *testing.T) {
	srcDir, cleanup := tempDir(t)
	defer cleanup()

	base, err := hex.DecodeString(nonUTF8)
	if err != nil {
		t.Fatalf("hex.DecodeString(): %v", err)
	}

	fd, err := os.Create(filepath.Join(srcDir, string(base)))
	if err != nil {
		t.Fatalf("os.Create(): %v", err)
	}
	defer fd.Close()

	err = os.Symlink(string(base), filepath.Join(srcDir, "link"))
	if err != nil {
		t.Fatalf("os.Symlink(): %v", err)
	}

	w := test.GetWorld(t)
	out := test.MustRunCmd(t, w.Cmd("camput", "file", filepath.Join(srcDir, "link")))
	br := strings.Split(out, "\n")[0]

	// See if we can camget it back correctly
	dstDir, cleanup := tempDir(t)
	defer cleanup()

	_ = test.MustRunCmd(t, w.Cmd("camget", "-o", dstDir, br))
	target, err := os.Readlink(filepath.Join(dstDir, "link"))
	if err != nil {
		t.Fatalf("os.Readlink(): %v", err)
	}

	if !bytes.Equal([]byte(target), base) {
		t.Fatalf("Retrieved symlink contains points to unexpected target")
	}

}
