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

package appengine

import (
	"http"
	"io"
	"os"

	"appengine"

	"camli/blobref"
	"camli/blobserver"
	"camli/jsonconfig"
)

type appengineStorage struct {
	*blobserver.SimpleBlobHubPartitionMap

	ctx appengine.Context
}

var errNoContext = os.NewError("Internal error: no App Engine context is available")

func newFromConfig(ld blobserver.Loader, config jsonconfig.Obj) (storage blobserver.Storage, err os.Error) {
	sto := &appengineStorage{
		SimpleBlobHubPartitionMap: &blobserver.SimpleBlobHubPartitionMap{},
	}
	if err := config.Validate(); err != nil {
		return nil, err
	}
	return sto, nil
}

var _ blobserver.ContextWrapper = (*appengineStorage)(nil)

func (sto *appengineStorage) WrapContext(req *http.Request) blobserver.Storage {
	s2 := new(appengineStorage)
	*s2 = *sto
	s2.ctx = appengine.NewContext(req)
	return s2
}

func (sto *appengineStorage) FetchStreaming(br *blobref.BlobRef) (file io.ReadCloser, size int64, err os.Error) {
	if sto.ctx == nil {
		err = errNoContext
		return
	}
	err = os.NewError("TODO-AppEngine-FetchStreaming")
	return
}

func (sto *appengineStorage) ReceiveBlob(b *blobref.BlobRef, source io.Reader) (sb blobref.SizedBlobRef, err os.Error) {
	if sto.ctx == nil {
		err = errNoContext
		return
	}
	err = os.NewError("TODO-AppEngine-ReceiveBlob")
	return
}

func (sto *appengineStorage) RemoveBlobs(blobs []*blobref.BlobRef) os.Error {
	if sto.ctx == nil {
		return errNoContext
	}
	return os.NewError("TODO-AppEngine-RemoveBlobs")
}

func (sto *appengineStorage) StatBlobs(dest chan<- blobref.SizedBlobRef, blobs []*blobref.BlobRef, waitSeconds int) os.Error {
	if sto.ctx == nil {
		return errNoContext
	}
	return os.NewError("TODO-AppEngine-StatBlobs")
}

func (sto *appengineStorage) EnumerateBlobs(dest chan<- blobref.SizedBlobRef, after string, limit uint, waitSeconds int) os.Error {
	if sto.ctx == nil {
		return errNoContext
	}
	return os.NewError("TODO-AppEngine-EnumerateBlobs")
}
