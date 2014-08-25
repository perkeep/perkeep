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

// This file adds the "put" subcommand to devcam, to run camput against the dev server.

package main

import (
	"flag"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"camlistore.org/pkg/cmdmain"
)

type putCmd struct {
	// start of flag vars
	altkey bool
	path   string
	port   string
	tls    bool
	// end of flag vars

	env *Env
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
	if !*noBuild {
		if err := build(filepath.Join("cmd", "camput")); err != nil {
			return fmt.Errorf("Could not build camput: %v", err)
		}
	}
	c.env.SetCamdevVars(c.altkey)
	// wipeCacheDir needs to be called after SetCamdevVars, because that is
	// where CAMLI_CACHE_DIR is defined.
	if *wipeCache {
		c.env.wipeCacheDir()
	}

	blobserver := "http://localhost:" + c.port + c.path
	if c.tls {
		blobserver = strings.Replace(blobserver, "http://", "https://", 1)
	}

	cmdBin := filepath.Join("bin", "camput")
	cmdArgs := []string{
		"-verbose=" + strconv.FormatBool(*cmdmain.FlagVerbose || !quiet),
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
