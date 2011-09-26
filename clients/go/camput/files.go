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

	"camli/client"
	"camli/schema"
)

type fileCmd struct {
	flags *flag.FlagSet

	name string
	tag  string

	makePermanode bool
}

func init() {
	flags := flag.NewFlagSet("file options", flag.ContinueOnError)
	flags.Usage = func() {}
	cmd := &fileCmd{flags: flags}

	flags.BoolVar(&cmd.makePermanode, "permanode", false, "Create an associate a new permanode for the uploaded file or directory.")
	flags.StringVar(&cmd.name, "name", "", "Optional name attribute to set on permanode when using -permanode and -file")
	flags.StringVar(&cmd.tag, "tag", "", "Optional tag attribute to set on permanode when using -permanode and -file. Single value or comma separated ones.")

	RegisterCommand("file", cmd)
}

func (c *fileCmd) Usage() {
	fmt.Fprintf(os.Stderr, "Usage: camput [globalopts] file [fileopts] <file/director(ies)>\n\nFile options:\n")
	c.flags.PrintDefaults()
}

func (c *fileCmd) RunCommand(up *Uploader, args []string) os.Error {
	if err := c.flags.Parse(args); err != nil {
		return ErrUsage
	}
	args = c.flags.Args()
	if len(args) == 0 {
		return os.NewError("No files or directories given.")
	}

	var (
		permaNode *client.PutResult
		lastPut   *client.PutResult
		err       os.Error
	)
	if c.makePermanode {
		if len(args) != 1 {
			return fmt.Errorf("The --permanode flag can only be used with exactly one file or directory argument")
		}
		permaNode, err = up.UploadNewPermanode()
		if err != nil {
			return fmt.Errorf("Uploading permanode: %v", err)
		}
	}

	for _, arg := range args {
		//if *flagBlob {
		//lastPut, err = up.UploadFileBlob(flag.Arg(n))
		//		handleResult("blob", lastPut, err)
		lastPut, err = up.UploadFile(arg)
		handleResult("file", lastPut, err)

		if permaNode != nil {
			put, err := up.UploadAndSignMap(schema.NewSetAttributeClaim(permaNode.BlobRef, "camliContent", lastPut.BlobRef.String()))
			handleResult("claim-permanode-content", put, err)
			if c.name != "" {
				put, err := up.UploadAndSignMap(schema.NewSetAttributeClaim(permaNode.BlobRef, "name", c.name))
				handleResult("claim-permanode-name", put, err)
			}
			if c.tag != "" {
				tags := strings.Split(c.tag, ",")
				m := schema.NewSetAttributeClaim(permaNode.BlobRef, "tag", tags[0])
				for _, tag := range tags {
					m = schema.NewAddAttributeClaim(permaNode.BlobRef, "tag", tag)
					put, err := up.UploadAndSignMap(m)
					handleResult("claim-permanode-tag", put, err)
				}
			}
			handleResult("permanode", permaNode, nil)
		}
	}
	return nil
}
