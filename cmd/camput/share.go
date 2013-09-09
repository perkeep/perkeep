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
	"time"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/cmdmain"
	"camlistore.org/pkg/schema"
)

type shareCmd struct {
	transitive bool
	duration   time.Duration // zero means forever
}

func init() {
	cmdmain.RegisterCommand("share", func(flags *flag.FlagSet) cmdmain.CommandRunner {
		cmd := new(shareCmd)
		flags.BoolVar(&cmd.transitive, "transitive", false, "share everything reachable from the given blobref")
		flags.DurationVar(&cmd.duration, "duration", 0, "how long the share claim is valid for. The default of 0 means forever. For valid formats, see http://golang.org/pkg/time/#ParseDuration")
		return cmd
	})
}

func (c *shareCmd) Describe() string {
	return `Grant access to a resource by making a "share" blob.`
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
	target, ok := blob.Parse(args[0])
	if !ok {
		return cmdmain.UsageError("invalid blobref")
	}
	unsigned := schema.NewShareRef(schema.ShareHaveRef, target, c.transitive)
	if c.duration != 0 {
		unsigned.SetShareExpiration(time.Now().Add(c.duration))
	}

	pr, err := getUploader().UploadAndSignBlob(unsigned)
	handleResult("share", pr, err)
	return nil
}
