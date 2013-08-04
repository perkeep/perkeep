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

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/cmdmain"
)

type removeCmd struct{}

func init() {
	cmdmain.RegisterCommand("remove", func(flags *flag.FlagSet) cmdmain.CommandRunner {
		cmd := new(removeCmd)
		return cmd
	})
}

func (c *removeCmd) Usage() {
	fmt.Fprintf(cmdmain.Stderr, `Usage: camput remove <blobref(s)>

This command is for debugging only.  You're not expected to use it in practice.
`)
}

func (c *removeCmd) RunCommand(args []string) error {
	if len(args) == 0 {
		return cmdmain.ErrUsage
	}
	refs := make([]blob.Ref, 0, len(args))
	for _, s := range args {
		br, ok := blob.Parse(s)
		if !ok {
			return fmt.Errorf("Invalid blobref %q", s)
		}
		refs = append(refs, br)
	}
	return getUploader().RemoveBlobs(refs)
}
