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
	"time"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/cmdmain"
	"camlistore.org/pkg/search"
	"camlistore.org/pkg/types"
)

type desCmd struct {
	server string
	depth  int
}

func init() {
	cmdmain.RegisterCommand("describe", func(flags *flag.FlagSet) cmdmain.CommandRunner {
		cmd := new(desCmd)
		flags.StringVar(&cmd.server, "server", "", "Server to query. "+serverFlagHelp)
		flags.IntVar(&cmd.depth, "depth", 1, "Depth to follow in describe request")
		return cmd
	})
}

func (c *desCmd) Describe() string {
	return "Ask the search system to describe one or more blobs."
}

func (c *desCmd) Usage() {
	fmt.Fprintf(os.Stderr, "Usage: camtool [globalopts] describe [--depth=n] blobref [blobref, blobref...]\n")
}

func (c *desCmd) Examples() []string {
	return []string{}
}

func (c *desCmd) RunCommand(args []string) error {
	if len(args) == 0 {
		return cmdmain.UsageError("requires blobref")
	}
	var blobs []blob.Ref
	for _, arg := range args {
		br, ok := blob.Parse(arg)
		if !ok {
			return cmdmain.UsageError(fmt.Sprintf("invalid blobref %q", arg))
		}
		blobs = append(blobs, br)
	}
	var at time.Time // TODO: implement. from "2 days ago" "-2d", "-2h", "2013-02-05", etc

	cl := newClient(c.server)
	res, err := cl.Describe(&search.DescribeRequest{
		BlobRefs: blobs,
		Depth:    c.depth,
		At:       types.Time3339(at),
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
