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
	"fmt"
	"http"
	"io"
	"io/ioutil"
	"log"
	"os"

	"appengine"
	"appengine/datastore"
	"appengine/blobstore"

	"camli/blobref"
	"camli/blobserver"
	"camli/jsonconfig"
)

var _ = log.Printf

const blobKind = "Blob"

type appengineStorage struct {
	*blobserver.SimpleBlobHubPartitionMap

	ctx appengine.Context
}

type blobEnt struct {
	Size    []byte // an int64 as "%d" to make it unindexed
	BlobKey []byte // an appengine.BlobKey

	// TODO(bradfitz): IsCamliSchemaBlob bool?
}

func (b *blobEnt) size() (int64, os.Error) {
	var size int64
	n, err := fmt.Fscanf(bytes.NewBuffer(b.Size), "%d", &size)
	if n != 1 || err != nil {
		return 0, fmt.Errorf("invalid Size column in datastore: %q", string(b.Size))
	}
	return size, nil
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

	key := datastore.NewKey(sto.ctx, blobKind, br.String(), 0, nil)
	row := new(blobEnt)
	err = datastore.Get(sto.ctx, key, row)
	if err == datastore.ErrNoSuchEntity {
		err = os.ENOENT
		return
	}
	if err != nil {
		return
	}
	size, err = row.size()
	if err != nil {
		return
	}
	reader := blobstore.NewReader(sto.ctx, appengine.BlobKey(string(row.BlobKey)))
	return ioutil.NopCloser(reader), size, nil
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
	ent.Size = []byte(fmt.Sprintf("%d", written))
	ent.BlobKey = []byte(string(bkey))

	dkey := datastore.NewKey(sto.ctx, blobKind, br.String(), 0, nil)
	_, err = datastore.Put(sto.ctx, dkey, &ent)
	if err != nil {
		blobstore.Delete(sto.ctx, bkey) // TODO: insert into task queue on error to try later?
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
	var (
		keys = make([]*datastore.Key, 0, len(blobs))
		out  = make([]interface{}, 0, len(blobs))
		errs = make([]os.Error, len(blobs))
	)
	for _, br := range blobs {
		keys = append(keys, datastore.NewKey(sto.ctx, blobKind, br.String(), 0, nil))
		out = append(out, new(blobEnt))
	}
	err := datastore.GetMulti(sto.ctx, keys, out)
	if merr, ok := err.(datastore.ErrMulti); ok {
		errs = []os.Error(merr)
		err = nil
	}
	if err != nil {
		return err
	}
	for i, br := range blobs {
		thisErr := errs[i]
		if thisErr == datastore.ErrNoSuchEntity {
			continue
		}
		if thisErr != nil {
			err = errs[i] // just return last one found?
			continue
		}
		ent := out[i].(*blobEnt)
		size, err := ent.size()
		if err == nil {
			dest <- blobref.SizedBlobRef{br, size}
		} else {
			sto.ctx.Warningf("skipping corrupt blob %s with Size %q during Stat", br, string(ent.Size))
		}
	}
	return err
}

func (sto *appengineStorage) EnumerateBlobs(dest chan<- blobref.SizedBlobRef, after string, limit uint, waitSeconds int) os.Error {
	defer close(dest)
	if sto.ctx == nil {
		return errNoContext
	}
	q := datastore.NewQuery(blobKind).Limit(int(limit))
	if after != "" {
		akey := datastore.NewKey(sto.ctx, blobKind, after, 0, nil)
		q = q.Filter("__key__>", akey)
	}
	it := q.Run(sto.ctx)
	var ent blobEnt
	for {
		key, err := it.Next(&ent)
		if err == datastore.Done {
			break
		}
		if err != nil {
			return err
		}
		size, err := ent.size()
		if err != nil {
			return err
		}
		dest <- blobref.SizedBlobRef{blobref.Parse(key.StringID()), size}
	}
	return nil
}
