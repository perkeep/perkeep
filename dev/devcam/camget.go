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

// This program runs camget in "dev" mode,
// to facilitate hacking on camlistore.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"camlistore.org/pkg/cmdmain"
	"camlistore.org/pkg/osutil"
)

type getCmd struct {
	// start of flag vars
	path string
	port string
	tls  bool
	// end of flag vars

	verbose      string // set by CAMLI_QUIET
	camliSrcRoot string // the camlistore source tree
}

func init() {
	cmdmain.RegisterCommand("get", func(flags *flag.FlagSet) cmdmain.CommandRunner {
		cmd := new(getCmd)
		flags.StringVar(&cmd.path, "path", "/bs", "Optional URL prefix path.")
		flags.StringVar(&cmd.port, "port", "3179", "Port camlistore is listening on.")
		flags.BoolVar(&cmd.tls, "tls", false, "Use TLS.")
		cmd.verbose = "false"
		return cmd
	})
}

func (c *getCmd) Usage() {
	fmt.Fprintf(cmdmain.Stderr, "Usage: devcam get [get_opts] -- camget_args\n")
}

func (c *getCmd) Examples() []string {
	return []string{
		"<blobref>",
		"-- --shared http://localhost:3169/share/<blobref>",
	}
}

func (c *getCmd) Describe() string {
	return "run camget in dev mode."
}

func (c *getCmd) RunCommand(args []string) error {
	err := c.checkFlags(args)
	if err != nil {
		return cmdmain.UsageError(fmt.Sprint(err))
	}
	c.camliSrcRoot, err = osutil.GoPackagePath("camlistore.org")
	if err != nil {
		return errors.New("Package camlistore.org not found in $GOPATH (or $GOPATH not defined).")
	}
	err = os.Chdir(c.camliSrcRoot)
	if err != nil {
		return fmt.Errorf("Could not chdir to %v: %v", c.camliSrcRoot, err)
	}
	if err := c.setEnvVars(); err != nil {
		return fmt.Errorf("Could not setup the env vars: %v", err)
	}

	cmdBin := filepath.Join(c.camliSrcRoot, "bin", "camget")
	cmdArgs := []string{
		"-verbose=" + c.verbose,
	}
	if !isSharedMode(args) {
		blobserver := "http://localhost:" + c.port + c.path
		if c.tls {
			blobserver = strings.Replace(blobserver, "http://", "https://", 1)
		}
		cmdArgs = append(cmdArgs, "-server="+blobserver)
	}
	cmdArgs = append(cmdArgs, args...)
	return runExec(cmdBin, cmdArgs)
}

func (c *getCmd) checkFlags(args []string) error {
	if _, err := strconv.ParseInt(c.port, 0, 0); err != nil {
		return fmt.Errorf("Invalid -port value: %q", c.port)
	}
	return nil
}

func (c *getCmd) build(name string) error {
	cmdName := "camget"
	target := filepath.Join("camlistore.org", "cmd", cmdName)
	binPath := filepath.Join(c.camliSrcRoot, "bin", cmdName)
	var modtime int64
	fi, err := os.Stat(binPath)
	if err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("Could not stat %v: %v", binPath, err)
		}
	} else {
		modtime = fi.ModTime().Unix()
	}
	args := []string{
		"run", "make.go",
		"--quiet",
		"--embed_static=false",
		fmt.Sprintf("--if_mods_since=%d", modtime),
		"--targets=" + target,
	}
	cmd := exec.Command("go", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("Error building %v: %v", target, err)
	}
	return nil
}

func (c *getCmd) setEnvVars() error {
	setenv("CAMLI_CONFIG_DIR", filepath.Join(c.camliSrcRoot, "config", "dev-client-dir"))
	setenv("CAMLI_SECRET_RING", filepath.Join(c.camliSrcRoot,
		filepath.FromSlash("pkg/jsonsign/testdata/test-secring.gpg")))
	setenv("CAMLI_KEYID", "26F5ABDA")
	setenv("CAMLI_AUTH", "userpass:camlistore:pass3179")
	setenv("CAMLI_DEV_KEYBLOBS", filepath.Join(c.camliSrcRoot,
		filepath.FromSlash("config/dev-client-dir/keyblobs")))
	c.verbose, _ = strconv.ParseBool(os.Getenv("CAMLI_QUIET"))
	return nil
}

func isSharedMode(args []string) bool {
	sharedRgx := regexp.MustCompile("--?shared")
	for _, v := range args {
		if sharedRgx.MatchString(v) {
			return true
		}
	}
	return false
}
