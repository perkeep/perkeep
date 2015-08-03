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

// This file adds the "fixv" subcommand to devcam, to rewrite the import paths
// of the vendored packages in Camlistore.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io/ioutil"
	"path/filepath"
	"regexp"
	"strings"

	"camlistore.org/pkg/cmdmain"
)

const vendoringPath = "camlistore.org/third_party/"

var errImportsNeedsFixing = errors.New("some imports need fixing")

var vendoredNames = []string{
	"code.google.com",
	"launchpad.net",
	"github.com",
	"labix.org",
	"bazil.org",
	"golang.org",
	"google.golang.org",
}

type fixvCmd struct {
	verbose bool
	fix     bool
}

func init() {
	cmdmain.RegisterCommand("fixv", func(flags *flag.FlagSet) cmdmain.CommandRunner {
		cmd := &fixvCmd{}
		flags.BoolVar(&cmd.verbose, "verbose", false, "Be verbose.")
		flags.BoolVar(&cmd.fix, "w", false, "Fix the imports.")
		return cmd
	})
}

func (c *fixvCmd) Usage() {
	cmdmain.Errorf("Usage: devcam [globalopts] fixv [args...]\n")
}

func (c *fixvCmd) Describe() string {
	return "Check, and optionally fix, import statements in vendored files."
}

func (c *fixvCmd) Examples() []string {
	return []string{
		"-w # automatically fix the imports in the vendored files from the git staging area",
		"/foo/bar.go # assume /foo/bar.go is vendored, and check if it needs to have its import fixed",
	}
}

func (c *fixvCmd) RunCommand(args []string) error {
	_, err := c.run(args)
	return err
}

func (c *fixvCmd) run(args []string) (tofix []string, err error) {
	var vendoredFiles []string
	if len(args) != 0 {
		vendoredFiles = args
	} else {
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
		vendoredFiles = addRoot(repo, filter(isVendored, nonBlankLines(out)))
		if len(vendoredFiles) == 0 {
			return nil, nil
		}
	}
	re := regexp.MustCompile(`("` + strings.Join(vendoredNames, `/|"`) + `/)`)
	for _, filename := range vendoredFiles {
		data, err := ioutil.ReadFile(filename)
		if err != nil {
			return nil, err
		}
		if m := re.Find(data); m == nil {
			continue
		}
		if !c.fix {
			cmdmain.Errorf("%v imports need fixing\n", filename)
			tofix = append(tofix, filename)
			continue
		}
		for _, importName := range vendoredNames {
			re := regexp.MustCompile(`"(` + importName + "/)")
			data = re.ReplaceAll(data, []byte(`"`+vendoringPath+`$1`))
		}
		if err := ioutil.WriteFile(filename, data, 0600); err != nil {
			return nil, fmt.Errorf("failed to write modified file %v: %v", filename, err)
		}
		cmdmain.Errorf("%v imports now fixed\n", filename)
	}
	if !c.fix && len(tofix) > 0 {
		return tofix, errImportsNeedsFixing
	}
	return nil, nil
}

func isVendored(file string) bool {
	if strings.HasSuffix(file, ".go") &&
		strings.HasPrefix(file, "third_party/") {
		return true
	}
	return false
}
