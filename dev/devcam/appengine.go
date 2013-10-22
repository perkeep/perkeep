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

// This file adds the "appengine" subcommand to devcam, to run the
// development appengine camlistore with dev_appserver.py.

package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"camlistore.org/pkg/cmdmain"
)

type gaeCmd struct {
	// start of flag vars
	all  bool
	port string
	sdk  string
	wipe bool
	// end of flag vars
}

func init() {
	cmdmain.RegisterCommand("appengine", func(flags *flag.FlagSet) cmdmain.CommandRunner {
		cmd := new(gaeCmd)
		flags.BoolVar(&cmd.all, "all", false, "Listen on all interfaces.")
		flags.StringVar(&cmd.port, "port", "3179", "Port to listen on.")
		flags.StringVar(&cmd.sdk, "sdk", "", "The path to the App Engine Go SDK (or a symlink to it).")
		flags.BoolVar(&cmd.wipe, "wipe", false, "Wipe the blobs on disk and the indexer.")
		return cmd
	})
}

func (c *gaeCmd) Usage() {
	fmt.Fprintf(cmdmain.Stderr, "Usage: devcam [globalopts] appengine [cmdopts] [other_dev_appserver_opts] \n")
}

func (c *gaeCmd) Describe() string {
	return "run the App Engine camlistored in dev mode."
}

func (c *gaeCmd) RunCommand(args []string) error {
	err := c.checkFlags(args)
	if err != nil {
		return cmdmain.UsageError(fmt.Sprint(err))
	}
	applicationDir := filepath.Join("server", "appengine")
	if _, err := os.Stat(applicationDir); err != nil {
		return fmt.Errorf("Appengine application dir not found at %s", applicationDir)
	}
	if err = c.checkSDK(); err != nil {
		return err
	}
	if err = c.mirrorSourceRoot(applicationDir); err != nil {
		return err
	}

	devAppServerBin := filepath.Join(c.sdk, "dev_appserver.py")
	cmdArgs := []string{
		"--skip_sdk_update_check",
		fmt.Sprintf("--port=%s", c.port),
	}
	if c.all {
		cmdArgs = append(cmdArgs, "--host", "0.0.0.0")
	}
	if c.wipe {
		cmdArgs = append(cmdArgs, "--clear_datastore")
	}
	cmdArgs = append(cmdArgs, args...)
	cmdArgs = append(cmdArgs, applicationDir)
	return runExec(devAppServerBin, cmdArgs, NewCopyEnv())
}

func (c *gaeCmd) checkFlags(args []string) error {
	if _, err := strconv.ParseInt(c.port, 0, 0); err != nil {
		return fmt.Errorf("Invalid -port value: %q", c.port)
	}
	return nil
}

func (c *gaeCmd) checkSDK() error {
	defaultSDK := "appengine-sdk"
	if c.sdk == "" {
		c.sdk = defaultSDK
	}
	if _, err := os.Stat(c.sdk); err != nil {
		return fmt.Errorf("App Engine SDK not found. Please specify it with --sdk or:\n$ ln -s /path/to/appengine-go-sdk %s\n\n", defaultSDK)
	}
	return nil
}

func (c *gaeCmd) mirrorSourceRoot(gaeAppDir string) error {
	uiDirs := []string{"server/camlistored/ui", "third_party/closure/lib/closure", "pkg/server"}
	for _, dir := range uiDirs {
		oriPath := filepath.Join(camliSrcRoot, filepath.FromSlash(dir))
		dstPath := filepath.Join(gaeAppDir, "source_root", filepath.FromSlash(dir))
		if err := cpDir(oriPath, dstPath, []string{".go"}); err != nil {
			return fmt.Errorf("Error while mirroring %s to %s: %v", oriPath, dstPath, err)
		}
	}
	return nil
}
