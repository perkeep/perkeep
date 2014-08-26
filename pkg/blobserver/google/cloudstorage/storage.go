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
	"crypto/sha1"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"time"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/blobserver"
	"camlistore.org/pkg/constants"
	"camlistore.org/pkg/context"
	"camlistore.org/pkg/googlestorage"
	"camlistore.org/pkg/jsonconfig"
	"camlistore.org/pkg/syncutil"
)

type Storage struct {
	bucket string // the gs bucket containing blobs
	client *googlestorage.Client

	// For blobserver.Generationer:
	genTime   time.Time
	genRandom string
}

var (
	_ blobserver.MaxEnumerateConfig = (*Storage)(nil)
	_ blobserver.Generationer       = (*Storage)(nil)
)

func (gs *Storage) MaxEnumerate() int { return 1000 }

func (gs *Storage) StorageGeneration() (time.Time, string, error) {
	return gs.genTime, gs.genRandom, nil
}
func (gs *Storage) ResetStorageGeneration() error { return errors.New("not supported") }

func newFromConfig(_ blobserver.Loader, config jsonconfig.Obj) (blobserver.Storage, error) {
	var (
		auth   = config.RequiredObject("auth")
		bucket = config.RequiredString("bucket")

		clientID     = auth.RequiredString("client_id") // or "auto" for service accounts
		clientSecret = auth.OptionalString("client_secret", "")
		refreshToken = auth.OptionalString("refresh_token", "")
	)

	if err := config.Validate(); err != nil {
		return nil, err
	}
	if err := auth.Validate(); err != nil {
		return nil, err
	}

	gs := &Storage{bucket: bucket}
	if clientID == "auto" {
		var err error
		gs.client, err = googlestorage.NewServiceClient()
		if err != nil {
			return nil, err
		}
	} else {
		if clientSecret == "" {
			return nil, errors.New("missing required parameter 'client_secret'")
		}
		if refreshToken == "" {
			return nil, errors.New("missing required parameter 'refresh_token'")
		}
		gs.client = googlestorage.NewClient(googlestorage.MakeOauthTransport(
			clientID, clientSecret, refreshToken))
	}

	bi, err := gs.client.BucketInfo(bucket)
	if err != nil {
		return nil, fmt.Errorf("error statting bucket %q: %v", bucket, err)
	}
	hash := sha1.New()
	fmt.Fprintf(hash, "%v%v", bi.TimeCreated, bi.Metageneration)
	gs.genRandom = fmt.Sprintf("%x", hash.Sum(nil))
	gs.genTime, _ = time.Parse(time.RFC3339, bi.TimeCreated)

	return gs, nil
}

func (gs *Storage) EnumerateBlobs(ctx *context.Context, dest chan<- blob.SizedRef, after string, limit int) error {
	defer close(dest)
	objs, err := gs.client.EnumerateObjects(gs.bucket, after, limit)
	if err != nil {
		log.Printf("gstorage EnumerateObjects: %v", err)
		return err
	}
	for _, obj := range objs {
		br, ok := blob.Parse(obj.Key)
		if !ok {
			return fmt.Errorf("Non-Camlistore object named %q found in bucket", obj.Key)
		}
		select {
		case dest <- blob.SizedRef{Ref: br, Size: uint32(obj.Size)}:
		case <-ctx.Done():
			return context.ErrCanceled
		}
	}
	return nil
}

func (gs *Storage) ReceiveBlob(br blob.Ref, source io.Reader) (blob.SizedRef, error) {
	buf := &bytes.Buffer{}
	size, err := io.Copy(buf, source)
	if err != nil {
		return blob.SizedRef{}, err
	}

	for tries, shouldRetry := 0, true; tries < 2 && shouldRetry; tries++ {
		shouldRetry, err = gs.client.PutObject(
			&googlestorage.Object{Bucket: gs.bucket, Key: br.String()},
			ioutil.NopCloser(bytes.NewReader(buf.Bytes())))
	}
	if err != nil {
		return blob.SizedRef{}, err
	}

	return blob.SizedRef{Ref: br, Size: uint32(size)}, nil
}

func (gs *Storage) StatBlobs(dest chan<- blob.SizedRef, blobs []blob.Ref) error {
	var grp syncutil.Group
	gate := syncutil.NewGate(20) // arbitrary cap
	for i := range blobs {
		br := blobs[i]
		gate.Start()
		grp.Go(func() error {
			defer gate.Done()
			size, exists, err := gs.client.StatObject(
				&googlestorage.Object{Bucket: gs.bucket, Key: br.String()})
			if err != nil {
				return err
			}
			if !exists {
				return nil
			}
			if size > constants.MaxBlobSize {
				return fmt.Errorf("blob %s stat size too large (%d)", br, size)
			}
			dest <- blob.SizedRef{Ref: br, Size: uint32(size)}
			return nil
		})
	}
	return grp.Err()
}

func (gs *Storage) Fetch(blob blob.Ref) (file io.ReadCloser, size uint32, err error) {
	file, sz, err := gs.client.GetObject(&googlestorage.Object{Bucket: gs.bucket, Key: blob.String()})
	if err != nil && sz > constants.MaxBlobSize {
		err = errors.New("object too big")
	}
	return file, uint32(sz), err

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

func init() {
	blobserver.RegisterStorageConstructor("googlecloudstorage", blobserver.StorageConstructor(newFromConfig))
}
