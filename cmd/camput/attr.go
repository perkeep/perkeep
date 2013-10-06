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
		"<permanode> <name> <value>       Set attribute",
		"--add <permanode> <name> <value> Adds attribute (e.g. \"tag\")",
		"--del <permanode> <name> [<value>] Deletes named attribute",
	}
}

func (c *attrCmd) RunCommand(args []string) error {
	if err := c.checkArgs(args); err != nil {
		return err
	}
	permanode, attr := args[0], args[1]
	value := ""
	if len(args) > 2 {
		value = args[2]
	}

	pn, ok := blob.Parse(permanode)
	if !ok {
		return fmt.Errorf("Error parsing blobref %q", permanode)
	}
	claimFunc := func() func(blob.Ref, string, string) *schema.Builder {
		switch {
		case c.add:
			return schema.NewAddAttributeClaim
		case c.del:
			return schema.NewDelAttributeClaim
		default:
			return schema.NewSetAttributeClaim
		}
	}()
	bb := claimFunc(pn, attr, value)
	put, err := getUploader().UploadAndSignBlob(bb)
	handleResult(bb.Type(), put, err)
	return nil
}

func (c *attrCmd) checkArgs(args []string) error {
	if c.del {
		if c.add {
			return cmdmain.UsageError("Add and del options are exclusive")
		}
		if len(args) < 2 {
			return cmdmain.UsageError("Attr -del takes at least 2 args: <permanode> <attr> [<value>]")
		}
		return nil
	}
	if len(args) != 3 {
		return cmdmain.UsageError("Attr takes 3 args: <permanode> <attr> <value>")
	}
	return nil
}
