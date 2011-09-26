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

	"camli/client"
)

type blobCmd struct {
	flags *flag.FlagSet
}

func init() {
	flags := flag.NewFlagSet("blob options", flag.ContinueOnError)
	flags.Usage = func() {}
	cmd := &blobCmd{flags: flags}

	RegisterCommand("blob", cmd)
}

func (c *blobCmd) Usage() {
	fmt.Fprintf(os.Stderr, "Usage: camput [globalopts] blob <files>\n	camput [globalopts] blob -\n")
	c.flags.PrintDefaults()
}

func (c *blobCmd) RunCommand(up *Uploader, args []string) os.Error {
	if err := c.flags.Parse(args); err != nil {
		return ErrUsage
	}
	args = c.flags.Args()
	if len(args) == 0 {
		return os.NewError("No files given.")
	}

	var (
		err os.Error
		put *client.PutResult
	)

	for _, arg := range args {
		put, err = up.UploadFileBlob(arg)
		handleResult("blob", put, err)
	}
	return nil
}
