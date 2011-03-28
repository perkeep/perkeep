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

package blobserver

import (
	"camli/blobref"
	"io"
	"os"
)

type NoImplStorage struct {
}

func (nis *NoImplStorage) Fetch(*blobref.BlobRef) (file blobref.ReadSeekCloser, size int64, err os.Error) {
	return nil, 0, os.ENOENT
}

func (nis *NoImplStorage) ReceiveBlob(blob *blobref.BlobRef, source io.Reader, mirrorPartions []Partition) (*blobref.SizedBlobRef, os.Error) {
	return nil, os.NewError("ReceiveBlob not implemented")
}

func (nis *NoImplStorage) Stat(dest chan *blobref.SizedBlobRef,
		partition Partition,
		blobs []*blobref.BlobRef,
		waitSeconds int) os.Error {
	return os.NewError("Stat not implemented")
}

func (nis *NoImplStorage) EnumerateBlobs(dest chan *blobref.SizedBlobRef,
		partition Partition,
		after string,
		limit uint,
		waitSeconds int) os.Error {
	return os.NewError("EnumerateBlobs not implemented")
}

func (nis *NoImplStorage) Remove(partition Partition, blobs []*blobref.BlobRef) os.Error {
	return os.NewError("Remove not implemented")
}
