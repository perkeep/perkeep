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

package cond

import (
	"bytes"
	"fmt"
	"json"
	"io"
	"log"
	"os"

	"camli/blobref"
	"camli/blobserver"
	"camli/jsonconfig"
	"camli/schema"
)

var _ = log.Printf

const buffered = 8

type storageFunc func(src io.Reader) (dest blobserver.Storage, overRead []byte, err os.Error)

type condStorage struct {
	*blobserver.SimpleBlobHubPartitionMap

	storageForReceive storageFunc
	read              blobserver.Storage
	remove            blobserver.Storage
}

func (sto *condStorage) GetBlobHub() blobserver.BlobHub {
	return sto.SimpleBlobHubPartitionMap.GetBlobHub()
}

func newFromConfig(ld blobserver.Loader, conf jsonconfig.Obj) (storage blobserver.Storage, err os.Error) {
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

func buildStorageForReceive(ld blobserver.Loader, confOrString interface{}) (storageFunc, os.Error) {
	// Static configuration from a string
	if s, ok := confOrString.(string); ok {
		sto, err := ld.GetStorage(s)
		if err != nil {
			return nil, err
		}
		f := func(io.Reader) (blobserver.Storage, []byte, os.Error) {
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

func isSchemaPicker(thenSto, elseSto blobserver.Storage) storageFunc {
	return func(src io.Reader) (dest blobserver.Storage, overRead []byte, err os.Error) {
		// TODO: make decision earlier, by parsing JSON as it comes in,
		// not after we have up to 1 MB.
		var buf bytes.Buffer
		_, err = io.Copyn(&buf, src, 1<<20)
		if err != nil && err != os.EOF {
			return
		}
		ss := new(schema.Superset)
		if err = json.NewDecoder(bytes.NewBuffer(buf.Bytes())).Decode(ss); err != nil {
			log.Printf("cond: json parse failure => not schema => else")
			return elseSto, buf.Bytes(), nil
		}
		if ss.Type == "" {
			log.Printf("cond: json => but not schema => else")
			return elseSto, buf.Bytes(), nil
		}
		log.Printf("cond: json => schema => then")
		return thenSto, buf.Bytes(), nil
	}
}

func (sto *condStorage) ReceiveBlob(b *blobref.BlobRef, source io.Reader) (sb blobref.SizedBlobRef, err os.Error) {
	destSto, overRead, err := sto.storageForReceive(source)
	if err != nil {
		return
	}
	if len(overRead) > 0 {
		source = io.MultiReader(bytes.NewBuffer(overRead), source)
	}
	return destSto.ReceiveBlob(b, source)
}

func (sto *condStorage) RemoveBlobs(blobs []*blobref.BlobRef) os.Error {
	if sto.remove != nil {
		return sto.remove.RemoveBlobs(blobs)
	}
	return os.NewError("cond: Remove not configured")
}

func (sto *condStorage) IsFetcherASeeker() bool {
	_, ok := sto.read.(blobref.SeekFetcher)
	return ok
}

func (sto *condStorage) FetchStreaming(b *blobref.BlobRef) (file io.ReadCloser, size int64, err os.Error) {
	if sto.read != nil {
		return sto.read.FetchStreaming(b)
	}
	err = os.NewError("cond: Read not configured")
	return
}

func (sto *condStorage) StatBlobs(dest chan<- blobref.SizedBlobRef, blobs []*blobref.BlobRef, waitSeconds int) os.Error {
	if sto.read != nil {
		return sto.read.StatBlobs(dest, blobs, waitSeconds)
	}
	return os.NewError("cond: Read not configured")
}

func (sto *condStorage) EnumerateBlobs(dest chan<- blobref.SizedBlobRef, after string, limit uint, waitSeconds int) os.Error {
	if sto.read != nil {
		return sto.read.EnumerateBlobs(dest, after, limit, waitSeconds)
	}
	return os.NewError("cond: Read not configured")
}

func init() {
	blobserver.RegisterStorageConstructor("cond", blobserver.StorageConstructor(newFromConfig))
}
