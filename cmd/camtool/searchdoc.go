/*
Copyright 2014 The Camlistore Authors

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
	"strings"
	"text/tabwriter"

	"camlistore.org/pkg/cmdmain"
	"camlistore.org/pkg/search"
)

type searchDocCmd struct{}

func init() {
	cmdmain.RegisterCommand("searchdoc", func(flags *flag.FlagSet) cmdmain.CommandRunner {
		return new(searchDocCmd)
	})
}

func (c *searchDocCmd) Describe() string {
	return "Provide help on the predicates for search expressions"
}

func (c *searchDocCmd) Usage() {
	cmdmain.Errorf("camtool searchdoc")
}

func (c *searchDocCmd) RunCommand(args []string) error {
	if len(args) > 0 {
		return cmdmain.UsageError("No arguments allowed")
	}

	formattedSearchHelp()
	return nil
}

func formattedSearchHelp() {
	s := search.SearchHelp()
	type help struct{ Name, Description string }
	h := []help{}
	err := json.Unmarshal([]byte(s), &h)
	if err != nil {
		cmdmain.Errorf("%v", err)
		os.Exit(1)
	}

	w := new(tabwriter.Writer)
	w.Init(cmdmain.Stdout, 0, 8, 0, '\t', 0)
	fmt.Fprintln(w, "Predicates for search expressions")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Predicate\tDescription")
	fmt.Fprintln(w)
	for _, predicate := range h {
		desc := strings.Split(predicate.Description, "\n")
		for i, d := range desc {
			if i == 0 {
				fmt.Fprintf(w, "%s\t%s\n", predicate.Name, d)
			} else {
				fmt.Fprintf(w, "\t%s\n", d)
			}
		}
	}
	w.Flush()
}
