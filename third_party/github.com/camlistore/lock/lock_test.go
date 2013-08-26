/*
Copyright 2013 The Go Authors

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

package lock

import (
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
)

func TestLock(t *testing.T) {
	testLock(t, false)
}

func TestLockPortable(t *testing.T) {
	testLock(t, true)
}

func TestLockInChild(t *testing.T) {
	f := os.Getenv("TEST_LOCK_FILE")
	if f == "" {
		// not child
		return
	}
	lock := Lock
	if v, _ := strconv.ParseBool(os.Getenv("TEST_LOCK_PORTABLE")); v {
		lock = lockPortable
	}

	lk, err := lock(f)
	if err != nil {
		log.Fatal("Lock failed: %v", err)
	}
	lk.Close()
}

func testLock(t *testing.T, portable bool) {
	lock := Lock
	if portable {
		lock = lockPortable
	}

	td, err := ioutil.TempDir("", "")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(td)

	path := filepath.Join(td, "foo.lock")

	childLock := func() error {
		cmd := exec.Command(os.Args[0], "-test.run=LockInChild$")
		cmd.Env = []string{"TEST_LOCK_FILE=" + path}
		if portable {
			cmd.Env = append(cmd.Env, "TEST_LOCK_PORTABLE=1")
		}
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("Child Process lock of %s failed: %v %s", path, err, out)
		}
		return nil
	}

	if err := childLock(); err != nil {
		t.Fatalf("lock in child process: %v", err)
	}

	lk1, err := lock(path)
	if err != nil {
		t.Fatal(err)
	}

	_, err = lock(path)
	if err == nil {
		t.Fatal("expected second lock to fail")
	}

	if childLock() == nil {
		t.Fatalf("expected lock in child process to fail")
	}

	if err := lk1.Close(); err != nil {
		t.Fatal(err)
	}

	lk3, err := lock(path)
	if err != nil {
		t.Fatal(err)
	}
	lk3.Close()
}
