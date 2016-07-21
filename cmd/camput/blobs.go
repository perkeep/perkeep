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
	"strings"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/client"
	"camlistore.org/pkg/cmdmain"
	"camlistore.org/pkg/constants"
	"camlistore.org/pkg/schema"
)

type blobCmd struct {
	title string
	tag   string

	makePermanode bool // make new, unique permanode of the blob
}

func init() {
	cmdmain.RegisterCommand("blob", func(flags *flag.FlagSet) cmdmain.CommandRunner {
		cmd := new(blobCmd)
		flags.BoolVar(&cmd.makePermanode, "permanode", false, "Create and associate a new permanode for the blob.")
		flags.StringVar(&cmd.title, "title", "", "Optional title attribute to set on permanode when using -permanode.")
		flags.StringVar(&cmd.tag, "tag", "", "Optional tag(s) to set on permanode when using -permanode. Single value or comma separated.")
		return cmd
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
		"--permanode --title='Homedir backup' --tag=backup,homedir $HOME",
		"-           (read from stdin)",
	}
}

func (c *blobCmd) RunCommand(args []string) error {
	if len(args) == 0 {
		return errors.New("No files given.")
	}
	if c.title != "" && !c.makePermanode {
		return cmdmain.UsageError("Can't set title without using --permanode")
	}
	if c.tag != "" && !c.makePermanode {
		return cmdmain.UsageError("Can't set tag without using --permanode")
	}

	up := getUploader()
	if c.makePermanode {
		testSigBlobRef := up.Client.SignerPublicKeyBlobref()
		if !testSigBlobRef.Valid() {
			return cmdmain.UsageError("A GPG key is needed to create permanodes; configure one or use vivify mode.")
		}
	}

	var (
		handle    *client.UploadHandle
		permaNode *client.PutResult
		put       *client.PutResult
		err       error
	)

	for _, arg := range args {
		if arg == "-" {
			handle, err = stdinBlobHandle()
		} else {
			handle, err = fileBlobHandle(up, arg)
		}
		if err != nil {
			return err
		}
		put, err = up.Upload(handle)
		handleResult("blob", put, err)
		continue
	}

	if c.makePermanode {
		permaNode, err = up.UploadNewPermanode()
		if err != nil {
			return fmt.Errorf("Uploading permanode: %v", err)
		}
	}

	if permaNode != nil && put != nil {
		put, err := up.UploadAndSignBlob(schema.NewSetAttributeClaim(permaNode.BlobRef, "camliContent", put.BlobRef.String()))
		if handleResult("claim-permanode-content", put, err) != nil {
			return err
		}
		if c.title != "" {
			put, err := up.UploadAndSignBlob(schema.NewSetAttributeClaim(permaNode.BlobRef, "title", c.title))
			handleResult("claim-permanode-title", put, err)
		}
		if c.tag != "" {
			tags := strings.Split(c.tag, ",")
			for _, tag := range tags {
				m := schema.NewAddAttributeClaim(permaNode.BlobRef, "tag", tag)
				put, err := up.UploadAndSignBlob(m)
				handleResult("claim-permanode-tag", put, err)
			}
		}
		handleResult("permanode", permaNode, nil)
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
