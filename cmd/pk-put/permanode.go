/*
Copyright 2011 The Perkeep Authors

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
	"fmt"
	"strings"
	"time"

	"perkeep.org/pkg/client"
	"perkeep.org/pkg/cmdmain"
	"perkeep.org/pkg/schema"
)

type permanodeCmd struct {
	title   string
	tag     string
	key     string // else random
	sigTime string
}

func init() {
	cmdmain.RegisterMode("permanode", func(flags *flag.FlagSet) cmdmain.CommandRunner {
		cmd := new(permanodeCmd)
		flags.StringVar(&cmd.title, "title", "", "Optional 'title' attribute to set on new permanode")
		flags.StringVar(&cmd.tag, "tag", "", "Optional tag(s) to set on new permanode; comma separated.")
		flags.StringVar(&cmd.key, "key", "", "Optional key to create deterministic ('planned') permanodes. Must also use --sigtime.")
		flags.StringVar(&cmd.sigTime, "sigtime", "", "Optional time to put in the OpenPGP signature packet instead of the current time. Required when producing a deterministic permanode (with --key). In format YYYY-MM-DD HH:MM:SS")
		return cmd
	})
}

func (c *permanodeCmd) Describe() string {
	return "Create and upload a permanode."
}

func (c *permanodeCmd) Usage() {
	cmdmain.Errorf("Usage: pk-put [globalopts] permanode [permanodeopts]\n")
}

func (c *permanodeCmd) Examples() []string {
	return []string{
		"                               (create a new permanode)",
		`--title="Some Title" --tag=foo,bar (with attributes added)`,
	}
}

func (c *permanodeCmd) RunCommand(args []string) error {
	if len(args) > 0 {
		return errors.New("Permanode command doesn't take any additional arguments")
	}

	var (
		permaNode *client.PutResult
		err       error
		up        = getUploader()
	)
	if (c.key != "") != (c.sigTime != "") {
		return errors.New("both --key and --sigtime must be used to produce deterministic permanodes")
	}
	if c.key == "" {
		// Normal case, with a random permanode.
		permaNode, err = up.UploadNewPermanode(ctxbg)
	} else {
		const format = "2006-01-02 15:04:05"
		sigTime, err := time.Parse(format, c.sigTime)
		if err != nil {
			return fmt.Errorf("Error parsing time %q; expecting time of form %q", c.sigTime, format)
		}
		permaNode, err = up.UploadPlannedPermanode(ctxbg, c.key, sigTime) //lint:ignore SA4006 used by handleResult below
	}
	if handleResult("permanode", permaNode, err) != nil {
		return err
	}

	if c.title != "" {
		put, err := up.UploadAndSignBlob(ctxbg, schema.NewSetAttributeClaim(permaNode.BlobRef, "title", c.title))
		handleResult("claim-permanode-title", put, err)
	}
	if c.tag != "" {
		tags := strings.SplitSeq(c.tag, ",")
		for tag := range tags {
			m := schema.NewAddAttributeClaim(permaNode.BlobRef, "tag", tag)
			put, err := up.UploadAndSignBlob(ctxbg, m)
			handleResult("claim-permanode-tag", put, err)
		}
	}
	return nil
}
