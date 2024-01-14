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
	"encoding/json"
	"errors"
	"flag"
	"os"
	"regexp"

	"perkeep.org/internal/osutil"
	_ "perkeep.org/internal/osutil/gce"
	"perkeep.org/pkg/cmdmain"
	"perkeep.org/pkg/serverinit"
)

type dumpconfigCmd struct{}

func init() {
	cmdmain.RegisterMode("dumpconfig", func(flags *flag.FlagSet) cmdmain.CommandRunner {
		return new(dumpconfigCmd)
	})
}

func (c *dumpconfigCmd) Describe() string {
	return "Dump the low-level server config from its simple config."
}

func (c *dumpconfigCmd) Usage() {
}

func (c *dumpconfigCmd) RunCommand(args []string) error {
	var file string
	switch {
	case len(args) == 0:
		file = osutil.UserServerConfigPath()
	case len(args) == 1:
		file = args[0]
	default:
		return errors.New("More than 1 argument not allowed")
	}
	cfg, err := serverinit.LoadFile(file)
	if err != nil {
		return err
	}
	lowj, err := json.MarshalIndent(cfg.LowLevelJSONConfig(), "", "  ") //lint:ignore SA1019 we use it still
	if err != nil {
		return err
	}
	knownKeys := regexp.MustCompile(`(?ms)^\s+"_knownkeys": {.+?},?\n`)
	lowj = knownKeys.ReplaceAll(lowj, nil)
	_, err = os.Stdout.Write(lowj)
	return err
}
