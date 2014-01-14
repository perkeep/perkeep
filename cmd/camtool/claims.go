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
	"log"
	"net/http"
	"os"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/client"
	"camlistore.org/pkg/cmdmain"
	"camlistore.org/pkg/search"
)

type claimsCmd struct {
	src string

	logger *log.Logger
}

func init() {
	cmdmain.RegisterCommand("claims", func(flags *flag.FlagSet) cmdmain.CommandRunner {
		cmd := new(claimsCmd)
		flags.StringVar(&cmd.src, "src", "", "Source blobserver is either a URL prefix (with optional path), a host[:port], a path (starting with /, ./, or ../), or blank to use the Camlistore client config's default host.")
		return cmd
	})
}

func (c *claimsCmd) Describe() string {
	return "Ask the search system to describe one or more blobs."
}

func (c *claimsCmd) Usage() {
	fmt.Fprintf(os.Stderr, "Usage: camtool [globalopts] describe [--depth=n] blobref [blobref, blobref...]\n")
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
	cl := c.client()
	res, err := cl.GetClaims(&search.ClaimsRequest{
		Permanode: br,
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

func (c *claimsCmd) client() *client.Client {
	var cl *client.Client
	if c.src == "" {
		cl = client.NewOrFail()
	} else {
		cl = client.New(c.src)
	}
	cl.SetLogger(c.logger)
	cl.SetHTTPClient(&http.Client{
		Transport: cl.TransportForConfig(nil),
	})
	cl.SetupAuth()
	return cl
}
