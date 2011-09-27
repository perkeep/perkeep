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

	"camli/blobref"
	"camli/client"
	"camli/schema"
)

type shareCmd struct {
	transitive bool
}

func init() {
	RegisterCommand("share", func(flags *flag.FlagSet) CommandRunner {
		cmd := new(shareCmd)
		flags.BoolVar(&cmd.transitive, "transitive", false, "share everything reachable from the given blobref")
		return cmd
	})
}

func (c *shareCmd) Usage() {
	fmt.Fprintf(os.Stderr, `Usage: camput share [opts] <blobref>
`)
}

func (c *shareCmd) RunCommand(up *Uploader, args []string) os.Error {
	if len(args) != 1 {
		return UsageError("share takes exactly one argument, a blobref")
	}
	br := blobref.Parse(flag.Arg(0))
	if br == nil {
		return UsageError("invalid blobref")
	}
	pr, err := up.UploadShare(br, c.transitive)
	handleResult("share", pr, err)
	return nil
}

func (up *Uploader) UploadShare(target *blobref.BlobRef, transitive bool) (*client.PutResult, os.Error) {
	unsigned := schema.NewShareRef(schema.ShareHaveRef, target, transitive)
	return up.UploadAndSignMap(unsigned)
}
