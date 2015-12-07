/*
Copyright 2013 The Camlistore Authors.

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

// This file adds the "test" subcommand to devcam, to run the full test suite.

package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"camlistore.org/pkg/cmdmain"
)

type testCmd struct {
	// start of flag vars
	verbose   bool
	precommit bool
	short     bool
	run       string
	// end of flag vars

	// buildGoPath becomes our child "go" processes' GOPATH environment variable
	buildGoPath string
}

func init() {
	cmdmain.RegisterCommand("test", func(flags *flag.FlagSet) cmdmain.CommandRunner {
		cmd := new(testCmd)
		flags.BoolVar(&cmd.short, "short", false, "Use '-short' with go test.")
		flags.BoolVar(&cmd.precommit, "precommit", true, "Run the pre-commit githook as part of tests.")
		flags.BoolVar(&cmd.verbose, "v", false, "Use '-v' (for verbose) with go test.")
		flags.StringVar(&cmd.run, "run", "", "Use '-run' with go test.")
		return cmd
	})
}

func (c *testCmd) Usage() {
	fmt.Fprintf(cmdmain.Stderr, "Usage: devcam test [test_opts] [targets]\n")
}

func (c *testCmd) Describe() string {
	return "run the full test suite, or the tests in the specified target packages."
}

func (c *testCmd) RunCommand(args []string) error {
	if c.precommit {
		if err := c.runPrecommitHook(); err != nil {
			return err
		}
	}
	if err := c.syncSrc(); err != nil {
		return err
	}
	buildSrcDir := filepath.Join(c.buildGoPath, "src", "camlistore.org")
	if err := os.Chdir(buildSrcDir); err != nil {
		return err
	}
	if err := c.buildSelf(); err != nil {
		return err
	}
	if err := c.runTests(args); err != nil {
		return err
	}
	println("PASS")
	return nil
}

func (c *testCmd) env() *Env {
	if c.buildGoPath == "" {
		panic("called too early")
	}
	env := NewCopyEnv()
	env.NoGo()
	env.Set("GOPATH", c.buildGoPath)
	env.Set("CAMLI_MAKE_USEGOPATH", "true")
	env.Set("GO15VENDOREXPERIMENT", "1")
	return env
}

func (c *testCmd) syncSrc() error {
	args := []string{"run", "make.go", "--onlysync"}
	cmd := exec.Command("go", args...)
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("Error populating tmp src tree: %v", err)
	}
	c.buildGoPath = strings.TrimSpace(string(out))
	return nil
}

func (c *testCmd) buildSelf() error {
	args := []string{
		"install",
		filepath.FromSlash("./dev/devcam"),
	}
	cmd := exec.Command("go", args...)
	binDir, err := filepath.Abs("bin")
	if err != nil {
		return fmt.Errorf("Error setting GOBIN: %v", err)
	}
	env := c.env()
	env.Set("GOBIN", binDir)
	cmd.Env = env.Flat()
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("Error building devcam: %v", err)
	}
	return nil
}

func (c *testCmd) runTests(args []string) error {
	targs := []string{"test"}
	if !strings.HasSuffix(c.buildGoPath, "-nosqlite") {
		targs = append(targs, "--tags=with_sqlite fake_android")
	} else {
		targs = append(targs, "--tags=fake_android")
	}
	if c.short {
		targs = append(targs, "-short")
	}
	if c.verbose {
		targs = append(targs, "-v")
	}
	if c.run != "" {
		targs = append(targs, "-run="+c.run)
	}
	if len(args) > 0 {
		targs = append(targs, args...)
	} else {
		targs = append(targs, []string{
			"./pkg/...",
			"./server/camlistored",
			"./server/appengine",
			"./cmd/...",
			"./misc/docker/...",
			"./website",
		}...)
	}
	env := c.env()
	env.Set("SKIP_DEP_TESTS", "1")
	return runExec("go", targs, env)
}

func (c *testCmd) runPrecommitHook() error {
	out, err := exec.Command(filepath.FromSlash("./bin/devcam"), "hook", "pre-commit", "test").CombinedOutput()
	if err != nil {
		fmt.Println(string(out))
	}
	return err

}
