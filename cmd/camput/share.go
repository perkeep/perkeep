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
	"encoding/json"
	"flag"
	"fmt"
	"time"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/cmdmain"
	"camlistore.org/pkg/schema"
	"camlistore.org/pkg/search"
)

type shareCmd struct {
	search     string
	transitive bool
	duration   time.Duration // zero means forever
}

func init() {
	cmdmain.RegisterCommand("share", func(flags *flag.FlagSet) cmdmain.CommandRunner {
		cmd := new(shareCmd)
		flags.StringVar(&cmd.search, "search", "", "share a search result, rather than a single blob. Should be the JSON representation of a search.SearchQuery (see https://camlistore.org/pkg/search/#SearchQuery for details). Exclusive with, and overrides the <blobref> parameter.")
		flags.BoolVar(&cmd.transitive, "transitive", false, "share everything reachable from the given blobref")
		flags.DurationVar(&cmd.duration, "duration", 0, "how long the share claim is valid for. The default of 0 means forever. For valid formats, see http://golang.org/pkg/time/#ParseDuration")
		return cmd
	})
}

func (c *shareCmd) Describe() string {
	return `Grant access to a resource or search by making a "share" blob.`
}

func (c *shareCmd) Usage() {
	fmt.Fprintf(cmdmain.Stderr, `Usage: camput share [opts] [<blobref>]
`)
}

func (c *shareCmd) Examples() []string {
	return []string{
		"-transitive sha1-83896fcb182db73b653181652129d739280766b5",
		`-search='{"expression":"tag:blogphotos is:image","limit":42}'`,
	}
}

func (c *shareCmd) RunCommand(args []string) error {
	unsigned := schema.NewShareRef(schema.ShareHaveRef, c.transitive)

	if c.search != "" {
		if len(args) != 0 {
			return cmdmain.UsageError("when using the -search flag, share takes zero arguments")
		}
		var q search.SearchQuery
		if err := json.Unmarshal([]byte(c.search), &q); err != nil {
			return cmdmain.UsageError(fmt.Sprintf("invalid search: %s", err))
		}
		unsigned.SetShareSearch(&q)
	} else {
		if len(args) != 1 {
			return cmdmain.UsageError("share takes at most one argument")
		}
		target, ok := blob.Parse(args[0])
		if !ok {
			return cmdmain.UsageError("invalid blobref")
		}
		unsigned.SetShareTarget(target)
	}

	if c.duration != 0 {
		unsigned.SetShareExpiration(time.Now().Add(c.duration))
	}

	pr, err := getUploader().UploadAndSignBlob(unsigned)
	handleResult("share", pr, err)
	return nil
}
