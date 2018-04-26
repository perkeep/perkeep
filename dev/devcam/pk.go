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

// This file adds the "tool" subcommand to devcam, to run pk against
// the dev server.

package main

import (
	"flag"
	"fmt"
	"path/filepath"

	"perkeep.org/internal/osutil"
	"perkeep.org/pkg/cmdmain"
)

type toolCmd struct {
	// start of flag vars
	altkey bool
	// end of flag vars

	env *Env
}

func init() {
	cmdmain.RegisterMode("tool", func(flags *flag.FlagSet) cmdmain.CommandRunner {
		cmd := &toolCmd{
			env: NewCopyEnv(),
		}
		flags.BoolVar(&cmd.altkey, "altkey", false, "Use different gpg key and password from the server's.")
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
	return "run pk in dev mode."
}

func (c *toolCmd) RunCommand(args []string) error {
	if !*noBuild {
		if err := build(filepath.Join("cmd", "pk")); err != nil {
			return fmt.Errorf("Could not build pk: %v", err)
		}
	}
	c.env.SetCamdevVars(c.altkey)
	// wipeCacheDir needs to be called after SetCamdevVars, because that is
	// where CAMLI_CACHE_DIR is defined.
	if *wipeCache {
		c.env.wipeCacheDir()
	}

	cmdBin, err := osutil.LookPathGopath("pk")
	if err != nil {
		return err
	}
	return runExec(cmdBin, args, c.env)
}
