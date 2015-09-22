/*
Copyright 2015 The Camlistore Authors.

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

// This file adds the "hook" subcommand to devcam, to install and run git hooks.
package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"camlistore.org/pkg/cmdmain"
)

var hookPath = ".git/hooks/"
var hookFiles = []string{
	"pre-commit",
}

func (c *hookCmd) installHook() error {
	root, err := repoRoot()
	if err != nil {
		return err
	}
	for _, hookFile := range hookFiles {
		filename := filepath.Join(root, hookPath+hookFile)
		hookContent := fmt.Sprintf(hookScript, hookFile)
		// If hook file exists, assume it is okay.
		_, err := os.Stat(filename)
		if err == nil {
			if c.verbose {
				data, err := ioutil.ReadFile(filename)
				if err != nil {
					c.verbosef("reading hook: %v", err)
				} else if string(data) != hookContent {
					c.verbosef("unexpected hook content in %s", filename)
				}
			}
			continue
		}

		if !os.IsNotExist(err) {
			return fmt.Errorf("checking hook: %v", err)
		}
		c.verbosef("installing %s hook", hookFile)
		if err := ioutil.WriteFile(filename, []byte(hookContent), 0700); err != nil {
			return fmt.Errorf("writing hook: %v", err)
		}
	}
	return nil
}

var hookScript = `#!/bin/sh
exec devcam hook %s "$@"
`

type hookCmd struct {
	verbose bool
}

func init() {
	cmdmain.RegisterCommand("hook", func(flags *flag.FlagSet) cmdmain.CommandRunner {
		cmd := &hookCmd{}
		flags.BoolVar(&cmd.verbose, "verbose", false, "Be verbose.")
		// TODO(mpl): "-w" flag to run gofmt -w and devcam fixv -w. for now just print instruction.
		return cmd
	})
}

func (c *hookCmd) Usage() {
	printf("Usage: devcam [globalopts] hook [[hook-name] [args...]]\n")
}

func (c *hookCmd) Examples() []string {
	return []string{
		"# install the hooks (if needed)",
		"pre-commit # install the hooks (if needed), then run the pre-commit hook",
	}
}

func (c *hookCmd) Describe() string {
	return "Install git hooks for Camlistore, and if given, run the hook given as argument. Currently available hooks are: " + strings.TrimSuffix(strings.Join(hookFiles, ", "), ",") + "."
}

func (c *hookCmd) RunCommand(args []string) error {
	if err := c.installHook(); err != nil {
		return err
	}
	if len(args) == 0 {
		return nil
	}
	switch args[0] {
	case "pre-commit":
		if err := c.hookPreCommit(args[1:]); err != nil {
			if !(len(args) > 1 && args[1] == "test") {
				printf("You can override these checks with 'git commit --no-verify'\n")
			}
			cmdmain.ExitWithFailure = true
			return err
		}
	}
	return nil
}

// hookPreCommit does the following checks, in order:
// gofmt, and trailing space.
// If appropriate, any one of these checks prints the action
// required from the user, and the following checks are not
// performed.
func (c *hookCmd) hookPreCommit(args []string) (err error) {
	if err = c.hookGofmt(); err != nil {
		return err
	}
	return c.hookTrailingSpace()
}

// hookGofmt runs a gofmt check on the local files matching the files in the
// git staging area.
// An error is returned if something went wrong or if some of the files need
// gofmting. In the latter case, the instruction is printed.
func (c *hookCmd) hookGofmt() error {
	if os.Getenv("GIT_GOFMT_HOOK") == "off" {
		printf("gofmt disabled by $GIT_GOFMT_HOOK=off\n")
		return nil
	}

	files, err := c.runGofmt()
	if err != nil {
		printf("gofmt hook reported errors:\n\t%v\n", strings.Replace(strings.TrimSpace(err.Error()), "\n", "\n\t", -1))
		return errors.New("gofmt errors")
	}
	if len(files) == 0 {
		return nil
	}
	printf("You need to format with gofmt:\n\tgofmt -w %s\n",
		strings.Join(files, " "))
	return errors.New("gofmt required")
}

func (c *hookCmd) hookTrailingSpace() error {
	out, _ := cmdOutputDirErr(".", "git", "diff-index", "--check", "--diff-filter=ACM", "--cached", "HEAD", "--")
	if out != "" {
		printf("\n%s", out)
		printf("Trailing whitespace detected, you need to clean it up manually.\n")
		return errors.New("trailing whitespace.")
	}
	return nil
}

// runGofmt runs the external gofmt command over the local version of staged files.
// It returns the files that need gofmting.
func (c *hookCmd) runGofmt() (files []string, err error) {
	repo, err := repoRoot()
	if err != nil {
		return nil, err
	}
	if !strings.HasSuffix(repo, string(filepath.Separator)) {
		repo += string(filepath.Separator)
	}

	out, err := cmdOutputDirErr(".", "git", "diff-index", "--name-only", "--diff-filter=ACM", "--cached", "HEAD", "--")
	if err != nil {
		return nil, err
	}
	indexFiles := addRoot(repo, filter(gofmtRequired, nonBlankLines(out)))
	if len(indexFiles) == 0 {
		return
	}

	args := []string{"-l"}
	// TODO(mpl): it would be nice to TrimPrefix the pwd from each file to get a shorter output.
	// However, since git sets the pwd to GIT_DIR before running the pre-commit hook, we lost
	// the actual pwd from when we ran `git commit`, so no dice so far.
	for _, file := range indexFiles {
		args = append(args, file)
	}

	if c.verbose {
		fmt.Fprintln(cmdmain.Stderr, commandString("gofmt", args))
	}
	cmd := exec.Command("gofmt", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err = cmd.Run()

	if err != nil {
		// Error but no stderr: usually can't find gofmt.
		if stderr.Len() == 0 {
			return nil, fmt.Errorf("invoking gofmt: %v", err)
		}
		return nil, fmt.Errorf("%s: %v", stderr.String(), err)
	}

	// Build file list.
	files = lines(stdout.String())
	sort.Strings(files)
	return files, nil
}

func printf(format string, args ...interface{}) {
	cmdmain.Errorf(format, args...)
}

func addRoot(root string, list []string) []string {
	var out []string
	for _, x := range list {
		out = append(out, filepath.Join(root, x))
	}
	return out
}

// nonBlankLines returns the non-blank lines in text.
func nonBlankLines(text string) []string {
	var out []string
	for _, s := range lines(text) {
		if strings.TrimSpace(s) != "" {
			out = append(out, s)
		}
	}
	return out
}

// filter returns the elements in list satisfying f.
func filter(f func(string) bool, list []string) []string {
	var out []string
	for _, x := range list {
		if f(x) {
			out = append(out, x)
		}
	}
	return out
}

// gofmtRequired reports whether the specified file should be checked
// for gofmt'dness by the pre-commit hook.
// The file name is relative to the repo root.
func gofmtRequired(file string) bool {
	if !strings.HasSuffix(file, ".go") {
		return false
	}
	if !strings.HasPrefix(file, "test/") {
		return true
	}
	return strings.HasPrefix(file, "test/bench/") || file == "test/run.go"
}

func commandString(command string, args []string) string {
	return strings.Join(append([]string{command}, args...), " ")
}

func lines(text string) []string {
	out := strings.Split(text, "\n")
	// Split will include a "" after the last line. Remove it.
	if n := len(out) - 1; n >= 0 && out[n] == "" {
		out = out[:n]
	}
	return out
}

func (c *hookCmd) verbosef(format string, args ...interface{}) {
	if c.verbose {
		fmt.Fprintf(cmdmain.Stdout, format, args...)
	}
}

// cmdOutputDirErr runs the command line in dir, returning its output
// and any error results.
//
// NOTE: cmdOutputDirErr must be used only to run commands that read state,
// not for commands that make changes. Commands that make changes
// should be run using runDirErr so that the -v and -n flags apply to them.
func cmdOutputDirErr(dir, command string, args ...string) (string, error) {
	// NOTE: We only show these non-state-modifying commands with -v -v.
	// Otherwise things like 'git sync -v' show all our internal "find out about
	// the git repo" commands, which is confusing if you are just trying to find
	// out what git sync means.

	cmd := exec.Command(command, args...)
	if dir != "." {
		cmd.Dir = dir
	}
	b, err := cmd.CombinedOutput()
	return string(b), err
}
