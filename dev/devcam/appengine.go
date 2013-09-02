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

// This program runs the development appengine
// camlistore with dev_appserver.py.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"camlistore.org/pkg/cmdmain"
	"camlistore.org/pkg/osutil"
)

type gaeCmd struct {
	// start of flag vars
	all  bool
	port string
	sdk  string
	wipe bool
	// end of flag vars

	camliSrcRoot   string // the camlistore source tree
	applicationDir string // App Engine application dir: camliSrcRoot/server/appengine
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
	c.camliSrcRoot, err = osutil.GoPackagePath("camlistore.org")
	if err != nil {
		return errors.New("Package camlistore.org not found in $GOPATH (or $GOPATH not defined).")
	}
	c.applicationDir = filepath.Join(c.camliSrcRoot, "server", "appengine")
	if _, err := os.Stat(c.applicationDir); err != nil {
		return fmt.Errorf("Appengine application dir not found at %s", c.applicationDir)
	}
	if err = c.checkSDK(); err != nil {
		return err
	}
	if err = c.mirrorSourceRoot(); err != nil {
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
	cmdArgs = append(cmdArgs, c.applicationDir)
	return runExec(devAppServerBin, cmdArgs)
}

func (c *gaeCmd) checkFlags(args []string) error {
	if _, err := strconv.ParseInt(c.port, 0, 0); err != nil {
		return fmt.Errorf("Invalid -port value: %q", c.port)
	}
	return nil
}

func (c *gaeCmd) checkSDK() error {
	defaultSDK := filepath.Join(c.camliSrcRoot, "appengine-sdk")
	if c.sdk == "" {
		c.sdk = defaultSDK
	}
	if _, err := os.Stat(c.sdk); err != nil {
		return fmt.Errorf("App Engine SDK not found. Please specify it with --sdk or:\n$ ln -s /path/to/appengine-go-sdk %s\n\n", defaultSDK)
	}
	return nil
}

func (c *gaeCmd) mirrorSourceRoot() error {
	uiDirs := []string{"server/camlistored/ui", "third_party/closure/lib/closure", "pkg/server"}
	for _, dir := range uiDirs {
		oriPath := filepath.Join(c.camliSrcRoot, filepath.FromSlash(dir))
		dstPath := filepath.Join(c.applicationDir, "source_root", filepath.FromSlash(dir))
		if err := cpDir(oriPath, dstPath, []string{".go"}); err != nil {
			return fmt.Errorf("Error while mirroring %s to %s: %v", oriPath, dstPath, err)
		}
	}
	return nil
}
