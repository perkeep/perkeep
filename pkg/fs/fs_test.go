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

package fs

import (
	"io"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"camlistore.org/pkg/test"
)

var (
	errmu   sync.Mutex
	lasterr error
)

func condSkip(t *testing.T) {
	errmu.Lock()
	defer errmu.Unlock()
	if lasterr != nil {
		t.Skipf("Skipping test; some other test already failed.")
	}
	if runtime.GOOS != "darwin" {
		t.Skipf("Skipping test on OS %q", runtime.GOOS)
	}
	if runtime.GOOS == "darwin" {
		_, err := os.Stat("/Library/Filesystems/osxfusefs.fs/Support/mount_osxfusefs")
		if os.IsNotExist(err) {
			test.DependencyErrorOrSkip(t)
		} else if err != nil {
			t.Fatal(err)
		}
	}
}

func cammountTest(t *testing.T, fn func(mountPoint string)) {
	w := test.GetWorld(t)
	mountPoint, err := ioutil.TempDir("", "fs-test-mount")
	if err != nil {
		t.Fatal(err)
	}
	verbose := "false"
	var stderrDest io.Writer = ioutil.Discard
	if v, _ := strconv.ParseBool(os.Getenv("VERBOSE_FUSE")); v {
		verbose = "true"
		stderrDest = testLog{t}
	}
	if v, _ := strconv.ParseBool(os.Getenv("VERBOSE_FUSE_STDERR")); v {
		stderrDest = io.MultiWriter(stderrDest, os.Stderr)
	}

	mount := w.Cmd("cammount", "--debug="+verbose, mountPoint)
	mount.Stderr = stderrDest

	stdin, err := mount.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := mount.Start(); err != nil {
		t.Fatal(err)
	}
	waitc := make(chan error, 1)
	go func() { waitc <- mount.Wait() }()
	defer func() {
		log.Printf("Sending quit")
		stdin.Write([]byte("q\n"))
		select {
		case <-time.After(5 * time.Second):
			log.Printf("timeout waiting for cammount to finish")
			mount.Process.Kill()
			Unmount(mountPoint)
		case err := <-waitc:
			log.Printf("cammount exited: %v", err)
		}
		if !waitFor(not(dirToBeFUSE(mountPoint)), 5*time.Second, 1*time.Second) {
			// It didn't unmount. Try again.
			Unmount(mountPoint)
		}
	}()

	if !waitFor(dirToBeFUSE(mountPoint), 5*time.Second, 100*time.Millisecond) {
		t.Fatalf("error waiting for %s to be mounted", mountPoint)
	}
	fn(mountPoint)
}

func TestRoot(t *testing.T) {
	condSkip(t)
	cammountTest(t, func(mountPoint string) {
		f, err := os.Open(mountPoint)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		names, err := f.Readdirnames(-1)
		if err != nil {
			t.Fatal(err)
		}
		sort.Strings(names)
		want := []string{"WELCOME.txt", "date", "recent", "roots", "sha1-xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx", "tag"}
		if !reflect.DeepEqual(names, want) {
			t.Errorf("root directory = %q; want %q", names, want)
		}
	})
}

type testLog struct {
	t *testing.T
}

func (tl testLog) Write(p []byte) (n int, err error) {
	tl.t.Log(strings.TrimSpace(string(p)))
	return len(p), nil
}

func TestMutable(t *testing.T) {
	condSkip(t)
	dupLog := io.MultiWriter(os.Stderr, testLog{t})
	log.SetOutput(dupLog)
	defer log.SetOutput(os.Stderr)
	cammountTest(t, func(mountPoint string) {
		rootDir := filepath.Join(mountPoint, "roots", "r")
		if err := os.Mkdir(rootDir, 0700); err != nil {
			t.Fatalf("Failed to make roots/r dir: %v", err)
		}
		fi, err := os.Stat(rootDir)
		if err != nil || !fi.IsDir() {
			t.Fatalf("Stat of roots/r dir = %v, %v; want a directory", fi, err)
		}

		filename := filepath.Join(rootDir, "x")
		f, err := os.Create(filename)
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		if err := f.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
		fi, err = os.Stat(filename)
		if err != nil || !fi.Mode().IsRegular() || fi.Size() != 0 {
			t.Fatalf("Stat of roots/r/x = %v, %v; want a 0-byte regular file", fi, err)
		}

		for _, str := range []string{"foo, ", "bar\n", "another line.\n"} {
			f, err = os.OpenFile(filename, os.O_WRONLY|os.O_APPEND, 0644)
			if err != nil {
				t.Fatalf("OpenFile: %v", err)
			}
			if _, err := f.Write([]byte(str)); err != nil {
				t.Logf("Error with append: %v", err)
				t.Fatalf("Error appending %q to %s: %v", str, filename, err)
			}
			if err := f.Close(); err != nil {
				t.Fatal(err)
			}
		}
		slurp, err := ioutil.ReadFile(filename)
		if err != nil {
			t.Fatal(err)
		}
		const want = "foo, bar\nanother line.\n"
		fi, err = os.Stat(filename)
		if err != nil || !fi.Mode().IsRegular() || fi.Size() != int64(len(want)) {
			t.Errorf("Stat of roots/r/x = %v, %v; want a %d byte regular file", fi, len(want), err)
		}
		if got := string(slurp); got != want {
			t.Fatalf("contents = %q; want %q", got, want)
		}

		// Delete it.
		if err := os.Remove(filename); err != nil {
			t.Fatal(err)
		}

		// Gone?
		if _, err := os.Stat(filename); !os.IsNotExist(err) {
			t.Fatalf("expected file to be gone; got stat err = %v instead", err)
		}
	})
}

func waitFor(condition func() bool, maxWait, checkInterval time.Duration) bool {
	t0 := time.Now()
	tmax := t0.Add(maxWait)
	for time.Now().Before(tmax) {
		if condition() {
			return true
		}
		time.Sleep(checkInterval)
	}
	return false
}

func not(cond func() bool) func() bool {
	return func() bool {
		return !cond()
	}
}

func dirToBeFUSE(dir string) func() bool {
	return func() bool {
		out, err := exec.Command("df", dir).CombinedOutput()
		if err != nil {
			return false
		}
		if runtime.GOOS == "darwin" {
			if strings.Contains(string(out), "mount_osxfusefs@") {
				log.Printf("fs %s is mounted on fuse.", dir)
				return true
			}
			return false
		}
		return false
	}
}
