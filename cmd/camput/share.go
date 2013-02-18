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

	"camlistore.org/pkg/blobref"
	"camlistore.org/pkg/client"
	"camlistore.org/pkg/cmdmain"
	"camlistore.org/pkg/schema"
)

type shareCmd struct {
	transitive bool
}

func init() {
	cmdmain.RegisterCommand("share", func(flags *flag.FlagSet) cmdmain.CommandRunner {
		cmd := new(shareCmd)
		flags.BoolVar(&cmd.transitive, "transitive", false, "share everything reachable from the given blobref")
		return cmd
	})
}

func (c *shareCmd) Usage() {
	fmt.Fprintf(cmdmain.Stderr, `Usage: camput share [opts] <blobref>
`)
}

func (c *shareCmd) Examples() []string {
	return []string{
		"[opts] <blobref to share via haveref>",
	}
}

func (c *shareCmd) RunCommand(args []string) error {
	if len(args) != 1 {
		return cmdmain.UsageError("share takes exactly one argument, a blobref")
	}
	br := blobref.Parse(args[0])
	if br == nil {
		return cmdmain.UsageError("invalid blobref")
	}
	pr, err := getUploader().UploadShare(br, c.transitive)
	handleResult("share", pr, err)
	return nil
}

func (up *Uploader) UploadShare(target *blobref.BlobRef, transitive bool) (*client.PutResult, error) {
	unsigned := schema.NewShareRef(schema.ShareHaveRef, target, transitive)
	return up.UploadAndSignBlob(unsigned)
}
