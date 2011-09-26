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
	flags *flag.FlagSet

	add bool
	del bool
}

func init() {
	flags := flag.NewFlagSet("attr options", flag.ContinueOnError)
	flags.Usage = func() {}
	cmd := &attrCmd{flags: flags}

	flags.BoolVar(&cmd.add, "add", false, `Adds attribute (e.g. "tag")`)
	flags.BoolVar(&cmd.del, "del", false, "Deletes named attribute [value]")

	RegisterCommand("attr", cmd)
}

func (c *attrCmd) Usage() {
	fmt.Fprintf(os.Stderr, "Usage: camput [globalopts] attr [attroption] <permanode> <name> <value> \n Attr options: \n")
	c.flags.PrintDefaults()
}

func (c *attrCmd) RunCommand(up *Uploader, args []string) os.Error {
	if err := c.flags.Parse(args); err != nil {
		return ErrUsage
	}
	args = c.flags.Args()
	if len(args) != 3 {
		return os.NewError("Attr takes 3 args: <permanode> <attr> <value>")
	}
	var err os.Error

	pn := blobref.Parse(args[0])
	if pn == nil {
		return fmt.Errorf("Error parsing blobref %q", args[0])
	}
	m := schema.NewSetAttributeClaim(pn, args[1], args[2])
	if c.add {
		if c.del {
			return os.NewError("Add and del options are exclusive")
		}
		m = schema.NewAddAttributeClaim(pn, args[1], args[2])
	} else {
		// TODO: del
		if c.del {
			return os.NewError("del not yet implemented")
		}
	}
	put, err := up.UploadAndSignMap(m)
	handleResult(m["claimType"].(string), put, err)
	return nil
}
