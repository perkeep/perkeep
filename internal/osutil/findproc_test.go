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

const parentDeathTestEnv = "OSUTIL_TEST_DIE_ON_PARENT_DEATH"

// TestDieOnParentDeath checks that a child exits after its parent is killed.
// In the delayed case the parent is already dead before the waiter starts.
func TestDieOnParentDeath(t *testing.T) {
	if mode := os.Getenv(parentDeathTestEnv); mode != "" {
		parentDeathTestHelper(mode)
		return
	}
	for _, mode := range []string{"public", "delayed"} {
		t.Run(mode, func(t *testing.T) {
			parentDeathTest(t, mode)
		})
	}
}

func parentDeathTestHelper(mode string) {
	// A broken watcher must not leave an hour-long orphan. The unexpected
	// output makes watchdog termination distinguishable from success.
	time.AfterFunc(30*time.Second, func() {
		fmt.Println("parent-death helper watchdog expired")
		os.Exit(1)
	})
	switch mode {
	case "parent-public", "parent-delayed":
		childMode := "child-public"
		if mode == "parent-delayed" {
			childMode = "child-delayed"
		}
		child := exec.Command(os.Args[0], "-test.run=^TestDieOnParentDeath$")
		child.Env = append(os.Environ(), parentDeathTestEnv+"="+childMode)
		child.Stdin = os.Stdin
		child.Stdout = os.Stdout
		child.Stderr = os.Stderr
		if err := child.Start(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	case "child-public", "child-delayed":
		// The top-level test owns this pipe's writer. Its closure cleans up
		// an orphan even if readiness or the parent-death assertion fails.
		release := make(chan struct{})
		go func() {
			var b [1]byte
			if _, err := io.ReadFull(os.Stdin, b[:]); err != nil {
				if err != io.EOF {
					fmt.Printf("parent-death helper lifeline: %v\n", err)
					os.Exit(1)
				}
				os.Exit(0)
			}
			close(release)
			if _, err := io.Copy(io.Discard, os.Stdin); err != nil {
				fmt.Printf("parent-death helper lifeline: %v\n", err)
				os.Exit(1)
			}
			os.Exit(0)
		}()
		ppid := os.Getppid()
		if mode == "child-public" {
			DieOnParentDeath()
		}
		fmt.Println("ready")
		if mode == "child-delayed" {
			// The test releases us only after killing and reaping the
			// original parent, simulating a watcher scheduled too late.
			<-release
			waitParentDeath(ppid)
			os.Exit(0)
		}
	default:
		fmt.Println("unexpected parent-death helper mode")
		os.Exit(1)
	}
	time.Sleep(time.Hour)
}

func parentDeathTest(t *testing.T, mode string) {
	t.Helper()
	// Own the stdout pipe ourselves: Cmd.Wait must not close its reader
	// before the child's inherited writer has actually closed.
	out, stdout, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { out.Close(); stdout.Close() })
	stdin, lifeline, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stdin.Close(); lifeline.Close() })
	parent := exec.Command(os.Args[0], "-test.run=^TestDieOnParentDeath$")
	parent.Env = append(os.Environ(), parentDeathTestEnv+"=parent-"+mode)
	parent.Stdin = stdin
	parent.Stdout = stdout
	parent.Stderr = os.Stderr
	if err := parent.Start(); err != nil {
		t.Fatal(err)
	}
	waited := false
	t.Cleanup(func() {
		// Closing the lifeline terminates a surviving child. Closing out
		// also unblocks either bounded reader after a failed assertion.
		lifeline.Close()
		out.Close()
		if !waited {
			parent.Process.Kill()
			parent.Wait()
		}
	})
	stdin.Close()
	stdout.Close()
	br := bufio.NewReader(out)
	ready := make(chan error, 1)
	go func() {
		line, err := br.ReadString('\n')
		if err == nil && line != "ready\n" {
			err = fmt.Errorf("got %q; want ready", line)
		}
		ready <- err
	}()
	select {
	case err := <-ready:
		if err != nil {
			t.Fatalf("reading child's ready line: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for child's ready line")
	}
	if err := parent.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	parent.Wait()
	waited = true
	if mode == "delayed" {
		if _, err := lifeline.Write([]byte{'r'}); err != nil {
			t.Fatal(err)
		}
	}
	exited := make(chan error, 1)
	go func() {
		rest, err := io.ReadAll(br)
		if err == nil && len(rest) > 0 {
			err = fmt.Errorf("after ready line, read %q; want EOF", rest)
		}
		exited <- err
	}()
	select {
	case err := <-exited:
		if err != nil {
			t.Error(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("child did not exit after its parent died")
	}
}
