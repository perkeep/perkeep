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
	fmt.Fprintf(stderr, "Usage: camput [globalopts] blob <files>\n	camput [globalopts] blob -\n")
}

func (c *blobCmd) Examples() []string {
	return []string{
		"<files>     (raw, without any metadata)",
		"-           (read from stdin)",
	}
}

func (c *blobCmd) RunCommand(up *Uploader, args []string) error {
	if len(args) == 0 {
		return errors.New("No files given.")
	}

	for _, arg := range args {
		var (
			handle *client.UploadHandle
			err    error
		)
		if arg == "-" {
			handle, err = stdinBlobHandle()
		} else {
			handle, err = fileBlobHandle(up, arg)
		}
		if err != nil {
			return err
		}
		put, err := up.Upload(handle)
		handleResult("blob", put, err)
		continue
	}
	return nil
}

func stdinBlobHandle() (uh *client.UploadHandle, err error) {
	var buf bytes.Buffer
	size, err := io.Copy(&buf, stdin)
	if err != nil {
		return
	}
	// TODO(bradfitz,mpl): limit this buffer size?
	file := buf.Bytes()
	h := blobref.NewHash()
	size, err = io.Copy(h, bytes.NewReader(file))
	if err != nil {
		return
	}
	return &client.UploadHandle{
		BlobRef:  blobref.FromHash(h),
		Size:     size,
		Contents: io.LimitReader(bytes.NewReader(file), size),
	}, nil
}

func fileBlobHandle(up *Uploader, path string) (uh *client.UploadHandle, err error) {
	fi, err := up.stat(path)
	if err != nil {
		return
	}
	if fi.Mode()&os.ModeType != 0 {
		return nil, fmt.Errorf("%q is not a regular file", path)
	}
	file, err := up.open(path)
	if err != nil {
		return
	}
	ref, size, err := blobDetails(file)
	if err != nil {
		return nil, err
	}
	return &client.UploadHandle{
		BlobRef:  ref,
		Size:     size,
		Contents: io.LimitReader(file, size),
	}, nil
}

func blobDetails(contents io.ReadSeeker) (bref *blobref.BlobRef, size int64, err error) {
	s1 := sha1.New()
	contents.Seek(0, 0)
	size, err = io.Copy(s1, contents)
	if err == nil {
		bref = blobref.FromHash(s1)
	}
	contents.Seek(0, 0)
	return
}
