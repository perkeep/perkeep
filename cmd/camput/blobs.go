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
	"bytes"
	"crypto/sha1"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"camlistore.org/pkg/blobref"
	"camlistore.org/pkg/client"
)

type blobCmd struct{}

func init() {
	RegisterCommand("blob", func(flags *flag.FlagSet) CommandRunner {
		return new(blobCmd)
	})
}

func (c *blobCmd) Usage() {
	fmt.Fprintf(os.Stderr, "Usage: camput [globalopts] blob <files>\n	camput [globalopts] blob -\n")
}

func (c *blobCmd) RunCommand(up *Uploader, args []string) error {
	if len(args) == 0 {
		return errors.New("No files given.")
	}

	for _, arg := range args {
		if arg == "-" {
			var buf bytes.Buffer
			size, err := io.Copy(&buf, os.Stdin)
			if err != nil {
				return err
			}
			// TODO(bradfitz,mpl): limit this buffer size?
			file := buf.Bytes()
			s1 := sha1.New()
			size, err = io.Copy(s1, &buf)
			if err != nil {
				return err
			}
			ref := blobref.FromHash("sha1", s1)
			body := io.LimitReader(bytes.NewReader(file), size)
			handle := &client.UploadHandle{ref, size, body}
			put, err := up.Upload(handle)
			handleResult("blob", put, err)
			continue
		}
		put, err := up.UploadFileBlob(arg)
		handleResult("blob", put, err)
	}
	return nil
}
