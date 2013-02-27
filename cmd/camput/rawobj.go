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

	"camlistore.org/pkg/cmdmain"
	"camlistore.org/pkg/schema"
)

type rawCmd struct {
	vals   string // pipe separated key=value "camliVersion=1|camliType=foo", etc
	signed bool
}

func init() {
	cmdmain.RegisterCommand("rawobj", func(flags *flag.FlagSet) cmdmain.CommandRunner {
		cmd := new(rawCmd)
		flags.StringVar(&cmd.vals, "vals", "", "Pipe-separated key=value properties")
		flags.BoolVar(&cmd.signed, "signed", true, "whether to sign the JSON object")
		return cmd
	})
}

func (c *rawCmd) Describe() string {
	return "Upload a custom JSON schema blob."
}

func (c *rawCmd) Usage() {
	cmdmain.Errorf("Usage: camput [globalopts] rawobj [rawopts]\n")
}

func (c *rawCmd) Examples() []string {
	return []string{"(debug command)"}
}

func (c *rawCmd) RunCommand(args []string) error {
	if len(args) > 0 {
		return errors.New("Raw Object command doesn't take any additional arguments")
	}

	if c.vals == "" {
		return errors.New("No values")
	}

	bb := schema.NewBuilder()
	for _, kv := range strings.Split(c.vals, "|") {
		kv := strings.SplitN(kv, "=", 2)
		bb.SetRawStringField(kv[0], kv[1])
	}

	up := getUploader()
	if c.signed {
		put, err := up.UploadAndSignBlob(bb)
		handleResult("raw-object-signed", put, err)
		return err
	}
	cj, err := bb.JSON()
	if err != nil {
		return err
	}
	put, err := up.uploadString(cj)
	handleResult("raw-object-unsigned", put, err)
	return err
}
