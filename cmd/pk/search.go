/*
Copyright 2014 The Perkeep Authors.

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
	"io"
	"os"
	"strings"

	"perkeep.org/pkg/cmdmain"
	"perkeep.org/pkg/search"

	"go4.org/errorutil"
	"go4.org/strutil"
)

type searchCmd struct {
	server   string
	limit    int
	cont     string
	describe bool
	rawQuery bool
	one      bool
}

func init() {
	cmdmain.RegisterMode("search", func(flags *flag.FlagSet) cmdmain.CommandRunner {
		cmd := new(searchCmd)
		flags.StringVar(&cmd.server, "server", "", "Server to search. "+serverFlagHelp)
		flags.IntVar(&cmd.limit, "limit", 0, "Limit number of results. 0 is default. Negative means no limit.")
		flags.StringVar(&cmd.cont, "continue", "", "Continue token from a previously limited search. The query must be identical to the original search.")
		flags.BoolVar(&cmd.describe, "describe", false, "Describe results as well.")
		flags.BoolVar(&cmd.rawQuery, "rawquery", false, "If true, the provided JSON is a SearchQuery, and not a Constraint. In this case, the -limit and -continue flags, if non-zero, are applied after parsing the JSON.")
		flags.BoolVar(&cmd.one, "1", false, "Output one blob ref per line, suitable for piping into xargs.")
		return cmd
	})
}

func (c *searchCmd) Describe() string {
	return "Execute a search query"
}

func (c *searchCmd) Usage() {
	fmt.Fprintf(os.Stderr, "Usage: pk [globalopts] search <expr or Constraint JSON>\n")
}

func (c *searchCmd) Examples() []string {
	return []string{
		`"loc:paris is:portrait" # expression`,
		`'{"blobrefPrefix":"sha224-f00d"}' # SearchConstraint JSON`,
		`- # piped from stdin`,
	}
}

func (c *searchCmd) RunCommand(args []string) error {
	if len(args) != 1 {
		return cmdmain.UsageError("requires search expression or Constraint JSON")
	}
	q := args[0]
	if q == "-" {
		slurp, err := io.ReadAll(cmdmain.Stdin)
		if err != nil {
			return err
		}
		q = string(slurp)
	}
	q = strings.TrimSpace(q)

	req := &search.SearchQuery{
		Limit:    c.limit,
		Continue: c.cont,
	}
	if c.rawQuery {
		req.Limit = 0     // clear it if they provided it
		req.Continue = "" // clear this as well

		if err := json.NewDecoder(strings.NewReader(q)).Decode(&req); err != nil {
			if se, ok := err.(*json.SyntaxError); ok {
				line, col, msg := errorutil.HighlightBytePosition(strings.NewReader(q), se.Offset)
				fmt.Fprintf(os.Stderr, "JSON syntax error at line %d, column %d parsing SearchQuery (https://godoc.org/perkeep.org/pkg/search#SearchQuery):\n%s\n", line, col, msg)
			}
			return err
		}
		if c.limit != 0 {
			req.Limit = c.limit
		}
		if c.cont != "" {
			req.Continue = c.cont
		}
	} else if strutil.IsPlausibleJSON(q) {
		cs := new(search.Constraint)
		if err := json.NewDecoder(strings.NewReader(q)).Decode(&cs); err != nil {
			if se, ok := err.(*json.SyntaxError); ok {
				line, col, msg := errorutil.HighlightBytePosition(strings.NewReader(q), se.Offset)
				fmt.Fprintf(os.Stderr, "JSON syntax error at line %d, column %d parsing Constraint (https://godoc.org/perkeep.org/pkg/search#Constraint):\n%s\n", line, col, msg)
			}
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
	res, err := cl.Query(ctxbg, req)
	if err != nil {
		return err
	}
	if c.one {
		for _, bl := range res.Blobs {
			fmt.Println(bl.Blob)
		}
		return nil
	}
	resj, err := json.MarshalIndent(res, "", "  ")
	if err != nil {
		return err
	}
	resj = append(resj, '\n')
	_, err = os.Stdout.Write(resj)
	return err
}
