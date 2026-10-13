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

package osutil

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"testing"
	"time"
)

// TestDieOnParentDeath runs a parent process that runs a child process
// that calls DieOnParentDeath, kills the parent, and checks that the
// child exits too: the child inherits the parent's stdout, so reading
// it reaches EOF only once both have exited.
func TestDieOnParentDeath(t *testing.T) {
	switch os.Getenv("OSUTIL_TEST_DIE_ON_PARENT_DEATH") {
	case "parent":
		child := exec.Command(os.Args[0], "-test.run=^TestDieOnParentDeath$")
		child.Env = append(os.Environ(), "OSUTIL_TEST_DIE_ON_PARENT_DEATH=child")
		child.Stdout = os.Stdout
		child.Stderr = os.Stderr
		if err := child.Start(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		time.Sleep(time.Hour)
		return
	case "child":
		DieOnParentDeath()
		fmt.Println("ready")
		time.Sleep(time.Hour)
		return
	}

	parent := exec.Command(os.Args[0], "-test.run=^TestDieOnParentDeath$")
	parent.Env = append(os.Environ(), "OSUTIL_TEST_DIE_ON_PARENT_DEATH=parent")
	parent.Stderr = os.Stderr
	out, err := parent.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := parent.Start(); err != nil {
		t.Fatal(err)
	}
	br := bufio.NewReader(out)
	if line, err := br.ReadString('\n'); line != "ready\n" {
		parent.Process.Kill()
		t.Fatalf("reading child's ready line: got %q, %v", line, err)
	}
	if err := parent.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	// This hangs if the child doesn't exit.
	if rest, err := io.ReadAll(br); err != nil || len(rest) > 0 {
		t.Errorf("after ready line, read %q, %v; want EOF", rest, err)
	}
	parent.Wait()
}
