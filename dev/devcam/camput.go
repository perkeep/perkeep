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

// This program runs camput in "dev" mode,
// to facilitate hacking on camlistore.
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"camlistore.org/pkg/cmdmain"
)

type putCmd struct {
	// start of flag vars
	altkey  bool
	path    string
	port    string
	tls     bool
	noBuild bool
	// end of flag vars

	verbose bool // set by CAMLI_QUIET
	env     *Env
}

func init() {
	cmdmain.RegisterCommand("put", func(flags *flag.FlagSet) cmdmain.CommandRunner {
		cmd := &putCmd{
			env: NewCopyEnv(),
		}
		flags.BoolVar(&cmd.altkey, "altkey", false, "Use different gpg key and password from the server's.")
		flags.BoolVar(&cmd.tls, "tls", false, "Use TLS.")
		flags.StringVar(&cmd.path, "path", "/", "Optional URL prefix path.")
		flags.StringVar(&cmd.port, "port", "3179", "Port camlistore is listening on.")
		flags.BoolVar(&cmd.noBuild, "nobuild", false, "Do not rebuild anything.")
		return cmd
	})
}

func (c *putCmd) Usage() {
	fmt.Fprintf(cmdmain.Stderr, "Usage: devcam put [put_opts] camput_args\n")
}

func (c *putCmd) Examples() []string {
	return []string{
		"file --filenodes /mnt/camera/DCIM",
	}
}

func (c *putCmd) Describe() string {
	return "run camput in dev mode."
}

func (c *putCmd) RunCommand(args []string) error {
	err := c.checkFlags(args)
	if err != nil {
		return cmdmain.UsageError(fmt.Sprint(err))
	}
	if err := c.build(); err != nil {
		return fmt.Errorf("Could not build camput: %v", err)
	}
	if err := c.setEnvVars(); err != nil {
		return fmt.Errorf("Could not setup the env vars: %v", err)
	}

	blobserver := "http://localhost:" + c.port + c.path
	if c.tls {
		blobserver = strings.Replace(blobserver, "http://", "https://", 1)
	}

	cmdBin := filepath.Join("bin", "camput")
	cmdArgs := []string{
		"-verbose=" + strconv.FormatBool(c.verbose),
		"-server=" + blobserver,
	}
	cmdArgs = append(cmdArgs, args...)
	return runExec(cmdBin, cmdArgs, c.env)
}

func (c *putCmd) checkFlags(args []string) error {
	if _, err := strconv.ParseInt(c.port, 0, 0); err != nil {
		return fmt.Errorf("Invalid -port value: %q", c.port)
	}
	return nil
}

func (c *putCmd) build() error {
	cmdName := "camput"
	target := filepath.Join("camlistore.org", "cmd", cmdName)
	binPath := filepath.Join("bin", cmdName)
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

func (c *putCmd) setEnvVars() error {
	c.env.Set("CAMLI_CONFIG_DIR", filepath.Join("config", "dev-client-dir"))
	c.env.Set("CAMLI_SECRET_RING", filepath.FromSlash(defaultSecring))
	c.env.Set("CAMLI_KEYID", defaultKeyID)
	c.env.Set("CAMLI_AUTH", "userpass:camlistore:pass3179")
	c.env.Set("CAMLI_DEV_KEYBLOBS", filepath.FromSlash("config/dev-client-dir/keyblobs"))
	if c.altkey {
		c.env.Set("CAMLI_SECRET_RING", filepath.FromSlash("pkg/jsonsign/testdata/password-foo-secring.gpg"))
		c.env.Set("CAMLI_KEYID", "C7C3E176")
		println("**\n** Note: password is \"foo\"\n**\n")
	}
	c.verbose, _ = strconv.ParseBool(c.env.m["CAMLI_QUIET"])
	return nil
}
