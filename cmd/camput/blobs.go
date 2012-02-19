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
	"os"

	"camlistore.org/pkg/client"
)

type blobCmd struct{}

func init() {
	RegisterCommand("blob", func(flags *flag.FlagSet) CommandRunner {
		return new(blobCmd)
	})
}

func (c *blobCmd) Usage() {
	fmt.Fprintf(os.Stderr, "Usage: camput [globalopts] blob <files>\n	camput [globalopts] blob -\n")
}

func (c *blobCmd) RunCommand(up *Uploader, args []string) error {
	if len(args) == 0 {
		return errors.New("No files given.")
	}

	var (
		err error
		put *client.PutResult
	)

	for _, arg := range args {
		put, err = up.UploadFileBlob(arg)
		handleResult("blob", put, err)
	}
	return nil
}
