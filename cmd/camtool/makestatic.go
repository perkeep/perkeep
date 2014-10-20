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
	"flag"
	"fmt"
	"os"
	"strings"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/cmdmain"
	"camlistore.org/pkg/schema"
	"camlistore.org/pkg/search"
)

type makeStaticCmd struct {
	server string
}

func init() {
	cmdmain.RegisterCommand("makestatic", func(flags *flag.FlagSet) cmdmain.CommandRunner {
		cmd := new(makeStaticCmd)
		flags.StringVar(&cmd.server, "server", "", "Server to search. "+serverFlagHelp)
		return cmd
	})
}

func (c *makeStaticCmd) Describe() string {
	return "Creates a static directory from a permanode set"
}

func (c *makeStaticCmd) Usage() {
	fmt.Fprintf(os.Stderr, "Usage: camtool [globalopts] makestatic [permanode]\n")
}

func (c *makeStaticCmd) Examples() []string {
	return []string{}
}

func (c *makeStaticCmd) RunCommand(args []string) error {
	if len(args) != 1 {
		return cmdmain.UsageError("requires a permanode")
	}
	pn, ok := blob.Parse(args[0])
	if !ok {
		return cmdmain.UsageError("invalid permanode argument")
	}

	cl := newClient(c.server)
	res, err := cl.Describe(&search.DescribeRequest{
		BlobRefs: []blob.Ref{pn},
		Rules: []*search.DescribeRule{
			{
				IfResultRoot: true,
				Attrs:        []string{"camliMember"},
				Rules: []*search.DescribeRule{
					{Attrs: []string{"camliContent"}},
				},
			},
		},
	})
	if err != nil {
		return err
	}

	camliType := func(ref string) string {
		m := res.Meta[ref]
		if m == nil {
			return ""
		}
		return m.CamliType
	}

	var ss schema.StaticSet
	pnDes, ok := res.Meta[pn.String()]
	if !ok {
		return fmt.Errorf("permanode %v not described", pn)
	}
	if pnDes.Permanode == nil {
		return fmt.Errorf("blob %v is not a permanode", pn)
	}
	members := pnDes.Permanode.Attr["camliMember"]
	if len(members) == 0 {
		return fmt.Errorf("permanode %v has no camliMember attributes", pn)
	}
	for _, fileRefStr := range members {
		if camliType(fileRefStr) != "permanode" {
			continue
		}
		contentRef := res.Meta[fileRefStr].Permanode.Attr.Get("camliContent")
		if contentRef == "" {
			continue
		}
		if camliType(contentRef) == "file" {
			ss.Add(blob.MustParse(contentRef))
		}
	}

	b := ss.Blob()
	_, err = cl.UploadBlob(b)
	if err != nil {
		return err
	}
	title := pnDes.Title()
	title = strings.Replace(title, string(os.PathSeparator), "", -1)
	if title == "" {
		title = pn.String()
	}
	dir := schema.NewDirMap(title).PopulateDirectoryMap(b.BlobRef())
	dirBlob := dir.Blob()
	_, err = cl.UploadBlob(dirBlob)
	if err == nil {
		fmt.Println(dirBlob.BlobRef().String())
	}
	return err
}
