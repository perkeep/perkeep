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
	"io/ioutil"
	"os"
	"strings"

	"camlistore.org/pkg/cmdmain"
	"camlistore.org/pkg/search"

	"go4.org/strutil"
)

type searchCmd struct {
	server   string
	limit    int
	describe bool
	rawQuery bool
}

func init() {
	cmdmain.RegisterCommand("search", func(flags *flag.FlagSet) cmdmain.CommandRunner {
		cmd := new(searchCmd)
		flags.StringVar(&cmd.server, "server", "", "Server to search. "+serverFlagHelp)
		flags.IntVar(&cmd.limit, "limit", 0, "Limit number of results. 0 is default. Negative means no limit.")
		flags.BoolVar(&cmd.describe, "describe", false, "Describe results as well.")
		flags.BoolVar(&cmd.rawQuery, "rawquery", false, "If true, the provided JSON is a SearchQuery, and not a Constraint. In this case, the -limit flag if non-zero is applied after parsing the JSON.")
		return cmd
	})
}

func (c *searchCmd) Describe() string {
	return "Execute a search query"
}

func (c *searchCmd) Usage() {
	fmt.Fprintf(os.Stderr, "Usage: camtool [globalopts] search <expr or Constraint JSON>\n")
}

func (c *searchCmd) Examples() []string {
	return []string{
		`"loc:paris is:portrait" # expression`,
		`'{"blobrefPrefix":"sha1-f00d"}' # SearchConstraint JSON`,
		`- # piped from stdin`,
	}
}

func (c *searchCmd) RunCommand(args []string) error {
	if len(args) != 1 {
		return cmdmain.UsageError("requires search expression or Constraint JSON")
	}
	q := args[0]
	if q == "-" {
		slurp, err := ioutil.ReadAll(cmdmain.Stdin)
		if err != nil {
			return err
		}
		q = string(slurp)
	}
	q = strings.TrimSpace(q)

	req := &search.SearchQuery{
		Limit: c.limit,
	}
	if c.rawQuery {
		req.Limit = 0 // clear it if they provided it
		if err := json.NewDecoder(strings.NewReader(q)).Decode(&req); err != nil {
			return err
		}
		if c.limit != 0 {
			req.Limit = c.limit
		}
	} else if strutil.IsPlausibleJSON(q) {
		cs := new(search.Constraint)
		if err := json.NewDecoder(strings.NewReader(q)).Decode(&cs); err != nil {
			return err
		}
		req.Constraint = cs
	} else {
		req.Expression = q
	}
	if c.describe {
		req.Describe = &search.DescribeRequest{}
	}

	cl := newClient(c.server)
	res, err := cl.Query(req)
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
