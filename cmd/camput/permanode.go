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
	"strings"

	"camlistore.org/pkg/client"
	"camlistore.org/pkg/schema"
)

type permanodeCmd struct {
	name string
	tag  string
}

func init() {
	RegisterCommand("permanode", func(flags *flag.FlagSet) CommandRunner {
		cmd := new(permanodeCmd)
		flags.StringVar(&cmd.name, "name", "", "Optional name attribute to set on new permanode")
		flags.StringVar(&cmd.tag, "tag", "", "Optional tag(s) to set on new permanode; comma separated.")
		return cmd
	})
}

func (c *permanodeCmd) Usage() {
	errf("Usage: camput [globalopts] permanode [permanodeopts]\n")
}

func (c *permanodeCmd) Examples() []string {
	return []string{
		"                               (create a new permanode)",
		`-name="Some Name" -tag=foo,bar (with attributes added)`,
	}
}

func (c *permanodeCmd) RunCommand(up *Uploader, args []string) error {
	if len(args) > 0 {
		return errors.New("Permanode command doesn't take any additional arguments")
	}

	var (
		permaNode *client.PutResult
		err       error
	)
	permaNode, err = up.UploadNewPermanode()
	handleResult("permanode", permaNode, err)

	if c.name != "" {
		put, err := up.UploadAndSignMap(schema.NewSetAttributeClaim(permaNode.BlobRef, "title", c.name))
		handleResult("claim-permanode-title", put, err)
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
	return nil
}
