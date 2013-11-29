/*
Copyright 2013 The Camlistore Authors.

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

type deleteCmd struct {
	up *Uploader
}

func init() {
	cmdmain.RegisterCommand("delete", func(flags *flag.FlagSet) cmdmain.CommandRunner {
		cmd := new(deleteCmd)
		return cmd
	})
}

func (c *deleteCmd) Describe() string {
	return "Create and upload a delete claim."
}

func (c *deleteCmd) Usage() {
	cmdmain.Errorf("Usage: camput [globalopts] delete <blobref1> [blobref2]...")
}

func (c *deleteCmd) RunCommand(args []string) error {
	if len(args) < 1 {
		return cmdmain.UsageError("Need at least one blob to delete.")
	}
	if err := delete(args); err != nil {
		return err
	}
	return nil
}

func delete(args []string) error {
	for _, arg := range args {
		br, ok := blob.Parse(arg)
		if !ok {
			return fmt.Errorf("Error parsing blobref %q", arg)
		}
		bb := schema.NewDeleteClaim(br)
		put, err := getUploader().UploadAndSignBlob(bb)
		if err := handleResult(bb.Type(), put, err); err != nil {
			return err
		}
	}
	return nil
}
