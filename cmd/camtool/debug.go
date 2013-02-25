/*
Copyright 2011 Google Inc.

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
	"strings"

	"camlistore.org/pkg/cmdmain"
)

var debugSubModes = map[string]*debugSubMode{
	"splits": &debugSubMode{
		doc: "Show splits of provided file.",
		fun: showSplits,
	},
	"mime": &debugSubMode{
		doc: "Show MIME type of provided file.",
		fun: showMIME,
	},
	"exif": &debugSubMode{
		doc: "Show EXIF dump of provided file.",
		fun: showEXIF,
	},
}

type debugSubMode struct {
	doc string
	fun func(string)
}

type debugCmd struct{}

func init() {
	cmdmain.RegisterCommand("debug", func(flags *flag.FlagSet) cmdmain.CommandRunner {
		return new(debugCmd)
	})
}

func (c *debugCmd) Describe() string {
	return "Show misc meta-info from the given file."
}

func (c *debugCmd) Usage() {
	var subModes, docs string
	for k, v := range debugSubModes {
		subModes += k + "|"
		docs += fmt.Sprintf("	%s: %s\n", k, v.doc)
	}
	subModes = strings.TrimRight(subModes, "|")
	fmt.Fprintf(os.Stderr,
		"Usage: camtool [globalopts] debug %s file\n%s",
		subModes, docs)
}

func (c *debugCmd) RunCommand(args []string) error {
	if args == nil || len(args) != 2 {
		return cmdmain.UsageError("Incorrect number of arguments.")
	}
	subMode, ok := debugSubModes[args[0]]
	if !ok {
		return cmdmain.UsageError(fmt.Sprintf("Invalid submode: %v", args[0]))
	}
	subMode.fun(args[1])
	return nil
}
