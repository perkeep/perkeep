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
	"net/http"
	"os"

	"camlistore.org/pkg/client"
	"camlistore.org/pkg/cmdmain"
)

type discoCmd struct {
	server string
}

func init() {
	cmdmain.RegisterCommand("discovery", func(flags *flag.FlagSet) cmdmain.CommandRunner {
		cmd := new(discoCmd)
		flags.StringVar(&cmd.server, "server", "", "Server to do discovery against. Either a URL prefix (with optional path), a host[:port]), a server alias, or blank to use the Camlistore client config's default server.")
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
	cl := c.client()
	disco, err := cl.DiscoveryDoc()
	if err != nil {
		return err
	}
	_, err = io.Copy(os.Stdout, disco)
	return err
}

func (c *discoCmd) client() *client.Client {
	// TODO: put this in a function somewhere. it's now repeated
	// like 5 times or something. and make sure it deals with the
	// alias case too, as documented in the flags.
	var cl *client.Client
	if c.server == "" {
		cl = client.NewOrFail()
	} else {
		cl = client.New(c.server)
	}
	cl.SetHTTPClient(&http.Client{
		Transport: cl.TransportForConfig(nil),
	})
	cl.SetupAuth()
	return cl
}
