/*
Copyright 2014 The Camlistore Authors.

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

	"camlistore.org/pkg/cmdmain"
)

type listCmd struct {
	*syncCmd
}

func init() {
	cmdmain.RegisterCommand("list", func(flags *flag.FlagSet) cmdmain.CommandRunner {
		cmd := &listCmd{
			&syncCmd{
				dest: "stdout",
			},
		}
		flags.StringVar(&cmd.src, "src", "", "Source blobserver is either a URL prefix (with optional path), a host[:port], a path (starting with /, ./, or ../), or blank to use the Camlistore client config's default host.")
		flags.BoolVar(&cmd.verbose, "verbose", false, "Be verbose.")
		return cmd
	})
}

func (c *listCmd) Describe() string {
	return "List blobs on a server."
}

func (c *listCmd) Usage() {
	fmt.Fprintf(os.Stderr, "Usage: camtool [globalopts] list [listopts] \n")
}

func (c *listCmd) Examples() []string {
	return nil
}
