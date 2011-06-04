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
	"io"
	"log"
	"os"

	"camli/blobref"
	"camli/blobserver"
	"camli/jsonconfig"
)

var _ = log.Printf

const buffered = 8

type condStorage struct {
	*blobserver.SimpleBlobHubPartitionMap

}

func (sto *condStorage) GetBlobHub() blobserver.BlobHub {
	return sto.SimpleBlobHubPartitionMap.GetBlobHub()
}

func newFromConfig(ld blobserver.Loader, conf jsonconfig.Obj) (storage blobserver.Storage, err os.Error) {
	sto := &condStorage{
		SimpleBlobHubPartitionMap: &blobserver.SimpleBlobHubPartitionMap{},
	}
	_ = conf.RequiredString("if")
	_ = conf.RequiredString("then")
	_ = conf.RequiredString("else")
	if err := conf.Validate(); err != nil {
		return nil, err
	}
	return sto, nil
}

func (sto *condStorage) FetchStreaming(b *blobref.BlobRef) (file io.ReadCloser, size int64, err os.Error) {
	panic("NOIMPL")
	return nil, 0, nil
}

func (sto *condStorage) Stat(dest chan<- blobref.SizedBlobRef, blobs []*blobref.BlobRef, waitSeconds int) os.Error {
	panic("NOIMPL")
	return nil
}

func (sto *condStorage) ReceiveBlob(b *blobref.BlobRef, source io.Reader) (xxgo blobref.SizedBlobRef, err os.Error) {
	var sb blobref.SizedBlobRef
	panic("NOIMPL")
	return sb, nil
}

func (sto *condStorage) Remove(blobs []*blobref.BlobRef) os.Error {
	panic("NOIMPL")
	return nil
}


func (sto *condStorage) EnumerateBlobs(dest chan<- blobref.SizedBlobRef, after string, limit uint, waitSeconds int) os.Error {
	panic("NOIMPL")
	return nil
}

func init() {
	blobserver.RegisterStorageConstructor("cond", blobserver.StorageConstructor(newFromConfig))
}
