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

// This file adds the "tool" subcommand to devcam, to run camtool against
// the dev server.

package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"

	"camlistore.org/pkg/cmdmain"
)

type toolCmd struct {
	// start of flag vars
	altkey  bool
	noBuild bool
	// end of flag vars

	verbose bool // set by CAMLI_QUIET
	env     *Env
}

func init() {
	cmdmain.RegisterCommand("tool", func(flags *flag.FlagSet) cmdmain.CommandRunner {
		cmd := &toolCmd{
			env: NewCopyEnv(),
		}
		flags.BoolVar(&cmd.altkey, "altkey", false, "Use different gpg key and password from the server's.")
		flags.BoolVar(&cmd.noBuild, "nobuild", false, "Do not rebuild anything.")
		return cmd
	})
}

func (c *toolCmd) Usage() {
	fmt.Fprintf(cmdmain.Stderr, "Usage: devcam tool [globalopts] <mode> [commandopts] [commandargs]\n")
}

func (c *toolCmd) Examples() []string {
	return []string{
		"sync --all",
	}
}

func (c *toolCmd) Describe() string {
	return "run camtool in dev mode."
}

func (c *toolCmd) RunCommand(args []string) error {
	if err := c.build(); err != nil {
		return fmt.Errorf("Could not build camtool: %v", err)
	}
	if err := c.setEnvVars(); err != nil {
		return fmt.Errorf("Could not setup the env vars: %v", err)
	}

	cmdBin := filepath.Join("bin", "camtool")
	return runExec(cmdBin, args, c.env)
}

func (c *toolCmd) build() error {
	cmdName := "camtool"
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

func (c *toolCmd) setEnvVars() error {
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
	c.verbose, _ = strconv.ParseBool(os.Getenv("CAMLI_QUIET"))
	return nil
}
