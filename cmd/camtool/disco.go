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
	"io"
	"os"

	"camlistore.org/pkg/cmdmain"
)

type discoCmd struct {
	server  string
	httpVer bool
}

func init() {
	cmdmain.RegisterCommand("discovery", func(flags *flag.FlagSet) cmdmain.CommandRunner {
		cmd := new(discoCmd)
		flags.StringVar(&cmd.server, "server", "", "Server to do discovery against. "+serverFlagHelp)
		flags.BoolVar(&cmd.httpVer, "httpversion", false, "discover the HTTP version")
		return cmd
	})
}

func (c *discoCmd) Describe() string {
	return "Perform configuration discovery against a server."
}

func (c *discoCmd) Usage() {
	fmt.Fprintf(os.Stderr, "Usage: camtool [globalopts] discovery")
}

func (c *discoCmd) Examples() []string {
	return []string{}
}

func (c *discoCmd) RunCommand(args []string) error {
	if len(args) > 0 {
		return cmdmain.UsageError("doesn't take args")
	}
	cl := newClient(c.server)
	if c.httpVer {
		v, err := cl.HTTPVersion()
		if err != nil {
			return err
		}
		fmt.Println(v)
		return nil
	}
	disco, err := cl.DiscoveryDoc()
	if err != nil {
		return err
	}
	_, err = io.Copy(os.Stdout, disco)
	return err
}
