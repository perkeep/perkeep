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

// TODO(bradfitz): maybe increase this? Keep "multi" blobs under a size
// threshold like 1 MiB, and definitely under the 16 MiB blob limit.
const attrBulkBatchSize = 100

// Leave room for the signer and signature within the schema blob size limit.
const attrBulkMaxUnsignedBytes = schema.MaxSchemaBlobSize - 4096

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
	cmdmain.Errorf("Claims are uploaded in ordered batches of up to 100; errors discard buffered claims, not earlier uploads.\n")
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

// run buffers claims in input order, stopping at the first error. A malformed
// record or read error discards buffered claims, not earlier uploads.
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

	claims := make([]*schema.Builder, 0, attrBulkBatchSize)
	firstLine := 1
	flush := func() error {
		if err := uploadAttrBulkBatch(claims, firstLine, upload); err != nil {
			return err
		}
		clear(claims)
		claims = claims[:0]
		return nil
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
			return flush()
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
		if len(claims) == 0 {
			firstLine = line
		}
		claims = append(claims, claimFunc(pn, fields[1], value))
		if len(claims) == attrBulkBatchSize {
			if err := flush(); err != nil {
				return err
			}
		}
		if err == io.EOF {
			return flush()
		}
	}
}

// uploadAttrBulkBatch splits batches whose serialized JSON would exceed the
// size budget. Even a single operation is wrapped in a multi claim.
func uploadAttrBulkBatch(claims []*schema.Builder, firstLine int, upload func(*schema.Builder) error) error {
	if len(claims) == 0 {
		return nil
	}
	lastLine := firstLine + len(claims) - 1
	bb, err := schema.NewMultiClaim(claims...)
	if err != nil {
		return fmt.Errorf("attr-bulk: lines %d-%d: building multi claim: %w", firstLine, lastLine, err)
	}
	unsigned, err := bb.JSON()
	if err != nil {
		return fmt.Errorf("attr-bulk: lines %d-%d: encoding multi claim: %w", firstLine, lastLine, err)
	}
	if len(unsigned) > attrBulkMaxUnsignedBytes {
		if len(claims) == 1 {
			return fmt.Errorf("attr-bulk: line %d: attribute claim exceeds the %d-byte unsigned size limit", firstLine, attrBulkMaxUnsignedBytes)
		}
		mid := len(claims) / 2
		if err := uploadAttrBulkBatch(claims[:mid], firstLine, upload); err != nil {
			return err
		}
		return uploadAttrBulkBatch(claims[mid:], firstLine+mid, upload)
	}
	if err := upload(bb); err != nil {
		return fmt.Errorf("attr-bulk: lines %d-%d: uploading multi claim: %w", firstLine, lastLine, err)
	}
	return nil
}
