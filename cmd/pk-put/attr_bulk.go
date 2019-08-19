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
	"bufio"
	"flag"
	"fmt"
	"io"
	"strings"

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
	return "Add, set, or delete permanode attributes from tab-separated stdin."
}

func (c *attrBulkCmd) Usage() {
	cmdmain.Errorf("Usage: pk-put [globalopts] attr-bulk [--add | --del] < attributes.tsv\n")
	cmdmain.Errorf("Each line: <permanode>\\t<name>\\t<value> (value optional with --del).\n")
	cmdmain.Errorf("Use full permanode blobrefs. Tabs and newlines cannot appear in fields.\n")
	cmdmain.Errorf("Claims are uploaded in order; an error stops processing without rolling back earlier claims.\n")
}

func (c *attrBulkCmd) Examples() []string {
	return []string{
		"< attributes.tsv       Set attributes",
		"--add < attributes.tsv Add attribute values (e.g. tags)",
		"--del < attributes.tsv Delete attribute values, or all values if omitted",
	}
}

func (c *attrBulkCmd) RunCommand(args []string) error {
	if len(args) != 0 {
		return cmdmain.UsageError("attr-bulk takes no positional arguments; it reads tab-separated lines from stdin")
	}
	return c.run(cmdmain.Stdin, func(bb *schema.Builder) error {
		put, err := getUploader().UploadAndSignBlob(ctxbg, bb)
		if err != nil {
			return err
		}
		return handleResult(bb.Type(), put, nil)
	})
}

// run streams claims in input order, stopping at the first error. upload is
// called only after a complete input line has been read and validated.
func (c *attrBulkCmd) run(input io.Reader, upload func(*schema.Builder) error) error {
	if c.add && c.del {
		return cmdmain.UsageError("Add and del options are exclusive")
	}
	claimFunc := schema.NewSetAttributeClaim
	if c.add {
		claimFunc = schema.NewAddAttributeClaim
	} else if c.del {
		claimFunc = schema.NewDelAttributeClaim
	}

	reader := bufio.NewReader(input)
	for line := 1; ; line++ {
		// ReadString handles long lines without truncation or Scanner's
		// default token limit, including a final line without a newline.
		record, err := reader.ReadString('\n')
		if err != nil && err != io.EOF {
			return fmt.Errorf("attr-bulk: line %d: reading stdin: %w", line, err)
		}
		if record == "" && err == io.EOF {
			return nil
		}

		if strings.HasSuffix(record, "\n") {
			record = strings.TrimSuffix(record, "\n")
			record = strings.TrimSuffix(record, "\r")
		}
		fields := strings.Split(record, "\t")
		if len(fields) != 3 && !(c.del && len(fields) == 2) {
			return fmt.Errorf("attr-bulk: line %d: expected <permanode>\\t<name>\\t<value> (value optional with --del)", line)
		}
		pn, ok := blob.Parse(fields[0])
		if !ok {
			return fmt.Errorf("attr-bulk: line %d: invalid permanode blobref %q", line, fields[0])
		}
		if fields[1] == "" {
			return fmt.Errorf("attr-bulk: line %d: attribute name must not be empty", line)
		}
		value := ""
		if len(fields) == 3 {
			value = fields[2]
		}
		if err := upload(claimFunc(pn, fields[1], value)); err != nil {
			return fmt.Errorf("attr-bulk: line %d: uploading claim: %w", line, err)
		}
		if err == io.EOF {
			return nil
		}
	}
}
