/*
Copyright 2015 The Camlistore Authors.

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
	"bytes"
	"flag"
	"fmt"
	"io"
	"log"
	"os"

	"camlistore.org/pkg/cmdmain"
	"camlistore.org/pkg/search"
)

type packBlobsCmd struct {
	server string
}

func init() {
	cmdmain.RegisterCommand("packblobs", func(flags *flag.FlagSet) cmdmain.CommandRunner {
		cmd := new(packBlobsCmd)
		flags.StringVar(&cmd.server, "server", "", "Server to search. "+serverFlagHelp)
		return cmd
	})
}

func (c *packBlobsCmd) Describe() string {
	return "Pack related blobs together (migration tool)"
}

func (c *packBlobsCmd) Usage() {
	fmt.Fprintf(os.Stderr, "Usage: camtool [globalopts] packblobs\n")
}

func (c *packBlobsCmd) Examples() []string {
	return []string{}
}

func (c *packBlobsCmd) RunCommand(args []string) error {
	if len(args) != 0 {
		return cmdmain.UsageError("doesn't take arguments")
	}
	req := &search.SearchQuery{
		Limit: -1,
		Sort:  search.BlobRefAsc,
		Constraint: &search.Constraint{
			File: &search.FileConstraint{
				FileSize: &search.IntConstraint{
					Min: 512 << 10,
				},
			},
		},
	}
	cl := newClient(c.server)
	looseClient := cl.NewPathClient("/bs-loose/")

	res, err := cl.Query(req)
	if err != nil {
		return err
	}
	total := len(res.Blobs)
	n := 0
	var buf bytes.Buffer
	for _, sr := range res.Blobs {
		n++
		fileRef := sr.Blob
		rc, _, err := looseClient.Fetch(fileRef)
		if err == os.ErrNotExist {
			fmt.Printf("%d/%d: %v already done\n", n, total, fileRef)
			continue
		}
		if err != nil {
			log.Printf("error fetching %v: %v\n", fileRef, err)
			continue
		}
		buf.Reset()
		_, err = io.Copy(&buf, rc)
		rc.Close()
		if err != nil {
			log.Printf("error reading %v: %v\n", fileRef, err)
			continue
		}
		_, err = cl.ReceiveBlob(fileRef, &buf)
		if err != nil {
			log.Printf("error write %v: %v\n", fileRef, err)
			continue
		}
		fmt.Printf("%d/%d: %v\n", n, total, fileRef)
	}
	return nil
}
