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

/*
Package cond registers the "cond" conditional blobserver storage type
to select routing of get/put operations on blobs to other storage
targets as a function of their content.

Currently only the "isSchema" predicate is defined.

Example usage:

  "/bs-and-maybe-also-index/": {
	"handler": "storage-cond",
	"handlerArgs": {
		"write": {
			"if": "isSchema",
			"then": "/bs-and-index/",
			"else": "/bs/"
		},
		"read": "/bs/"
	}
  }
*/
package cond

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/blobserver"
	"camlistore.org/pkg/schema"
	"go4.org/jsonconfig"
	"golang.org/x/net/context"
)

const buffered = 8

// A storageFunc selects a destination for a given blob. It may consume from src but must always return
// a newSrc that is identical to the original src passed in.
type storageFunc func(br blob.Ref, src io.Reader) (dest blobserver.Storage, newSrc io.Reader, err error)

type condStorage struct {
	storageForReceive storageFunc
	read              blobserver.Storage
	remove            blobserver.Storage

	ctx *http.Request // optional per-request context
}

func (sto *condStorage) StorageGeneration() (initTime time.Time, random string, err error) {
	if gener, ok := sto.read.(blobserver.Generationer); ok {
		return gener.StorageGeneration()
	}
	err = blobserver.GenerationNotSupportedError(fmt.Sprintf("blobserver.Generationer not implemented on %T", sto.read))
	return
}

func (sto *condStorage) ResetStorageGeneration() error {
	if gener, ok := sto.read.(blobserver.Generationer); ok {
		return gener.ResetStorageGeneration()
	}
	return blobserver.GenerationNotSupportedError(fmt.Sprintf("blobserver.Generationer not implemented on %T", sto.read))
}

func newFromConfig(ld blobserver.Loader, conf jsonconfig.Obj) (storage blobserver.Storage, err error) {
	sto := &condStorage{}

	receive := conf.OptionalStringOrObject("write")
	read := conf.RequiredString("read")
	remove := conf.OptionalString("remove", "")
	if err := conf.Validate(); err != nil {
		return nil, err
	}

	if receive != nil {
		sto.storageForReceive, err = buildStorageForReceive(ld, receive)
		if err != nil {
			return
		}
	}

	sto.read, err = ld.GetStorage(read)
	if err != nil {
		return
	}

	if remove != "" {
		sto.remove, err = ld.GetStorage(remove)
		if err != nil {
			return
		}
	}
	return sto, nil
}

func buildStorageForReceive(ld blobserver.Loader, confOrString interface{}) (storageFunc, error) {
	// Static configuration from a string
	if s, ok := confOrString.(string); ok {
		sto, err := ld.GetStorage(s)
		if err != nil {
			return nil, err
		}
		f := func(br blob.Ref, src io.Reader) (blobserver.Storage, io.Reader, error) {
			return sto, src, nil
		}
		return f, nil
	}

	conf := jsonconfig.Obj(confOrString.(map[string]interface{}))

	ifStr := conf.RequiredString("if")
	// TODO: let 'then' and 'else' point to not just strings but either
	// a string or a JSON object with another condition, and then
	// call buildStorageForReceive on it recursively
	thenTarget := conf.RequiredString("then")
	elseTarget := conf.RequiredString("else")
	if err := conf.Validate(); err != nil {
		return nil, err
	}
	thenSto, err := ld.GetStorage(thenTarget)
	if err != nil {
		return nil, err
	}
	elseSto, err := ld.GetStorage(elseTarget)
	if err != nil {
		return nil, err
	}

	switch ifStr {
	case "isSchema":
		return isSchemaPicker(thenSto, elseSto), nil
	}
	return nil, fmt.Errorf("cond: unsupported 'if' type of %q", ifStr)
}

func isSchemaPicker(thenSto, elseSto blobserver.Storage) storageFunc {
	return func(br blob.Ref, src io.Reader) (dest blobserver.Storage, newSrc io.Reader, err error) {
		var buf bytes.Buffer
		blob, err := schema.BlobFromReader(br, io.TeeReader(src, &buf))
		newSrc = io.MultiReader(bytes.NewReader(buf.Bytes()), src)
		if err != nil || blob.Type() == "" {
			return elseSto, newSrc, nil
		}
		return thenSto, newSrc, nil
	}
}

func (sto *condStorage) ReceiveBlob(br blob.Ref, src io.Reader) (sb blob.SizedRef, err error) {
	destSto, src, err := sto.storageForReceive(br, src)
	if err != nil {
		return
	}
	return blobserver.Receive(destSto, br, src)
}

func (sto *condStorage) RemoveBlobs(blobs []blob.Ref) error {
	if sto.remove != nil {
		return sto.remove.RemoveBlobs(blobs)
	}
	return errors.New("cond: Remove not configured")
}

func (sto *condStorage) Fetch(b blob.Ref) (file io.ReadCloser, size uint32, err error) {
	if sto.read != nil {
		return sto.read.Fetch(b)
	}
	err = errors.New("cond: Read not configured")
	return
}

func (sto *condStorage) StatBlobs(dest chan<- blob.SizedRef, blobs []blob.Ref) error {
	if sto.read != nil {
		return sto.read.StatBlobs(dest, blobs)
	}
	return errors.New("cond: Read not configured")
}

func (sto *condStorage) EnumerateBlobs(ctx context.Context, dest chan<- blob.SizedRef, after string, limit int) error {
	if sto.read != nil {
		return sto.read.EnumerateBlobs(ctx, dest, after, limit)
	}
	return errors.New("cond: Read not configured")
}

func init() {
	blobserver.RegisterStorageConstructor("cond", blobserver.StorageConstructor(newFromConfig))
}
