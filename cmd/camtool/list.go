/*
Copyright 2014 The Camlistore Authors.

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
	"bufio"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/client"
	"camlistore.org/pkg/cmdmain"
	"camlistore.org/pkg/search"
)

type listCmd struct {
	*syncCmd

	describe bool           // whether to describe each blob.
	cl       *client.Client // client used for the describe requests.
}

func init() {
	cmdmain.RegisterCommand("list", func(flags *flag.FlagSet) cmdmain.CommandRunner {
		cmd := &listCmd{
			syncCmd: &syncCmd{
				dest: "stdout",
			},
			describe: false,
		}
		flags.StringVar(&cmd.syncCmd.src, "src", "", "Source blobserver is either a URL prefix (with optional path), a host[:port], a path (starting with /, ./, or ../), or blank to use the Camlistore client config's default host.")
		flags.BoolVar(&cmd.verbose, "verbose", false, "Be verbose.")
		flags.BoolVar(&cmd.describe, "describe", false, "Use describe requests to get each blob's type. Requires a source server with a search endpoint. Mostly used for demos. Requires many extra round-trips to the server currently.")
		return cmd
	})
}

const describeBatchSize = 50

func (c *listCmd) Describe() string {
	return "List blobs on a server."
}

func (c *listCmd) Usage() {
	fmt.Fprintf(os.Stderr, "Usage: camtool [globalopts] list [listopts] \n")
}

func (c *listCmd) Examples() []string {
	return nil
}

func (c *listCmd) RunCommand(args []string) error {
	if !c.describe {
		return c.syncCmd.RunCommand(args)
	}

	stdout := cmdmain.Stdout
	defer func() { cmdmain.Stdout = stdout }()
	pr, pw, err := os.Pipe()
	if err != nil {
		return fmt.Errorf("Could not create pipe to read from stdout: %v", err)
	}
	defer pr.Close()
	cmdmain.Stdout = pw

	if err := c.setClient(); err != nil {
		return err
	}

	scanner := bufio.NewScanner(pr)
	go func() {
		err := c.syncCmd.RunCommand(args)
		if err != nil {
			log.Printf("Error when enumerating source with sync: %v", err)
		}
		pw.Close()
	}()

	blobRefs := make([]blob.Ref, 0, describeBatchSize)
	describe := func() error {
		if len(blobRefs) == 0 {
			return nil
		}
		// TODO(mpl): setting depth to 1, not 0, because otherwise r.depth() in pkg/search/handler.go defaults to 4. Can't remember why we disallowed 0 right now, and I do not want to change that in pkg/search/handler.go and risk breaking things.
		described, err := c.cl.Describe(&search.DescribeRequest{
			BlobRefs: blobRefs,
			Depth:    1,
		})
		if err != nil {
			return fmt.Errorf("Error when describing blobs %v: %v", blobRefs, err)
		}
		for _, v := range blobRefs {
			blob, ok := described.Meta[v.String()]
			if !ok {
				// This can happen if the index is out of sync with the storage we enum from.
				fmt.Fprintf(stdout, "%v <not described>\n", v)
				continue
			}
			detailed := detail(blob)
			if detailed != "" {
				detailed = fmt.Sprintf("\t%v", detailed)
			}
			fmt.Fprintf(stdout, "%v %v%v\n", v, blob.Size, detailed)
		}
		blobRefs = make([]blob.Ref, 0, describeBatchSize)
		return nil
	}
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) != 2 {
			return fmt.Errorf("Bogus output from sync: got %q, wanted \"blobref size\"", scanner.Text())
		}
		blobRefs = append(blobRefs, blob.MustParse(fields[0]))
		if len(blobRefs) == describeBatchSize {
			if err := describe(); err != nil {
				return err
			}
		}
	}
	if err := describe(); err != nil {
		return err
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("Error reading on pipe from stdout: %v", err)
	}
	return nil
}

// setClient configures a client for c, for the describe requests.
func (c *listCmd) setClient() error {
	ss, err := c.syncCmd.storageFromParam("src", c.syncCmd.src)
	if err != nil {
		fmt.Errorf("Could not set client for describe requests: %v", err)
	}
	var ok bool
	c.cl, ok = ss.(*client.Client)
	if !ok {
		return fmt.Errorf("storageFromParam returned a %T, was expecting a *client.Client", ss)
	}
	return nil
}

func detail(blob *search.DescribedBlob) string {
	// TODO(mpl): attrType, value for claim. but I don't think they're accessible just with a describe req.
	if blob.CamliType == "file" {
		return fmt.Sprintf("%v (%v size=%v)", blob.CamliType, blob.File.FileName, blob.File.Size)
	}
	return blob.CamliType
}
