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
	name string
	tag  string

	makePermanode bool
	rollSplits    bool

	havecache, statcache bool
}

func init() {
	RegisterCommand("file", func(flags *flag.FlagSet) CommandRunner {
		cmd := new(fileCmd)
		flags.BoolVar(&cmd.makePermanode, "permanode", false, "Create an associate a new permanode for the uploaded file or directory.")
		flags.StringVar(&cmd.name, "name", "", "Optional name attribute to set on permanode when using -permanode.")
		flags.StringVar(&cmd.tag, "tag", "", "Optional tag(s) to set on permanode when using -permanode. Single value or comma separated.")

		flags.BoolVar(&cmd.havecache, "statcache", false, "Use the stat cache, assuming unchanged files already uploaded in the past are still there. Fast, but potentially dangerous.")
		flags.BoolVar(&cmd.statcache, "havecache", false, "Use the 'have cache', a cache keeping track of what blobs the remote server should already have from previous uploads.")
		flags.BoolVar(&cmd.rollSplits, "rolling", false, "Use rolling checksum file splits.")

		flagCacheLog = flags.Bool("logcache", false, "log caching details")

		return cmd
	})
}

func (c *fileCmd) Usage() {
	fmt.Fprintf(os.Stderr, "Usage: camput [globalopts] file [fileopts] <file/director(ies)>\n")
}

func (c *fileCmd) Examples() []string {
	return []string{
		"[opts] <file(s)/director(ies)",
		"--permanode --name='Homedir backup' --tag=backup,homedir $HOME",
	}
}

func (c *fileCmd) RunCommand(up *Uploader, args []string) os.Error {
	if len(args) == 0 {
		return UsageError("No files or directories given.")
	}
	if c.name != "" && !c.makePermanode {
		return UsageError("Can't set name without using --permanode")
	}
	if c.tag != "" && !c.makePermanode {
		return UsageError("Can't set tag without using --permanode")
	}

	if c.statcache {
		cache := NewFlatStatCache()
		AddSaveHook(func() { cache.Save() })
		up.statCache = cache
	}
	if c.havecache {
		cache := NewFlatHaveCache()
		AddSaveHook(func() { cache.Save() })
		up.haveCache = cache
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
		lastPut, err = up.UploadFile(arg, c.rollSplits)
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
