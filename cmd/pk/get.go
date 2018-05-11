/*
Copyright 2018 The Perkeep Authors.

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

	"perkeep.org/internal/osutil"
	"perkeep.org/pkg/cmdmain"
)

type getCmd struct {
	binName string // the executable that is actually called, i.e. "pk-get".
}

func init() {
	cmdmain.RegisterCommand("get", func(flags *flag.FlagSet) cmdmain.CommandRunner {
		return &getCmd{
			binName: "pk-get",
		}
	})
}

func (c *getCmd) Describe() string {
	return "Fetches blobs, files, and directories."
}

func (c *getCmd) Usage() {
	panic("pk get Usage should never get called, as we should always end up calling either pk's or pk-get's usage")
}

func (c *getCmd) RunCommand(args []string) error {
	// RunCommand is only implemented to satisfy the CommandRunner interface.
	panic("pk get RunCommand should never get called, as pk is supposed to invoke pk-get instead.")
}

// LookPath returns the full path to the executable that "pk get" actually
// calls, i.e. "pk-get".
func (c *getCmd) LookPath() (string, error) {
	return osutil.LookPathGopath(c.binName)
}
