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

// Package cloudstorage registers the "googlecloudstorage" blob storage type, storing blobs
// on Google Cloud Storage (not Google Drive).
// See https://cloud.google.com/products/cloud-storage
package cloudstorage

import (
	"bytes"
	"io"
	"io/ioutil"
	"log"
	"time"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/blobserver"
	"camlistore.org/pkg/googlestorage"
	"camlistore.org/pkg/jsonconfig"
)

type Storage struct {
	hub    *blobserver.SimpleBlobHub
	bucket string // the gs bucket containing blobs
	client *googlestorage.Client
}

var _ blobserver.MaxEnumerateConfig = (*Storage)(nil)

func (gs *Storage) MaxEnumerate() int { return 1000 }

func newFromConfig(_ blobserver.Loader, config jsonconfig.Obj) (blobserver.Storage, error) {
	auth := config.RequiredObject("auth")

	gs := &Storage{
		&blobserver.SimpleBlobHub{},
		config.RequiredString("bucket"),
		googlestorage.NewClient(googlestorage.MakeOauthTransport(
			auth.RequiredString("client_id"),
			auth.RequiredString("client_secret"),
			auth.RequiredString("refresh_token"))),
	}
	if err := config.Validate(); err != nil {
		return nil, err
	}
	if err := auth.Validate(); err != nil {
		return nil, err
	}
	return gs, nil
}

func (gs *Storage) EnumerateBlobs(dest chan<- blob.SizedRef, after string, limit int, wait time.Duration) error {
	defer close(dest)
	objs, err := gs.client.EnumerateObjects(gs.bucket, after, limit)
	if err != nil {
		log.Printf("gstorage EnumerateObjects: %v", err)
		return err
	}
	for _, obj := range objs {
		br, ok := blob.Parse(obj.Key)
		if !ok {
			continue
		}
		dest <- blob.SizedRef{Ref: br, Size: obj.Size}
	}
	return nil
}

func (gs *Storage) ReceiveBlob(br blob.Ref, source io.Reader) (blob.SizedRef, error) {
	buf := &bytes.Buffer{}
	hash := br.Hash()
	size, err := io.Copy(io.MultiWriter(hash, buf), source)
	if err != nil {
		return blob.SizedRef{}, err
	}
	if !br.HashMatches(hash) {
		return blob.SizedRef{}, blobserver.ErrCorruptBlob
	}

	for tries, shouldRetry := 0, true; tries < 2 && shouldRetry; tries++ {
		shouldRetry, err = gs.client.PutObject(
			&googlestorage.Object{Bucket: gs.bucket, Key: br.String()},
			ioutil.NopCloser(bytes.NewReader(buf.Bytes())))
	}
	if err != nil {
		return blob.SizedRef{}, err
	}

	return blob.SizedRef{Ref: br, Size: size}, nil
}

func (gs *Storage) StatBlobs(dest chan<- blob.SizedRef, blobs []blob.Ref, wait time.Duration) error {
	var reterr error

	// TODO: do a batch API call, or at least keep N of these in flight at a time. No need to do them all serially.
	for _, br := range blobs {
		size, _, err := gs.client.StatObject(
			&googlestorage.Object{Bucket: gs.bucket, Key: br.String()})
		if err == nil {
			dest <- blob.SizedRef{Ref: br, Size: size}
		} else {
			reterr = err
		}
	}
	return reterr
}

func (gs *Storage) FetchStreaming(blob blob.Ref) (file io.ReadCloser, size int64, err error) {
	file, size, err = gs.client.GetObject(&googlestorage.Object{Bucket: gs.bucket, Key: blob.String()})
	return

}

func (gs *Storage) RemoveBlobs(blobs []blob.Ref) error {
	var reterr error
	// TODO: do a batch API call, or at least keep N of these in flight at a time. No need to do them all serially.
	for _, br := range blobs {
		err := gs.client.DeleteObject(&googlestorage.Object{Bucket: gs.bucket, Key: br.String()})
		if err != nil {
			reterr = err
		}
	}
	return reterr
}

func (gs *Storage) GetBlobHub() blobserver.BlobHub {
	return gs.hub
}

func init() {
	blobserver.RegisterStorageConstructor("googlecloudstorage", blobserver.StorageConstructor(newFromConfig))
}
