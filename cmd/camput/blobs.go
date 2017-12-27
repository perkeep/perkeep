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
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/client"
	"camlistore.org/pkg/cmdmain"
	"camlistore.org/pkg/constants"
)

type blobCmd struct{}

func init() {
	cmdmain.RegisterCommand("blob", func(flags *flag.FlagSet) cmdmain.CommandRunner {
		return new(blobCmd)
	})
}

func (c *blobCmd) Describe() string {
	return "Upload raw blob(s)."
}

func (c *blobCmd) Usage() {
	fmt.Fprintf(cmdmain.Stderr, "Usage: camput [globalopts] blob <files>\n	camput [globalopts] blob -\n")
}

func (c *blobCmd) Examples() []string {
	return []string{
		"<files>     (raw, without any metadata)",
		"-           (read from stdin)",
	}
}

func (c *blobCmd) RunCommand(args []string) error {
	if len(args) == 0 {
		return errors.New("no files given")
	}

	up := getUploader()
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

func stdinBlobHandle() (*client.UploadHandle, error) {
	var buf bytes.Buffer
	size, err := io.CopyN(&buf, cmdmain.Stdin, constants.MaxBlobSize+1)
	if size > constants.MaxBlobSize {
		return nil, fmt.Errorf("blob size cannot be bigger than %d", constants.MaxBlobSize)
	}
	if err != nil && err != io.EOF {
		return nil, err
	}
	h := blob.NewHash()
	if _, err := h.Write(buf.Bytes()); err != nil {
		return nil, err
	}
	return &client.UploadHandle{
		BlobRef:  blob.RefFromHash(h),
		Size:     uint32(buf.Len()),
		Contents: &buf,
	}, nil
}

func fileBlobHandle(up *Uploader, path string) (*client.UploadHandle, error) {
	fi, err := up.stat(path)
	if err != nil {
		return nil, err
	}
	if fi.Mode()&os.ModeType != 0 {
		return nil, fmt.Errorf("%q is not a regular file", path)
	}
	size := fi.Size()
	if size > constants.MaxBlobSize {
		return nil, fmt.Errorf("blob size cannot be bigger than %d", constants.MaxBlobSize)
	}
	file, err := up.open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	buf := make([]byte, size)
	if _, err := io.ReadFull(file, buf); err != nil {
		return nil, err
	}
	h := blob.NewHash()
	if _, err := h.Write(buf); err != nil {
		return nil, err
	}
	return &client.UploadHandle{
		BlobRef:  blob.RefFromHash(h),
		Size:     uint32(size),
		Contents: bytes.NewReader(buf),
	}, nil
}
