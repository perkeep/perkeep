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

type permanodeCmd struct {
	flags *flag.FlagSet

	name string
	tag  string
}

func init() {
	flags := flag.NewFlagSet("permanode options", flag.ContinueOnError)
	flags.Usage = func() {}
	cmd := &permanodeCmd{flags: flags}

	flags.StringVar(&cmd.name, "name", "", "Optional name attribute to set on permanode")
	flags.StringVar(&cmd.tag, "tag", "", "Optional tag attribute to set on permanode")

	RegisterCommand("permanode", cmd)
}

func (c *permanodeCmd) Usage() {
	fmt.Fprintf(os.Stderr, "Usage: camput [globalopts] permanode [permanodeopts] \n\nPermanode options:\n")
	c.flags.PrintDefaults()
}

func (c *permanodeCmd) RunCommand(up *Uploader, args []string) os.Error {
	if err := c.flags.Parse(args); err != nil {
		return ErrUsage
	}
	args = c.flags.Args()
	if len(args) > 0 {
		return os.NewError("Permanode command doesn't take any additional arguments")
	}

	var (
		permaNode *client.PutResult
		err       os.Error
	)
	permaNode, err = up.UploadNewPermanode()
	handleResult("permanode", permaNode, err)

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
	return nil
}
