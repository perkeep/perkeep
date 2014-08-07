/*
Copyright 2013 Google Inc.

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

	"camlistore.org/pkg/cmdmain"
	"camlistore.org/pkg/osutil"
	_ "camlistore.org/pkg/osutil/gce"
	"camlistore.org/pkg/serverinit"
)

type dumpconfigCmd struct{}

func init() {
	cmdmain.RegisterCommand("dumpconfig", func(flags *flag.FlagSet) cmdmain.CommandRunner {
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
	cfg.Obj["handlerConfig"] = true
	ll, err := json.MarshalIndent(cfg.Obj, "", "  ")
	if err != nil {
		return err
	}
	_, err = os.Stdout.Write(ll)
	return err
}
