/*
Copyright 2014 The Perkeep Authors.

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
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/cmdmain"
	"perkeep.org/pkg/schema"
	"perkeep.org/pkg/search"
)

type makeStaticCmd struct {
	server string
}

func init() {
	cmdmain.RegisterMode("makestatic", func(flags *flag.FlagSet) cmdmain.CommandRunner {
		cmd := new(makeStaticCmd)
		flags.StringVar(&cmd.server, "server", "", "Server to search. "+serverFlagHelp)
		return cmd
	})
}

func (c *makeStaticCmd) Describe() string {
	return "Creates a static directory from a permanode set"
}

func (c *makeStaticCmd) Usage() {
	fmt.Fprintf(os.Stderr, "Usage: pk [globalopts] makestatic [permanode]\n")
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
	res, err := cl.Describe(context.Background(), &search.DescribeRequest{
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

	camliType := func(ref string) schema.CamliType {
		m := res.Meta[ref]
		if m == nil {
			return ""
		}
		return m.CamliType
	}

	ss := schema.NewStaticSet()
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
	var memberRefs []blob.Ref
	for _, fileRefStr := range members {
		if camliType(fileRefStr) != "permanode" {
			continue
		}
		contentRef := res.Meta[fileRefStr].Permanode.Attr.Get("camliContent")
		if contentRef == "" {
			continue
		}
		if camliType(contentRef) == "file" {
			memberRefs = append(memberRefs, blob.MustParse(contentRef))
		}
	}
	subsets := ss.SetStaticSetMembers(memberRefs)

	// Large directories may have subsets. Upload any of those too:
	for _, v := range subsets {
		if _, err := cl.UploadBlob(context.Background(), v); err != nil {
			return err
		}
	}
	b := ss.Blob()
	_, err = cl.UploadBlob(ctxbg, b)
	if err != nil {
		return err
	}
	title := pnDes.Title()
	title = strings.ReplaceAll(title, string(os.PathSeparator), "")
	if title == "" {
		title = pn.String()
	}
	dir := schema.NewDirMap(title).PopulateDirectoryMap(b.BlobRef())
	dirBlob := dir.Blob()
	_, err = cl.UploadBlob(ctxbg, dirBlob)
	if err == nil {
		fmt.Println(dirBlob.BlobRef().String())
	}
	return err
}
