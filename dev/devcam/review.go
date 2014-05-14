/*
Copyright 2014 The Camlistore Authors.

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
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"path/filepath"

	"camlistore.org/pkg/cmdmain"
)

var (
	defaultHook = filepath.FromSlash("misc/commit-msg.githook")
	hookFile    = filepath.FromSlash(".git/hooks/commit-msg")
)

type reviewCmd struct{}

func init() {
	cmdmain.RegisterCommand("review", func(flags *flag.FlagSet) cmdmain.CommandRunner {
		return new(reviewCmd)
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
	gitPush()
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
			log.Fatal("Camlistore tree root not found. Run from within the Camlistore tree please.")
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
	fmt.Fprintf(cmdmain.Stdout, "Presubmit hook to add Change-Id to commit messages is missing.\nNow automatically creating it at %v from %v\n\n", hookFile, defaultHook)
	data, err := ioutil.ReadFile(defaultHook)
	if err != nil {
		log.Fatal(err)
	}
	if err := ioutil.WriteFile(hookFile, data, 0700); err != nil {
		log.Fatal(err)
	}
	fmt.Fprintf(cmdmain.Stdout, "Amending last commit to add Change-Id.\nPlease re-save description without making changes.\n\n")
	fmt.Fprintf(cmdmain.Stdout, "Press Enter to continue.\n")
	if _, _, err := bufio.NewReader(cmdmain.Stdin).ReadLine(); err != nil {
		log.Fatal(err)
	}

	cmd := exec.Command("git", []string{"commit", "--amend"}...)
	cmd.Stdout = cmdmain.Stdout
	cmd.Stderr = cmdmain.Stderr
	if err := cmd.Run(); err != nil {
		log.Fatal(err)
	}
}

func gitPush() {
	cmd := exec.Command("git",
		[]string{"push", "https://camlistore.googlesource.com/camlistore", "HEAD:refs/for/master"}...)
	cmd.Stdout = cmdmain.Stdout
	cmd.Stderr = cmdmain.Stderr
	if err := cmd.Run(); err != nil {
		log.Fatalf("Could not git push: %v", err)
	}
}
