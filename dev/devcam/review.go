/*
Copyright 2014 The Perkeep Authors.

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

// This file adds the "review" subcommand to devcam, to send changes for peer review.

package main

import (
	"bufio"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"

	"perkeep.org/pkg/cmdmain"
)

var (
	defaultHook = filepath.FromSlash("misc/commit-msg.githook")
	hookFile    = filepath.FromSlash(".git/hooks/commit-msg")
)

type reviewCmd struct {
	releaseBranch string
}

func init() {
	cmdmain.RegisterMode("review", func(flags *flag.FlagSet) cmdmain.CommandRunner {
		cmd := &reviewCmd{}
		flags.StringVar(&cmd.releaseBranch, "branch", "", "Alternative release branch to push to. Defaults to master branch.")
		return cmd
	})
}

func (c *reviewCmd) Usage() {
	fmt.Fprintf(cmdmain.Stderr, "Usage: devcam review\n")
}

func (c *reviewCmd) Describe() string {
	return "Submit your git commits for review."
}

func (c *reviewCmd) RunCommand(args []string) error {
	if len(args) > 0 {
		return cmdmain.UsageError("too many arguments.")
	}
	goToCamliRoot()
	c.checkHook()
	checkOrigin()
	c.gitPush()
	return nil
}

func goToCamliRoot() {
	prevDir, err := os.Getwd()
	if err != nil {
		log.Fatalf("could not get current directory: %v", err)
	}
	for {
		if _, err := os.Stat(defaultHook); err == nil {
			return
		}
		if err := os.Chdir(".."); err != nil {
			log.Fatalf("Could not chdir: %v", err)
		}
		currentDir, err := os.Getwd()
		if err != nil {
			log.Fatalf("Could not get current directory: %v", err)
		}
		if currentDir == prevDir {
			log.Fatal("Perkeep tree root not found. Run from within the Perkeep tree please.")
		}
		prevDir = currentDir
	}
}

func (c *reviewCmd) checkHook() {
	_, err := os.Stat(hookFile)
	if err == nil {
		return
	}
	if !os.IsNotExist(err) {
		log.Fatal(err)
	}
	fmt.Fprintf(cmdmain.Stdout, "Presubmit hook to add Change-Id to commit messages is missing.\nNow automatically creating it at %v\n\n", hookFile)
	cmd := exec.Command("devcam", "hook")
	cmd.Stdout = cmdmain.Stdout
	cmd.Stderr = cmdmain.Stderr
	if err := cmd.Run(); err != nil {
		log.Fatal(err)
	}
	fmt.Fprintf(cmdmain.Stdout, "Amending last commit to add Change-Id.\nPlease re-save description without making changes.\n\n")
	fmt.Fprintf(cmdmain.Stdout, "Press Enter to continue.\n")
	if _, _, err := bufio.NewReader(cmdmain.Stdin).ReadLine(); err != nil {
		log.Fatal(err)
	}

	cmd = exec.Command("git", []string{"commit", "--amend"}...)
	cmd.Stdout = cmdmain.Stdout
	cmd.Stderr = cmdmain.Stderr
	if err := cmd.Run(); err != nil {
		log.Fatal(err)
	}
}

const newOrigin = "https://perkeep.googlesource.com/perkeep"

var (
	newFetch = regexp.MustCompile(`.*Fetch\s+URL:\s+` + newOrigin + `.*`)
	newPush  = regexp.MustCompile(`.*Push\s+URL:\s+` + newOrigin + `.*`)
)

func checkOrigin() {
	out, err := exec.Command("git", "remote", "show", "origin").CombinedOutput()
	if err != nil {
		log.Fatalf("%v, %s", err, out)
	}

	if !newPush.Match(out) {
		setPushOrigin()
	}

	if !newFetch.Match(out) {
		setFetchOrigin()
	}
}

func setPushOrigin() {
	out, err := exec.Command("git", "remote", "set-url", "--push", "origin", newOrigin).CombinedOutput()
	if err != nil {
		log.Fatalf("%v, %s", err, out)
	}
}

func setFetchOrigin() {
	out, err := exec.Command("git", "remote", "set-url", "origin", newOrigin).CombinedOutput()
	if err != nil {
		log.Fatalf("%v, %s", err, out)
	}
}

func (c *reviewCmd) gitPush() {
	args := []string{"push", "origin"}
	if c.releaseBranch != "" {
		args = append(args, "HEAD:refs/for/releases/"+c.releaseBranch)
	} else {
		args = append(args, "HEAD:refs/for/master")
	}
	cmd := exec.Command("git", args...)
	cmd.Stdout = cmdmain.Stdout
	cmd.Stderr = cmdmain.Stderr
	if err := cmd.Run(); err != nil {
		log.Fatalf("Could not git push: %v", err)
	}
}
