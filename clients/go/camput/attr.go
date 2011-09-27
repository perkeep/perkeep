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

	"camli/blobref"
	"camli/schema"
)

type attrCmd struct {
	add bool
	del bool
}

func init() {
	RegisterCommand("attr", func(flags *flag.FlagSet) CommandRunner {
		cmd := new(attrCmd)
		flags.BoolVar(&cmd.add, "add", false, `Adds attribute (e.g. "tag")`)
		flags.BoolVar(&cmd.del, "del", false, "Deletes named attribute [value]")
		return cmd
	})
}

func (c *attrCmd) Usage() {
	errf("Usage: camput [globalopts] attr [attroption] <permanode> <name> <value>")
}

func (c *attrCmd) Examples() []string {
	return []string{
		"<permanode> <name> <value>         Set attribute",
		"--add <permanode> <name> <value>   Adds attribute (e.g. \"tag\")",
		"--del <permanode> <name> [<value>] Deletes named attribute [value",
	}
}

func (c *attrCmd) RunCommand(up *Uploader, args []string) os.Error {
	if len(args) != 3 {
		return os.NewError("Attr takes 3 args: <permanode> <attr> <value>")
	}
	permanode, attr, value := args[0], args[1], args[2]

	var err os.Error

	pn := blobref.Parse(permanode)
	if pn == nil {
		return fmt.Errorf("Error parsing blobref %q", permanode)
	}
	m := schema.NewSetAttributeClaim(pn, attr, value)
	if c.add {
		if c.del {
			return os.NewError("Add and del options are exclusive")
		}
		m = schema.NewAddAttributeClaim(pn, attr, value)
	} else {
		// TODO: del, which can make <value> be optional
		if c.del {
			return os.NewError("del not yet implemented")
		}
	}
	put, err := up.UploadAndSignMap(m)
	handleResult(m["claimType"].(string), put, err)
	return nil
}
