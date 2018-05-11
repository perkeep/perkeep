/*
Copyright 2013 The Perkeep Authors

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
	"log"
	"os"
	"path/filepath"

	"perkeep.org/internal/osutil"
	"perkeep.org/pkg/client"
	"perkeep.org/pkg/cmdmain"
)

var envMap = map[string]func() string{
	"configdir": func() string {
		dir, err := osutil.PerkeepConfigDir()
		if err != nil {
			log.Fatal(err)
		}
		return dir
	},
	"clientconfig": osutil.UserClientConfigPath,
	"serverconfig": osutil.UserServerConfigPath,
	"srcroot":      envSrcRoot,
	"secretring":   envSecretRingFile,
}

type envCmd struct{}

func init() {
	cmdmain.RegisterMode("env", func(flags *flag.FlagSet) cmdmain.CommandRunner {
		return new(envCmd)
	})
}

func (c *envCmd) Describe() string {
	return "Return Perkeep environment information"
}

func (c *envCmd) Usage() {
	fmt.Fprintf(os.Stderr, "pk env [key]\n")
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

func envSrcRoot() string {
	for _, dir := range filepath.SplitList(os.Getenv("GOPATH")) {
		if d := filepath.Join(dir, "src", "perkeep.org"); osutil.DirExists(d) {
			return d
		}
	}
	return ""
}

func envSecretRingFile() string {
	cc, err := client.New()
	if err != nil {
		return ""
	}
	return cc.SecretRingFile()
}
