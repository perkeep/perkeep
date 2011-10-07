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
	"bytes"
	"http"
	"io"
	"os"

	"appengine"
	"appengine/datastore"
	"appengine/blobstore"

	"camli/blobref"
	"camli/blobserver"
	"camli/jsonconfig"
)

type appengineStorage struct {
	*blobserver.SimpleBlobHubPartitionMap

	ctx appengine.Context
}

type blobEnt struct {
	BlobRefStr string
	Size       int64
	BlobKey    appengine.BlobKey
	// TODO(bradfitz): IsCamliSchemaBlob bool
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

func (sto *appengineStorage) ReceiveBlob(br *blobref.BlobRef, in io.Reader) (sb blobref.SizedBlobRef, err os.Error) {
	if sto.ctx == nil {
		err = errNoContext
		return
	}

	var b bytes.Buffer
	hash := br.Hash()
	written, err := io.Copy(io.MultiWriter(hash, &b), in)
	if err != nil {
		return
	}

	if !br.HashMatches(hash) {
		err = blobserver.ErrCorruptBlob
		return
	}
	mimeType := "application/octet-stream"
	bw, err := blobstore.Create(sto.ctx, mimeType)
	if err != nil {
		return
	}
	written, err = io.Copy(bw, &b)
	if err != nil {
		// TODO(bradfitz): try to clean up; close it, see if we can find the key, delete it.
		return
	}
	err = bw.Close()
	if err != nil {
		// TODO(bradfitz): try to clean up; see if we can find the key, delete it.
		return
	}
	bkey, err := bw.Key()
	if err != nil {
                return
        }

	var ent blobEnt
	ent.BlobRefStr = br.String()
	ent.Size = written
	ent.BlobKey = bkey

	dkey := datastore.NewKey(sto.ctx, "Blob", br.String(), 0, nil)
	_, err = datastore.Put(sto.ctx, dkey, &ent)
	if err != nil {
		blobstore.Delete(sto.ctx, bkey)  // TODO: insert into task queue on error to try later?
		return
	}

	return blobref.SizedBlobRef{br, written}, nil
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
