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

	"camlistore.org/pkg/blobref"
	"camlistore.org/pkg/blobserver"
	"camlistore.org/pkg/jsonconfig"
	"camlistore.org/pkg/schema"
)

const buffered = 8

type storageFunc func(src io.Reader) (dest blobserver.Storage, overRead []byte, err error)

type condStorage struct {
	*blobserver.SimpleBlobHubPartitionMap

	storageForReceive storageFunc
	read              blobserver.Storage
	remove            blobserver.Storage

	ctx *http.Request // optional per-request context
}

var _ blobserver.ContextWrapper = (*condStorage)(nil)

func (sto *condStorage) GetBlobHub() blobserver.BlobHub {
	return sto.SimpleBlobHubPartitionMap.GetBlobHub()
}

func (sto *condStorage) WrapContext(req *http.Request) blobserver.Storage {
	s2 := new(condStorage)
	*s2 = *sto
	s2.ctx = req
	return s2
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
	sto := &condStorage{
		SimpleBlobHubPartitionMap: &blobserver.SimpleBlobHubPartitionMap{},
	}

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
		f := func(io.Reader) (blobserver.Storage, []byte, error) {
			return sto, nil, nil
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

// dummyRef is just a dummy reference to give to BlobFromReader.
var dummyRef = blobref.MustParse("sha1-829c3804401b0727f70f73d4415e162400cbe57b")

func isSchemaPicker(thenSto, elseSto blobserver.Storage) storageFunc {
	return func(src io.Reader) (dest blobserver.Storage, overRead []byte, err error) {
		var buf bytes.Buffer
		tee := io.TeeReader(src, &buf)
		blob, err := schema.BlobFromReader(dummyRef, tee)
		if err != nil || blob.Type() == "" {
			return elseSto, buf.Bytes(), nil
		}
		return thenSto, buf.Bytes(), nil
	}
}

func (sto *condStorage) ReceiveBlob(b *blobref.BlobRef, source io.Reader) (sb blobref.SizedBlobRef, err error) {
	destSto, overRead, err := sto.storageForReceive(source)
	if err != nil {
		return
	}
	if len(overRead) > 0 {
		source = io.MultiReader(bytes.NewBuffer(overRead), source)
	}
	destSto = blobserver.MaybeWrapContext(destSto, sto.ctx)
	return destSto.ReceiveBlob(b, source)
}

func (sto *condStorage) RemoveBlobs(blobs []*blobref.BlobRef) error {
	if sto.remove != nil {
		rsto := blobserver.MaybeWrapContext(sto.remove, sto.ctx)
		return rsto.RemoveBlobs(blobs)
	}
	return errors.New("cond: Remove not configured")
}

func (sto *condStorage) IsFetcherASeeker() bool {
	_, ok := sto.read.(blobref.SeekFetcher)
	return ok
}

func (sto *condStorage) FetchStreaming(b *blobref.BlobRef) (file io.ReadCloser, size int64, err error) {
	if sto.read != nil {
		rsto := blobserver.MaybeWrapContext(sto.read, sto.ctx)
		return rsto.FetchStreaming(b)
	}
	err = errors.New("cond: Read not configured")
	return
}

func (sto *condStorage) StatBlobs(dest chan<- blobref.SizedBlobRef, blobs []*blobref.BlobRef, wait time.Duration) error {
	if sto.read != nil {
		rsto := blobserver.MaybeWrapContext(sto.read, sto.ctx)
		return rsto.StatBlobs(dest, blobs, wait)
	}
	return errors.New("cond: Read not configured")
}

func (sto *condStorage) EnumerateBlobs(dest chan<- blobref.SizedBlobRef, after string, limit int, wait time.Duration) error {
	if sto.read != nil {
		rsto := blobserver.MaybeWrapContext(sto.read, sto.ctx)
		return rsto.EnumerateBlobs(dest, after, limit, wait)
	}
	return errors.New("cond: Read not configured")
}

func init() {
	blobserver.RegisterStorageConstructor("cond", blobserver.StorageConstructor(newFromConfig))
}
