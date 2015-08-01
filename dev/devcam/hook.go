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
	"runtime"
	"sort"
	"strings"

	"camlistore.org/pkg/cmdmain"
)

var hookPath = ".git/hooks/"
var hookFiles = []string{
	"pre-commit",
}

func (c *hookCmd) installHook() error {
	for _, hookFile := range hookFiles {
		filename := filepath.Join(repoRoot(), hookPath+hookFile)
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
	fix     bool // disabled for now
	debug   bool
}

func init() {
	cmdmain.RegisterCommand("hook", func(flags *flag.FlagSet) cmdmain.CommandRunner {
		cmd := &hookCmd{}
		flags.BoolVar(&cmd.verbose, "verbose", false, "Be verbose.")
		flags.BoolVar(&cmd.debug, "debug", false, "Arguments after the hook name are files that will be used as input to the hook, instead of the hook using the staging area.")
		// TODO(mpl): "-w" flag to run gofmt -w and devcam fixv -w. for now just print instruction.
		// flags.BoolVar(&cmd.fix, "w", false, "Perform appropriate fixes, for hooks like pre-commit.")
		return cmd
	})
}

// TODO(mpl): more docs, examples. Also doc in website to tell ppl to use it.

func (c *hookCmd) Usage() {
	printf("Usage: devcam [globalopts] hook [[hook-name] [args...]]\n")
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
			printf("You can override these checks with 'git commit --no-verify'\n")
			// TODO(mpl): make sure that by exiting "early" we're not skipping some post-RunCommand
			// stuff controlled by cmdmain.Main
			os.Exit(1)
		}
	}
	return nil
}

// hookPreCommit does the following checks, in order:
// gofmt, import paths in vendored files, trailing space.
// If appropriate, any one of these checks prints the action
// required from the user, and the following checks are not
// performed.
func (c *hookCmd) hookPreCommit(args []string) (err error) {
	if err = c.hookGofmt(); err != nil {
		return err
	}
	if err := c.hookVendoredImports(args); err != nil {
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
		printf("gofmt reported errors:\n\t%v\n", strings.Replace(strings.TrimSpace(err.Error()), "\n", "\n\t", -1))
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

// hookVendoredImports runs devcam fixv on the files in args, if any, or on the
// files matching the files in the git staging area.
// If required fixing is found, the appropriate instruction is printed.
func (c *hookCmd) hookVendoredImports(args []string) error {
	tofix, err := (&fixvCmd{
		verbose: c.verbose,
		fix:     c.fix,
	}).run(args)
	if err != nil {
		if err == errImportsNeedsFixing {
			printf("You need to fix the imports of vendored files: \n\tdevcam fixv -w %s\n", strings.Join(tofix, " "))
		} else {
			printf("devcam fixv reported errors: %v", err)
		}
		return err
	}
	return nil
}

// runGofmt runs the external gofmt command over the local version of staged files.
// It returns the files that need gofmting.
func (c *hookCmd) runGofmt() (files []string, err error) {
	repo := repoRoot()
	if !strings.HasSuffix(repo, string(filepath.Separator)) {
		repo += string(filepath.Separator)
	}

	indexFiles := addRoot(repo, filter(gofmtRequired, nonBlankLines(cmdOutput("git", "diff-index", "--name-only", "--diff-filter=ACM", "--cached", "HEAD", "--"))))
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
	fmt.Fprintf(cmdmain.Stderr, format, args...)
}

func dief(format string, args ...interface{}) {
	printf(format, args...)
	os.Exit(1)
}

func repoRoot() string {
	dir, err := os.Getwd()
	if err != nil {
		dief("could not get current directory: %v", err)
	}
	rootlen := 1
	if runtime.GOOS == "windows" {
		rootlen += len(filepath.VolumeName(dir))
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir
		}
		if len(dir) == rootlen && dir[rootlen-1] == filepath.Separator {
			dief(".git not found. Rerun from within the Camlistore source tree.")
		}
		dir = filepath.Dir(dir)
	}
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

// cmdOutput runs the command line, returning its output.
// If the command cannot be run or does not exit successfully,
// cmdOutput dies.
//
// NOTE: cmdOutput must be used only to run commands that read state,
// not for commands that make changes. Commands that make changes
// should be run using runDirErr so that the -v and -n flags apply to them.
func cmdOutput(command string, args ...string) string {
	out, err := cmdOutputDirErr(".", command, args...)
	if err != nil {
		printf("%v\n", err)
		// TODO(mpl): maybe not die. see other comment about cmdmain.Main.
		os.Exit(1)
	}
	return out
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
