// +build appengine

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
	"time"

	"appengine"

	"camlistore.org/pkg/blobref"
	"camlistore.org/pkg/blobserver"
	"camlistore.org/pkg/jsonconfig"
	"camlistore.org/pkg/search"
)

type appengineIndex struct {
	*blobserver.NoImplStorage
	namespace string
	ctx       appengine.Context
}

func (x *appengineIndex) WrapContext(req *http.Request) blobserver.Storage {
	x2 := new(appengineIndex)
	*x2 = *x
	x2.ctx = appengine.NewContext(req)
	return x2
}

var _ search.Index = (*appengineIndex)(nil)
var _ blobserver.Storage = (*appengineIndex)(nil)

func indexFromConfig(ld blobserver.Loader, config jsonconfig.Obj) (storage blobserver.Storage, err os.Error) {
	sto := &appengineIndex{}
	ns := config.OptionalString("namespace", "")
	if err := config.Validate(); err != nil {
		return nil, err
	}
	sto.namespace, err = sanitizeNamespace(ns)
	if err != nil {
		return nil, err
	}
	return sto, nil
}

func (x *appengineIndex) ReceiveBlob(br *blobref.BlobRef, in io.Reader) (sb blobref.SizedBlobRef, err os.Error) {
	if x.ctx == nil {
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

	// TODO(bradfitz): implement

	return blobref.SizedBlobRef{br, written}, nil
}

func (x *appengineIndex) GetRecentPermanodes(dest chan *search.Result,
	owner *blobref.BlobRef,
	limit int) os.Error {
	defer close(dest)
	// TODO(bradfitz): this will need to be a context wrapper too, like storage
	return os.NewError("TODO: GetRecentPermanodes")
}

func (x *appengineIndex) SearchPermanodesWithAttr(dest chan<- *blobref.BlobRef,
	request *search.PermanodeByAttrRequest) os.Error {
	return os.NewError("TODO: SearchPermanodesWithAttr")
}

func (x *appengineIndex) GetOwnerClaims(permaNode, owner *blobref.BlobRef) (search.ClaimList, os.Error) {
	return nil, os.NewError("TODO: GetOwnerClaims")
}

func (x *appengineIndex) GetBlobMimeType(blob *blobref.BlobRef) (mime string, size int64, err os.Error) {
	err = os.NewError("TODO: GetBlobMimeType")
	return
}

func (x *appengineIndex) ExistingFileSchemas(bytesRef *blobref.BlobRef) ([]*blobref.BlobRef, os.Error) {
	return nil, os.NewError("TODO: xxx")
}

func (x *appengineIndex) GetFileInfo(fileRef *blobref.BlobRef) (*search.FileInfo, os.Error) {
	return nil, os.NewError("TODO: GetFileInfo")
}

func (x *appengineIndex) PermanodeOfSignerAttrValue(signer *blobref.BlobRef, attr, val string) (*blobref.BlobRef, os.Error) {
	return nil, os.NewError("TODO: PermanodeOfSignerAttrValue")
}

func (x *appengineIndex) PathsOfSignerTarget(signer, target *blobref.BlobRef) ([]*search.Path, os.Error) {
	return nil, os.NewError("TODO: PathsOfSignerTarget")
}

func (x *appengineIndex) PathsLookup(signer, base *blobref.BlobRef, suffix string) ([]*search.Path, os.Error) {
	return nil, os.NewError("TODO: PathsLookup")
}

func (x *appengineIndex) PathLookup(signer, base *blobref.BlobRef, suffix string, at *time.Time) (*search.Path, os.Error) {
	return nil, os.NewError("TODO: PathLookup")
}
