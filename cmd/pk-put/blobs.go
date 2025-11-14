/*
Copyright 2011 The Perkeep Authors

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
	"hash"
	"io"
	"log"
	"os"
	"strings"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/client"
	"perkeep.org/pkg/cmdmain"
	"perkeep.org/pkg/constants"
	"perkeep.org/pkg/schema"
)

type blobCmd struct {
	title string
	tag   string

	makePermanode bool   // make new, unique permanode of the blob
	hashFunc      string // empty means automatic
}

func init() {
	cmdmain.RegisterMode("blob", func(flags *flag.FlagSet) cmdmain.CommandRunner {
		cmd := new(blobCmd)
		flags.BoolVar(&cmd.makePermanode, "permanode", false, "Create and associate a new permanode for the blob.")
		flags.StringVar(&cmd.title, "title", "", "Optional title attribute to set on permanode when using -permanode.")
		flags.StringVar(&cmd.tag, "tag", "", "Optional tag(s) to set on permanode when using -permanode. Single value or comma separated.")
		flags.StringVar(&cmd.hashFunc, "hash", "", "Name of hash algorithm to use. Empty means to use the current recommended one.")
		return cmd
	})
}

func (c *blobCmd) Describe() string {
	return "Upload raw blob(s)."
}

func (c *blobCmd) Usage() {
	fmt.Fprintf(cmdmain.Stderr, "Usage: pk-put [globalopts] blob <files>\n	pk-put [globalopts] blob -\n")
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
		return errors.New("no files given")
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
			handle, err = c.fileBlobHandle(up, arg)
		}
		if err != nil {
			return err
		}
		put, err = up.Upload(ctxbg, handle)
		handleResult("blob", put, err)
		continue
	}

	if c.makePermanode {
		permaNode, err = up.UploadNewPermanode(ctxbg)
		if err != nil {
			return fmt.Errorf("Uploading permanode: %v", err)
		}
	}

	if permaNode != nil && put != nil {
		put, err := up.UploadAndSignBlob(ctxbg, schema.NewSetAttributeClaim(permaNode.BlobRef, "camliContent", put.BlobRef.String()))
		if handleResult("claim-permanode-content", put, err) != nil {
			return err
		}
		if c.title != "" {
			put, err := up.UploadAndSignBlob(ctxbg, schema.NewSetAttributeClaim(permaNode.BlobRef, "title", c.title))
			handleResult("claim-permanode-title", put, err)
		}
		if c.tag != "" {
			tags := strings.SplitSeq(c.tag, ",")
			for tag := range tags {
				m := schema.NewAddAttributeClaim(permaNode.BlobRef, "tag", tag)
				put, err := up.UploadAndSignBlob(ctxbg, m)
				handleResult("claim-permanode-tag", put, err)
			}
		}
		handleResult("permanode", permaNode, nil)
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

func (c *blobCmd) fileBlobHandle(up *Uploader, path string) (*client.UploadHandle, error) {
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
	h := c.newHash()
	if _, err := h.Write(buf); err != nil {
		return nil, err
	}
	return &client.UploadHandle{
		BlobRef:  blob.RefFromHash(h),
		Size:     uint32(size),
		Contents: bytes.NewReader(buf),
	}, nil
}

func (c *blobCmd) newHash() hash.Hash {
	switch c.hashFunc {
	case "":
		return blob.NewHash()
	case "sha1":
		return sha1.New()
	default:
		// TODO: move all this into the blob package?
		log.Fatalf("unsupported hash function %q", c.hashFunc)
		panic("unreachable")
	}
}
