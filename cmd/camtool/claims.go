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
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/cmdmain"
	"camlistore.org/pkg/search"
)

type claimsCmd struct {
	server string
	attr   string
}

func init() {
	cmdmain.RegisterCommand("claims", func(flags *flag.FlagSet) cmdmain.CommandRunner {
		cmd := new(claimsCmd)
		flags.StringVar(&cmd.server, "server", "", "Server to fetch claims from. "+serverFlagHelp)
		flags.StringVar(&cmd.attr, "attr", "", "Filter claims about a specific attribute. If empty, all claims are returned.")
		return cmd
	})
}

func (c *claimsCmd) Describe() string {
	return "Ask the search system to list the claims that modify a permanode."
}

func (c *claimsCmd) Usage() {
	fmt.Fprintf(os.Stderr, "Usage: camtool [globalopts] claims [--depth=n] [--attr=s] permanodeBlobRef\n")
}

func (c *claimsCmd) Examples() []string {
	return []string{}
}

func (c *claimsCmd) RunCommand(args []string) error {
	if len(args) != 1 {
		return cmdmain.UsageError("requires 1 blobref")
	}
	br, ok := blob.Parse(args[0])
	if !ok {
		return cmdmain.UsageError("invalid blobref")
	}
	cl := newClient(c.server)
	res, err := cl.GetClaims(&search.ClaimsRequest{
		Permanode:  br,
		AttrFilter: c.attr,
	})
	if err != nil {
		return err
	}
	resj, err := json.MarshalIndent(res, "", "  ")
	if err != nil {
		return err
	}
	resj = append(resj, '\n')
	_, err = os.Stdout.Write(resj)
	return err
}
