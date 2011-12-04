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

type collectionCmd struct {
	url string
	tag string

	publish    bool
	rollSplits bool
}

// TODO(mpl): support other kinds of collections (blog posts) where they're ready.
func init() {
	RegisterCommand("collection", func(flags *flag.FlagSet) CommandRunner {
		cmd := new(collectionCmd)
		flags.StringVar(&cmd.url, "url", "", "Path used to create the url.")
		flags.StringVar(&cmd.tag, "tag", "", "Optional collection tag(s). Single value or comma separated.")

		flags.BoolVar(&cmd.publish, "publish", false, "Publish the collection.")
		flags.BoolVar(&cmd.rollSplits, "rolling", false, "Use rolling checksum file splits.")
		flagCacheLog = flags.Bool("logcache", false, "log caching details")

		return cmd
	})
}

func (c *collectionCmd) Usage() {
	fmt.Fprintf(os.Stderr, "Usage: camput [globalopts] collection [fileopts] <file/director(ies)>\n")
}

func (c *collectionCmd) Examples() []string {
	return []string{
		"[opts] <file(s)/director(ies)",
		"--url=little/ponies --tag=ponies *.jpg",
	}
}

func notExist(err os.Error) bool {
	pe, ok := err.(*os.PathError)
	return ok && pe.Error == os.ENOENT
}

func (c *collectionCmd) RunCommand(up *Uploader, args []string) os.Error {
	if len(args) == 0 {
		return UsageError("No files or directories given.")
	}

	var (
		collec *client.PutResult
		pm     *client.PutResult
		file   *client.PutResult
		claim  *client.PutResult
		err    os.Error
	)

	collec, err = up.UploadNewPermanode()
	if err != nil {
		return fmt.Errorf("Uploading permanode: %v", err)
	}

	if c.tag != "" {
		tags := strings.Split(c.tag, ",")
		m := schema.NewSetAttributeClaim(collec.BlobRef, "tag", tags[0])
		for _, tag := range tags {
			m = schema.NewAddAttributeClaim(collec.BlobRef, "tag", tag)
			put, err := up.UploadAndSignMap(m)
			handleResult("claim-permanode-tag", put, err)
		}
	}

	for _, filename := range args {
		file, err = up.UploadFile(filename, c.rollSplits)
		if err != nil {
			if notExist(err) {
				handleResult("file", file, err)
				continue
			} else {
				return fmt.Errorf("Uploading file: %v", err)
			}
		}
		pm, err = up.UploadNewPermanode()
		if err != nil {
			return fmt.Errorf("Uploading permanode: %v", err)
		}
		claim, err = up.UploadAndSignMap(schema.NewSetAttributeClaim(pm.BlobRef, "camliContent", file.BlobRef.String()))
		handleResult("claim-permanode-content", claim, err)
		claim, err = up.UploadAndSignMap(schema.NewAddAttributeClaim(collec.BlobRef, "camliMember", pm.BlobRef.String()))
		handleResult("claim-permanode-member", claim, err)
	}
	if c.publish {
		claim, err = up.UploadAndSignMap(schema.NewSetAttributeClaim(collec.BlobRef, "camliAccess", "public"))
		handleResult("claim-permanode-access", claim, err)
	}
	if c.url != "" {
		url := strings.Trim(c.url, "/")
		picsRoot, err := up.Client.PicsRoot()
		if err != nil {
			return err
		}
		claim, err = up.UploadAndSignMap(schema.NewSetAttributeClaim(picsRoot, "camliPath:"+url, collec.BlobRef.String()))
		handleResult("claim-permanode-access", claim, err)
	}
	return nil
}
