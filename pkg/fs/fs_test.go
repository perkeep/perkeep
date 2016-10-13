// +build linux darwin

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
	"bufio"
	"bytes"
	"errors"
	"fmt"
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

	"bazil.org/fuse"
	"bazil.org/fuse/syscallx"
	"camlistore.org/pkg/test"
)

var (
	errmu         sync.Mutex
	osxFuseMarker string
)

func condSkip(t *testing.T) {
	errmu.Lock()
	defer errmu.Unlock()
	if !(runtime.GOOS == "darwin" || runtime.GOOS == "linux") {
		t.Skipf("Skipping test on OS %q", runtime.GOOS)
	}
	if runtime.GOOS == "darwin" {
		// TODO: simplify if/when bazil drops 2.x support.
		_, err := os.Stat(fuse.OSXFUSELocationV3.Mount)
		if err == nil {
			osxFuseMarker = "cammount@osxfuse"
			return
		}
		if !os.IsNotExist(err) {
			t.Fatal(err)
		}
		_, err = os.Stat(fuse.OSXFUSELocationV2.Mount)
		if err == nil {
			osxFuseMarker = "mount_osxfusefs@"
			// TODO(mpl): add a similar check/warning to pkg/fs or cammount.
			t.Log("OSXFUSE version 2.x detected. Please consider upgrading to v 3.x.")
			return
		}
		if !os.IsNotExist(err) {
			t.Fatal(err)
		}
		test.DependencyErrorOrSkip(t)
	}
}

type mountEnv struct {
	t          *testing.T
	mountPoint string
	process    *os.Process
}

func (e *mountEnv) Stat(s *stat) int64 {
	file := filepath.Join(e.mountPoint, ".camli_fs_stats", s.name)
	slurp, err := ioutil.ReadFile(file)
	if err != nil {
		e.t.Fatal(err)
	}
	slurp = bytes.TrimSpace(slurp)
	v, err := strconv.ParseInt(string(slurp), 10, 64)
	if err != nil {
		e.t.Fatalf("unexpected value %q in file %s", slurp, file)
	}
	return v
}

func testName() string {
	skip := 0
	for {
		pc, _, _, ok := runtime.Caller(skip)
		skip++
		if !ok {
			panic("Failed to find test name")
		}
		name := strings.TrimPrefix(runtime.FuncForPC(pc).Name(), "camlistore.org/pkg/fs.")
		if strings.HasPrefix(name, "Test") {
			return name
		}
	}
}

func inEmptyMutDir(t *testing.T, fn func(env *mountEnv, dir string)) {
	cammountTest(t, func(env *mountEnv) {
		dir := filepath.Join(env.mountPoint, "roots", testName())
		if err := os.Mkdir(dir, 0755); err != nil {
			t.Fatalf("Failed to make roots/r dir: %v", err)
		}
		fi, err := os.Stat(dir)
		if err != nil || !fi.IsDir() {
			t.Fatalf("Stat of %s dir = %v, %v; want a directory", dir, fi, err)
		}
		fn(env, dir)
	})
}

func cammountTest(t *testing.T, fn func(env *mountEnv)) {
	dupLog := io.MultiWriter(os.Stderr, testLog{t})
	log.SetOutput(dupLog)
	defer log.SetOutput(os.Stderr)

	w := test.GetWorld(t)
	mountPoint, err := ioutil.TempDir("", "fs-test-mount")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := os.RemoveAll(mountPoint); err != nil {
			t.Fatal(err)
		}
	}()
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
	mount.Env = append(mount.Env, "CAMLI_TRACK_FS_STATS=1")

	stdin, err := mount.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Ping(); err != nil {
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
		if !test.WaitFor(not(isMounted(mountPoint)), 5*time.Second, 1*time.Second) {
			// It didn't unmount. Try again.
			Unmount(mountPoint)
		}
	}()

	if !test.WaitFor(dirToBeFUSE(mountPoint), 5*time.Second, 100*time.Millisecond) {
		t.Fatalf("error waiting for %s to be mounted", mountPoint)
	}
	fn(&mountEnv{
		t:          t,
		mountPoint: mountPoint,
		process:    mount.Process,
	})

}

func TestRoot(t *testing.T) {
	condSkip(t)
	cammountTest(t, func(env *mountEnv) {
		f, err := os.Open(env.mountPoint)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		names, err := f.Readdirnames(-1)
		if err != nil {
			t.Fatal(err)
		}
		sort.Strings(names)
		want := []string{"WELCOME.txt", "at", "date", "recent", "roots", "sha1-xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx", "tag"}
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
	inEmptyMutDir(t, func(env *mountEnv, rootDir string) {
		filename := filepath.Join(rootDir, "x")
		f, err := os.Create(filename)
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		if err := f.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
		fi, err := os.Stat(filename)
		if err != nil {
			t.Errorf("Stat error: %v", err)
		} else if !fi.Mode().IsRegular() || fi.Size() != 0 {
			t.Errorf("Stat of roots/r/x = %v size %d; want a %d byte regular file", fi.Mode(), fi.Size(), 0)
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
		ro0 := env.Stat(mutFileOpenRO)
		slurp, err := ioutil.ReadFile(filename)
		if err != nil {
			t.Fatal(err)
		}
		if env.Stat(mutFileOpenRO)-ro0 != 1 {
			t.Error("Read didn't trigger read-only path optimization.")
		}

		const want = "foo, bar\nanother line.\n"
		fi, err = os.Stat(filename)
		if err != nil {
			t.Errorf("Stat error: %v", err)
		} else if !fi.Mode().IsRegular() || fi.Size() != int64(len(want)) {
			t.Errorf("Stat of roots/r/x = %v size %d; want a %d byte regular file", fi.Mode(), fi.Size(), len(want))
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

func TestDifferentWriteTypes(t *testing.T) {
	condSkip(t)
	inEmptyMutDir(t, func(env *mountEnv, rootDir string) {
		filename := filepath.Join(rootDir, "big")

		writes := []struct {
			name     string
			flag     int
			write    []byte // if non-nil, Write is called
			writeAt  []byte // if non-nil, WriteAt is used
			writePos int64  // writeAt position
			want     string // shortenString of remaining file
		}{
			{
				name:  "write 8k of a",
				flag:  os.O_RDWR | os.O_CREATE | os.O_TRUNC,
				write: bytes.Repeat([]byte("a"), 8<<10),
				want:  "a{8192}",
			},
			{
				name:     "writeAt HI at offset 10",
				flag:     os.O_RDWR,
				writeAt:  []byte("HI"),
				writePos: 10,
				want:     "a{10}HIa{8180}",
			},
			{
				name:  "append single C",
				flag:  os.O_WRONLY | os.O_APPEND,
				write: []byte("C"),
				want:  "a{10}HIa{8180}C",
			},
			{
				name:  "append 8k of b",
				flag:  os.O_WRONLY | os.O_APPEND,
				write: bytes.Repeat([]byte("b"), 8<<10),
				want:  "a{10}HIa{8180}Cb{8192}",
			},
		}

		for _, wr := range writes {
			f, err := os.OpenFile(filename, wr.flag, 0644)
			if err != nil {
				t.Fatalf("%s: OpenFile: %v", wr.name, err)
			}
			if wr.write != nil {
				if n, err := f.Write(wr.write); err != nil || n != len(wr.write) {
					t.Fatalf("%s: Write = (%v, %v); want (%d, nil)", wr.name, n, err, len(wr.write))
				}
			}
			if wr.writeAt != nil {
				if n, err := f.WriteAt(wr.writeAt, wr.writePos); err != nil || n != len(wr.writeAt) {
					t.Fatalf("%s: WriteAt = (%v, %v); want (%d, nil)", wr.name, n, err, len(wr.writeAt))
				}
			}
			if err := f.Close(); err != nil {
				t.Fatalf("%s: Close: %v", wr.name, err)
			}

			slurp, err := ioutil.ReadFile(filename)
			if err != nil {
				t.Fatalf("%s: Slurp: %v", wr.name, err)
			}
			if got := shortenString(string(slurp)); got != wr.want {
				t.Fatalf("%s: afterwards, file = %q; want %q", wr.name, got, wr.want)
			}

		}

		// Delete it.
		if err := os.Remove(filename); err != nil {
			t.Fatal(err)
		}
	})
}

func statStr(name string) string {
	fi, err := os.Stat(name)
	if os.IsNotExist(err) {
		return "ENOENT"
	}
	if err != nil {
		return "err=" + err.Error()
	}
	return fmt.Sprintf("file %v, size %d", fi.Mode(), fi.Size())
}

func TestRename(t *testing.T) {
	condSkip(t)
	inEmptyMutDir(t, func(env *mountEnv, rootDir string) {
		name1 := filepath.Join(rootDir, "1")
		name2 := filepath.Join(rootDir, "2")
		subdir := filepath.Join(rootDir, "dir")
		name3 := filepath.Join(subdir, "3")

		contents := []byte("Some file contents")
		const gone = "ENOENT"
		const reg = "file -rw-------, size 18"

		if err := ioutil.WriteFile(name1, contents, 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(subdir, 0755); err != nil {
			t.Fatal(err)
		}

		if got, want := statStr(name1), reg; got != want {
			t.Errorf("name1 = %q; want %q", got, want)
		}
		if err := os.Rename(name1, name2); err != nil {
			t.Fatal(err)
		}
		if got, want := statStr(name1), gone; got != want {
			t.Errorf("name1 = %q; want %q", got, want)
		}
		if got, want := statStr(name2), reg; got != want {
			t.Errorf("name2 = %q; want %q", got, want)
		}

		// Moving to a different directory.
		if err := os.Rename(name2, name3); err != nil {
			t.Fatal(err)
		}
		if got, want := statStr(name2), gone; got != want {
			t.Errorf("name2 = %q; want %q", got, want)
		}
		if got, want := statStr(name3), reg; got != want {
			t.Errorf("name3 = %q; want %q", got, want)
		}
	})
}

func TestMoveAt(t *testing.T) {
	condSkip(t)
	var beforeTime, afterTime time.Time
	oldName := filepath.FromSlash("1/1/1")
	newDir := filepath.FromSlash("2/1")
	newName := filepath.Join(newDir, "1")
	inEmptyMutDir(t, func(env *mountEnv, rootDir string) {
		name1 := filepath.Join(rootDir, oldName)
		name2 := filepath.Join(rootDir, newName)

		if err := os.MkdirAll(name1, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(rootDir, newDir), 0755); err != nil {
			t.Fatal(err)
		}

		time.Sleep(time.Second)
		beforeTime = time.Now()
		time.Sleep(time.Second)

		if err := os.Rename(name1, name2); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(name2); err != nil {
			t.Fatal(err)
		}

		time.Sleep(time.Second)
		afterTime = time.Now()
	})
	cammountTest(t, func(env *mountEnv) {
		atPrefix := filepath.Join(env.mountPoint, "at")
		testname := strings.Split(testName(), ".")[0]

		beforeName := filepath.Join(beforeTime.Format(time.RFC3339), testname, oldName)
		notYetExistName := filepath.Join(beforeTime.Format(time.RFC3339), testname, newName)
		afterName := filepath.Join(afterTime.Format(time.RFC3339), testname, newName)
		goneName := filepath.Join(afterTime.Format(time.RFC3339), testname, oldName)

		if _, err := os.Stat(filepath.Join(atPrefix, beforeName)); err != nil {
			t.Errorf("%v before; want found, got not found; err: %v", beforeName, err)
		}
		if _, err := os.Stat(filepath.Join(atPrefix, notYetExistName)); !os.IsNotExist(err) {
			t.Errorf("%v before; want not found, got found; err: %v", notYetExistName, err)
		}
		if _, err := os.Stat(filepath.Join(atPrefix, afterName)); err != nil {
			t.Errorf("%v after; want found, got not found; err: %v", afterName, err)
		}
		if _, err := os.Stat(filepath.Join(atPrefix, goneName)); !os.IsNotExist(err) {
			t.Errorf("%v after; want not found, got found; err: %v", goneName, err)
		}
	})
}

func parseXattrList(from []byte) map[string]bool {
	attrNames := bytes.Split(from, []byte{0})
	m := map[string]bool{}
	for _, nm := range attrNames {
		if len(nm) == 0 {
			continue
		}
		m[string(nm)] = true
	}
	return m
}

func TestXattr(t *testing.T) {
	condSkip(t)
	inEmptyMutDir(t, func(env *mountEnv, rootDir string) {
		name1 := filepath.Join(rootDir, "1")
		attr1 := "attr1"
		attr2 := "attr2"

		contents := []byte("Some file contents")

		if err := ioutil.WriteFile(name1, contents, 0644); err != nil {
			t.Fatal(err)
		}

		buf := make([]byte, 8192)
		// list empty
		n, err := syscallx.Listxattr(name1, buf)
		if err != nil {
			t.Errorf("Error in initial listxattr: %v", err)
		}
		if n != 0 {
			t.Errorf("Expected zero-length xattr list, got %q", buf[:n])
		}

		// get missing
		n, err = syscallx.Getxattr(name1, attr1, buf)
		if err == nil {
			t.Errorf("Expected error getting non-existent xattr, got %q", buf[:n])
		}

		// Set (two different attributes)
		err = syscallx.Setxattr(name1, attr1, []byte("hello1"), 0)
		if err != nil {
			t.Fatalf("Error setting xattr: %v", err)
		}
		err = syscallx.Setxattr(name1, attr2, []byte("hello2"), 0)
		if err != nil {
			t.Fatalf("Error setting xattr: %v", err)
		}
		// Alternate value for first attribute
		err = syscallx.Setxattr(name1, attr1, []byte("hello1a"), 0)
		if err != nil {
			t.Fatalf("Error setting xattr: %v", err)
		}

		// list attrs
		n, err = syscallx.Listxattr(name1, buf)
		if err != nil {
			t.Errorf("Error in initial listxattr: %v", err)
		}
		m := parseXattrList(buf[:n])
		if !(len(m) == 2 && m[attr1] && m[attr2]) {
			t.Errorf("Missing an attribute: %q", buf[:n])
		}

		// Remove attr
		err = syscallx.Removexattr(name1, attr2)
		if err != nil {
			t.Errorf("Failed to remove attr: %v", err)
		}

		// List attrs
		n, err = syscallx.Listxattr(name1, buf)
		if err != nil {
			t.Errorf("Error in initial listxattr: %v", err)
		}
		m = parseXattrList(buf[:n])
		if !(len(m) == 1 && m[attr1]) {
			t.Errorf("Missing an attribute: %q", buf[:n])
		}

		// Get remaining attr
		n, err = syscallx.Getxattr(name1, attr1, buf)
		if err != nil {
			t.Errorf("Error getting attr1: %v", err)
		}
		if string(buf[:n]) != "hello1a" {
			t.Logf("Expected hello1a, got %q", buf[:n])
		}
	})
}

func TestSymlink(t *testing.T) {
	condSkip(t)
	// Do it all once, unmount, re-mount and then check again.
	// TODO(bradfitz): do this same pattern (unmount and remount) in the other tests.
	var suffix string
	var link string
	const target = "../../some-target" // arbitrary string. some-target is fake.
	check := func() {
		fi, err := os.Lstat(link)
		if err != nil {
			t.Fatalf("Stat: %v", err)
		}
		if fi.Mode()&os.ModeSymlink == 0 {
			t.Errorf("Mode = %v; want Symlink bit set", fi.Mode())
		}
		got, err := os.Readlink(link)
		if err != nil {
			t.Fatalf("Readlink: %v", err)
		}
		if got != target {
			t.Errorf("ReadLink = %q; want %q", got, target)
		}
	}
	inEmptyMutDir(t, func(env *mountEnv, rootDir string) {
		// Save for second test:
		link = filepath.Join(rootDir, "some-link")
		suffix = strings.TrimPrefix(link, env.mountPoint)

		if err := os.Symlink(target, link); err != nil {
			t.Fatalf("Symlink: %v", err)
		}
		t.Logf("Checking in first process...")
		check()
	})
	cammountTest(t, func(env *mountEnv) {
		t.Logf("Checking in second process...")
		link = env.mountPoint + suffix
		check()
	})
}

func TestFinderCopy(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skipf("Skipping Darwin-specific test.")
	}
	condSkip(t)
	inEmptyMutDir(t, func(env *mountEnv, destDir string) {
		f, err := ioutil.TempFile("", "finder-copy-file")
		if err != nil {
			t.Fatal(err)
		}
		defer os.Remove(f.Name())
		want := []byte("Some data for Finder to copy.")
		if _, err := f.Write(want); err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}

		cmd := exec.Command("osascript")
		script := fmt.Sprintf(`
tell application "Finder"
  copy file POSIX file %q to folder POSIX file %q
end tell
`, f.Name(), destDir)
		cmd.Stdin = strings.NewReader(script)

		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("Error running AppleScript: %v, %s", err, out)
		} else {
			t.Logf("AppleScript said: %q", out)
		}

		destFile := filepath.Join(destDir, filepath.Base(f.Name()))
		fi, err := os.Stat(destFile)
		if err != nil {
			t.Errorf("Stat = %v, %v", fi, err)
		}
		if fi.Size() != int64(len(want)) {
			t.Errorf("Dest stat size = %d; want %d", fi.Size(), len(want))
		}
		slurp, err := ioutil.ReadFile(destFile)
		if err != nil {
			t.Fatalf("ReadFile: %v", err)
		}
		if !bytes.Equal(slurp, want) {
			t.Errorf("Dest file = %q; want %q", slurp, want)
		}
	})
}

func TestTextEdit(t *testing.T) {
	if testing.Short() {
		t.Skipf("Skipping in short mode")
	}
	if runtime.GOOS != "darwin" {
		t.Skipf("Skipping Darwin-specific test.")
	}
	condSkip(t)
	inEmptyMutDir(t, func(env *mountEnv, testDir string) {
		var (
			testFile = filepath.Join(testDir, "some-text-file.txt")
			content1 = []byte("Some text content.")
			content2 = []byte("Some replacement content.")
		)
		if err := ioutil.WriteFile(testFile, content1, 0644); err != nil {
			t.Fatal(err)
		}

		cmd := exec.Command("osascript")
		script := fmt.Sprintf(`
tell application "TextEdit"
	activate
	open POSIX file %q
	tell front document
		set paragraph 1 to %q as text
		save
		close
	end tell
end tell
`, testFile, content2)
		cmd.Stdin = strings.NewReader(script)

		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("Error running AppleScript: %v, %s", err, out)
		} else {
			t.Logf("AppleScript said: %q", out)
		}

		fi, err := os.Stat(testFile)
		if err != nil {
			t.Errorf("Stat = %v, %v", fi, err)
		} else if fi.Size() != int64(len(content2)) {
			t.Errorf("Stat size = %d; want %d", fi.Size(), len(content2))
		}
		slurp, err := ioutil.ReadFile(testFile)
		if err != nil {
			t.Fatalf("ReadFile: %v", err)
		}
		if !bytes.Equal(slurp, content2) {
			t.Errorf("File = %q; want %q", slurp, content2)
		}
	})
}

func not(cond func() bool) func() bool {
	return func() bool {
		return !cond()
	}
}

// isInProcMounts returns whether dir is found as a mount point of /dev/fuse in
// /proc/mounts. It does not guarantee the dir is usable as such, as it could have
// been left unmounted by a previously interrupted process ("transport endpoint is
// not connected" error).
func isInProcMounts(dir string) (error, bool) {
	if runtime.GOOS != "linux" {
		return errors.New("only available on linux"), false
	}
	data, err := ioutil.ReadFile("/proc/mounts")
	if err != nil {
		return err, false
	}
	sc := bufio.NewScanner(bytes.NewReader(data))
	dir = strings.TrimSuffix(dir, "/")
	for sc.Scan() {
		l := sc.Text()
		if !strings.HasPrefix(l, "/dev/fuse") {
			continue
		}
		if strings.Fields(l)[1] == dir {
			return nil, true
		}
	}
	return sc.Err(), false
}

// isMounted returns whether dir is considered mounted as far as the filesystem
// is concerned, when one needs to know whether to unmount dir. It does not
// guarantee the dir is usable as such, as it could have been left unmounted by a
// previously interrupted process.
func isMounted(dir string) func() bool {
	if runtime.GOOS == "darwin" {
		return dirToBeFUSE(dir)
	}
	return func() bool {
		err, ok := isInProcMounts(dir)
		if err != nil {
			log.Print(err)
		}
		return ok
	}
}

func dirToBeFUSE(dir string) func() bool {
	return func() bool {
		out, err := exec.Command("df", dir).CombinedOutput()
		if err != nil {
			return false
		}
		if runtime.GOOS == "darwin" {
			return strings.Contains(string(out), osxFuseMarker)
		}
		if runtime.GOOS == "linux" {
			return strings.Contains(string(out), "/dev/fuse") &&
				strings.Contains(string(out), dir)
		}
		return false
	}
}

// shortenString reduces any run of 5 or more identical bytes to "x{17}".
// "hello" => "hello"
// "fooooooooooooooooo" => "fo{17}"
func shortenString(v string) string {
	var buf bytes.Buffer
	var last byte
	var run int
	flush := func() {
		switch {
		case run == 0:
		case run < 5:
			for i := 0; i < run; i++ {
				buf.WriteByte(last)
			}
		default:
			buf.WriteByte(last)
			fmt.Fprintf(&buf, "{%d}", run)
		}
		run = 0
	}
	for i := 0; i < len(v); i++ {
		b := v[i]
		if b != last {
			flush()
		}
		last = b
		run++
	}
	flush()
	return buf.String()
}
