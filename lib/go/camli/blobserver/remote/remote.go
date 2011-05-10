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

package remote

import (
	"io"
	"os"

	"camli/blobref"
	"camli/client"
	"camli/blobserver"
	"camli/jsonconfig"
)

// remoteStorage is a blobserver.Storage proxy for a remote camlistore
// blobserver.
type remoteStorage struct {
	*blobserver.SimpleBlobHubPartitionMap // but not really
	client                                *client.Client
}

func newFromConfig(config jsonconfig.Obj) (storage blobserver.Storage, err os.Error) {
	url := config.RequiredString("url")
	password := config.RequiredString("password")
	skipStartupCheck := config.OptionalBool("skipStartupCheck", false)
	if err := config.Validate(); err != nil {
		return nil, err
	}
	sto := &remoteStorage{
		client: client.New(url, password),
	}
	if !skipStartupCheck {
		// TODO: do a server stat or something to check password
	}
	return sto, nil
}

func (sto *remoteStorage) Remove(blobs []*blobref.BlobRef) os.Error {
	return sto.client.RemoveBlobs(blobs)
}

func (sto *remoteStorage) Stat(dest chan<- *blobref.SizedBlobRef, blobs []*blobref.BlobRef, waitSeconds int) os.Error {
	return sto.client.Stat(dest, blobs, waitSeconds)
}

func (sto *remoteStorage) ReceiveBlob(blob *blobref.BlobRef, source io.Reader) (*blobref.SizedBlobRef, os.Error) {
	return nil, os.NewError("TODO: implement")
}

func (sto *remoteStorage) FetchStreaming(b *blobref.BlobRef) (file io.ReadCloser, size int64, err os.Error) {
	return nil, 0, os.NewError("TODO: implement")
}

func (sto *remoteStorage) MaxEnumerate() uint { return 1000 }

func (sto *remoteStorage) EnumerateBlobs(dest chan<- *blobref.SizedBlobRef, after string, limit uint, waitSeconds int) os.Error {
	defer close(dest)
	return os.NewError("TODO: implement")
}

func init() {
	blobserver.RegisterStorageConstructor("remote", blobserver.StorageConstructor(newFromConfig))
}
