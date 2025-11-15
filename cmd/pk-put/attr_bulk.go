/*
Copyright 2011 The Perkeep Authors

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
    "strings"
    "bufio"
    "os"
    "io"
	"flag"
	"fmt"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/cmdmain"
	"perkeep.org/pkg/schema"

)

type attrBulkCmd struct {
	add bool
	del bool
}

func init() {
	cmdmain.RegisterMode("attr-bulk", func(flags *flag.FlagSet) cmdmain.CommandRunner {
		cmd := new(attrBulkCmd)
		flags.BoolVar(&cmd.add, "add", false, `Adds attribute (e.g. "tag")`)
		flags.BoolVar(&cmd.del, "del", false, "Deletes named attribute [value]")
		return cmd
	})
}

func (c *attrBulkCmd) Describe() string {
	return "Add, set, or delete a permanode's attribute."
}

func (c *attrBulkCmd) Usage() {
	cmdmain.Errorf("Usage: pk-put [globalopts] attr [attroption] <permanode> <name> <value>")
}

func (c *attrBulkCmd) Examples() []string {
	return []string{
		"<permanode> <name> <value>       Set attribute",
		"--add <permanode> <name> <value> Adds attribute (e.g. \"tag\")",
		"--del <permanode> <name> [<value>] Deletes named attribute",
	}
}

func (c *attrBulkCmd) RunCommand(args []string) error {

	reader := bufio.NewReader(os.Stdin)
    uploader := getUploader()

	for {
		input, _, err := reader.ReadLine()
		if err != nil && err == io.EOF {
			break
		}
        args = strings.Split(string(input),"\t")

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
        put, err := uploader.UploadAndSignBlob(ctxbg, bb)
        handleResult(bb.Type(), put, err)
    }
    return nil
}

func (c *attrBulkCmd) checkArgs(args []string) error {
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
