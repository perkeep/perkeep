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

// This file adds the "mount" subcommand to devcam, to run cammount against the dev server.

package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"camlistore.org/pkg/cmdmain"
)

type mountCmd struct {
	// start of flag vars
	altkey bool
	path   string
	port   string
	tls    bool
	debug  bool
	// end of flag vars

	env *Env
}

const mountpoint = "/tmp/cammount-dir"

func init() {
	cmdmain.RegisterCommand("mount", func(flags *flag.FlagSet) cmdmain.CommandRunner {
		cmd := &mountCmd{
			env: NewCopyEnv(),
		}
		flags.BoolVar(&cmd.altkey, "altkey", false, "Use different gpg key and password from the server's.")
		flags.BoolVar(&cmd.tls, "tls", false, "Use TLS.")
		flags.StringVar(&cmd.path, "path", "/", "Optional URL prefix path.")
		flags.StringVar(&cmd.port, "port", "3179", "Port camlistore is listening on.")
		flags.BoolVar(&cmd.debug, "debug", false, "print debugging messages.")
		return cmd
	})
}

func (c *mountCmd) Usage() {
	fmt.Fprintf(cmdmain.Stderr, "Usage: devcam mount [mount_opts] [<root-blobref>|<share URL>]\n")
}

func (c *mountCmd) Examples() []string {
	return []string{
		"",
		"http://localhost:3169/share/<blobref>",
	}
}

func (c *mountCmd) Describe() string {
	return "run cammount in dev mode."
}

func tryUnmount(dir string) error {
	if runtime.GOOS == "darwin" {
		return exec.Command("diskutil", "umount", "force", dir).Run()
	}
	return exec.Command("fusermount", "-u", dir).Run()
}

func (c *mountCmd) RunCommand(args []string) error {
	err := c.checkFlags(args)
	if err != nil {
		return cmdmain.UsageError(fmt.Sprint(err))
	}
	if !*noBuild {
		if err := build(filepath.Join("cmd", "cammount")); err != nil {
			return fmt.Errorf("Could not build cammount: %v", err)
		}
	}
	c.env.SetCamdevVars(c.altkey)
	// wipeCacheDir needs to be called after SetCamdevVars, because that is
	// where CAMLI_CACHE_DIR is defined.
	if *wipeCache {
		c.env.wipeCacheDir()
	}

	tryUnmount(mountpoint)
	if err := os.Mkdir(mountpoint, 0700); err != nil && !os.IsExist(err) {
		return fmt.Errorf("Could not make mount point: %v", err)
	}

	blobserver := "http://localhost:" + c.port + c.path
	if c.tls {
		blobserver = strings.Replace(blobserver, "http://", "https://", 1)
	}

	cmdBin := filepath.Join("bin", "cammount")
	cmdArgs := []string{
		"-debug=" + strconv.FormatBool(c.debug),
		"-server=" + blobserver,
	}
	cmdArgs = append(cmdArgs, args...)
	cmdArgs = append(cmdArgs, mountpoint)
	fmt.Printf("Cammount running with mountpoint %v. Press 'q' <enter> or ctrl-c to shut down.\n", mountpoint)
	return runExec(cmdBin, cmdArgs, c.env)
}

func (c *mountCmd) checkFlags(args []string) error {
	if _, err := strconv.ParseInt(c.port, 0, 0); err != nil {
		return fmt.Errorf("Invalid -port value: %q", c.port)
	}
	return nil
}
