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
	"errors"
	"flag"
	"fmt"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/cmdmain"
	"camlistore.org/pkg/schema"
)

type attrCmd struct {
	add bool
	del bool
	up  *Uploader
}

func init() {
	cmdmain.RegisterCommand("attr", func(flags *flag.FlagSet) cmdmain.CommandRunner {
		cmd := new(attrCmd)
		flags.BoolVar(&cmd.add, "add", false, `Adds attribute (e.g. "tag")`)
		flags.BoolVar(&cmd.del, "del", false, "Deletes named attribute [value]")
		return cmd
	})
}

func (c *attrCmd) Describe() string {
	return "Add, set, or delete a permanode's attribute."
}

func (c *attrCmd) Usage() {
	cmdmain.Errorf("Usage: camput [globalopts] attr [attroption] <permanode> <name> <value>")
}

func (c *attrCmd) Examples() []string {
	return []string{
		"<permanode> <name> <value>         Set attribute",
		"--add <permanode> <name> <value>   Adds attribute (e.g. \"tag\")",
		"--del <permanode> <name> [<value>] Deletes named attribute [value",
	}
}

func (c *attrCmd) RunCommand(args []string) error {
	if len(args) != 3 {
		return errors.New("Attr takes 3 args: <permanode> <attr> <value>")
	}
	permanode, attr, value := args[0], args[1], args[2]

	var err error

	pn, ok := blob.Parse(permanode)
	if !ok {
		return fmt.Errorf("Error parsing blobref %q", permanode)
	}
	bb := schema.NewSetAttributeClaim(pn, attr, value)
	if c.add {
		if c.del {
			return errors.New("Add and del options are exclusive")
		}
		bb = schema.NewAddAttributeClaim(pn, attr, value)
	} else {
		// TODO: del, which can make <value> be optional
		if c.del {
			return errors.New("del not yet implemented")
		}
	}
	put, err := getUploader().UploadAndSignBlob(bb)
	handleResult(bb.Type(), put, err)
	return nil
}
