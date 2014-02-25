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
		return errors.New("No files given.")
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

func stdinBlobHandle() (uh *client.UploadHandle, err error) {
	var buf bytes.Buffer
	size, err := io.CopyN(&buf, cmdmain.Stdin, constants.MaxBlobSize+1)
	if err == io.EOF {
		err = nil
	}
	if err != nil {
		return
	}
	if size > constants.MaxBlobSize {
		err = fmt.Errorf("blob size cannot be bigger than %d", constants.MaxBlobSize)
	}
	file := buf.Bytes()
	h := blob.NewHash()
	size, err = io.Copy(h, bytes.NewReader(file))
	if err != nil {
		return
	}
	return &client.UploadHandle{
		BlobRef:  blob.RefFromHash(h),
		Size:     uint32(size),
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
		Contents: io.LimitReader(file, int64(size)),
	}, nil
}

func blobDetails(contents io.ReadSeeker) (bref blob.Ref, size uint32, err error) {
	s1 := sha1.New()
	if _, err = contents.Seek(0, 0); err != nil {
		return
	}
	defer func() {
		if _, seekErr := contents.Seek(0, 0); seekErr != nil {
			if err == nil {
				err = seekErr
			} else {
				err = fmt.Errorf("%s, cannot seek back: %v", err, seekErr)
			}
		}
	}()
	sz, err := io.CopyN(s1, contents, constants.MaxBlobSize+1)
	if err == nil || err == io.EOF {
		bref, err = blob.RefFromHash(s1), nil
	} else {
		err = fmt.Errorf("error reading contents: %v", err)
		return
	}
	if sz > constants.MaxBlobSize {
		err = fmt.Errorf("blob size cannot be bigger than %d", constants.MaxBlobSize)
	}
	size = uint32(sz)
	return
}
