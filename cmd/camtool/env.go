/*
Copyright 2013 The Camlistore Authors

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

package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"camlistore.org/pkg/cmdmain"
	"camlistore.org/pkg/osutil"
)

var envMap = map[string]func() string{
	"configdir":    osutil.CamliConfigDir,
	"clientconfig": osutil.UserClientConfigPath,
	"serverconfig": osutil.UserServerConfigPath,
	"camsrcroot":   srcRoot,
}

type envCmd struct{}

func init() {
	cmdmain.RegisterCommand("env", func(flags *flag.FlagSet) cmdmain.CommandRunner {
		return new(envCmd)
	})
}

func (c *envCmd) Describe() string {
	return "Return Camlistore environment information"
}

func (c *envCmd) Usage() {
	fmt.Fprintf(os.Stderr, "camtool env [key]\n")
}

func (c *envCmd) RunCommand(args []string) error {
	if len(args) == 0 {
		for k, fn := range envMap {
			fmt.Printf("%s: %s\n", k, fn())
		}
		return nil
	}
	if len(args) > 1 {
		return cmdmain.UsageError("only 0 or 1 arguments allowed")
	}
	fn := envMap[args[0]]
	if fn == nil {
		return fmt.Errorf("unknown environment key %q", args[0])
	}
	fmt.Println(fn())
	return nil
}

func srcRoot() string {
	for _, dir := range filepath.SplitList(os.Getenv("GOPATH")) {
		if d := filepath.Join(dir, "src", "camlistore.org"); osutil.DirExists(d) {
			return d
		}
	}
	return ""
}
