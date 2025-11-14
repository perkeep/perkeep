/*
Copyright 2013 The Perkeep Authors.

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

// This file adds the "get" subcommand to devcam, to run pk-get against the dev server.

package main

import (
	"flag"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"perkeep.org/internal/osutil"
	"perkeep.org/pkg/cmdmain"
)

type getCmd struct {
	// start of flag vars
	altkey bool
	path   string
	port   string
	tls    bool
	// end of flag vars

	env *Env
}

func init() {
	cmdmain.RegisterMode("get", func(flags *flag.FlagSet) cmdmain.CommandRunner {
		cmd := &getCmd{
			env: NewCopyEnv(),
		}
		flags.BoolVar(&cmd.altkey, "altkey", false, "Use different gpg key and password from the server's.")
		flags.StringVar(&cmd.path, "path", "/bs", "Optional URL prefix path.")
		flags.StringVar(&cmd.port, "port", "3179", "Port perkeep is listening on.")
		flags.BoolVar(&cmd.tls, "tls", false, "Use TLS.")
		return cmd
	})
}

func (c *getCmd) Usage() {
	fmt.Fprintf(cmdmain.Stderr, "Usage: devcam get [get_opts] -- pkg-get_args\n")
}

func (c *getCmd) Examples() []string {
	return []string{
		"<blobref>",
		"-- --shared http://localhost:3169/share/<blobref>",
	}
}

func (c *getCmd) Describe() string {
	return "run pk-get in dev mode."
}

func (c *getCmd) RunCommand(args []string) error {
	err := c.checkFlags(args)
	if err != nil {
		return cmdmain.UsageError(fmt.Sprint(err))
	}
	if !*noBuild {
		if err := build(filepath.Join("cmd", "pk-get")); err != nil {
			return fmt.Errorf("Could not build pk-get: %v", err)
		}
	}
	c.env.SetCamdevVars(c.altkey)
	// wipeCacheDir needs to be called after SetCamdevVars, because that is
	// where CAMLI_CACHE_DIR is defined.
	if *wipeCache {
		c.env.wipeCacheDir()
	}

	cmdBin, err := osutil.LookPathGopath("pk-get")
	if err != nil {
		return err
	}
	cmdArgs := []string{
		"-verbose=" + strconv.FormatBool(*cmdmain.FlagVerbose || !quiet),
	}
	if !isSharedMode(args) {
		blobserver := "http://localhost:" + c.port + c.path
		if c.tls {
			blobserver = strings.Replace(blobserver, "http://", "https://", 1)
		}
		cmdArgs = append(cmdArgs, "-server="+blobserver)
	}
	cmdArgs = append(cmdArgs, args...)
	return runExec(cmdBin, cmdArgs, c.env)
}

func (c *getCmd) checkFlags(args []string) error {
	if _, err := strconv.ParseInt(c.port, 0, 0); err != nil {
		return fmt.Errorf("Invalid -port value: %q", c.port)
	}
	return nil
}

func isSharedMode(args []string) bool {
	sharedRgx := regexp.MustCompile("--?shared")
	return slices.ContainsFunc(args, sharedRgx.MatchString)
}
