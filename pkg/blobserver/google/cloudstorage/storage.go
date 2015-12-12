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
	"path"
	"strings"
	"time"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/blobserver"
	"camlistore.org/pkg/blobserver/memory"
	"camlistore.org/pkg/constants"
	"camlistore.org/pkg/constants/google"
	"camlistore.org/pkg/googlestorage"
	"go4.org/jsonconfig"
	"golang.org/x/net/context"

	"go4.org/oauthutil"
	"go4.org/syncutil"
	"golang.org/x/oauth2"
)

type Storage struct {
	bucket string // the gs bucket containing blobs
	// optional "directory" where the blobs are stored, instead of at the root of the bucket.
	// gcs is actually flat, which in effect just means that all the objects should have this
	// dirPrefix as a prefix of their key.
	// If non empty, it should be a slash separated path with a trailing slash and no starting
	// slash.
	dirPrefix string
	client    *googlestorage.Client
	cache     *memory.Storage // or nil for no cache

	// For blobserver.Generationer:
	genTime   time.Time
	genRandom string
}

var (
	_ blob.SubFetcher               = (*Storage)(nil)
	_ blobserver.Generationer       = (*Storage)(nil)
	_ blobserver.MaxEnumerateConfig = (*Storage)(nil)
)

func (gs *Storage) MaxEnumerate() int { return 1000 }

func (gs *Storage) StorageGeneration() (time.Time, string, error) {
	return gs.genTime, gs.genRandom, nil
}
func (gs *Storage) ResetStorageGeneration() error { return errors.New("not supported") }

func newFromConfig(_ blobserver.Loader, config jsonconfig.Obj) (blobserver.Storage, error) {
	var (
		auth      = config.RequiredObject("auth")
		bucket    = config.RequiredString("bucket")
		cacheSize = config.OptionalInt64("cacheSize", 32<<20)

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

	var dirPrefix string
	if parts := strings.SplitN(bucket, "/", 2); len(parts) > 1 {
		dirPrefix = parts[1]
		bucket = parts[0]
	}
	if dirPrefix != "" && !strings.HasSuffix(dirPrefix, "/") {
		dirPrefix += "/"
	}
	gs := &Storage{
		bucket:    bucket,
		dirPrefix: dirPrefix,
	}
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
		oAuthClient := oauth2.NewClient(oauth2.NoContext, oauthutil.NewRefreshTokenSource(&oauth2.Config{
			Scopes:       []string{googlestorage.Scope},
			Endpoint:     google.Endpoint,
			ClientID:     clientID,
			ClientSecret: clientSecret,
			RedirectURL:  oauthutil.TitleBarRedirectURL,
		}, refreshToken))
		gs.client = googlestorage.NewClient(oAuthClient)
	}

	if cacheSize != 0 {
		gs.cache = memory.NewCache(cacheSize)
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

func (s *Storage) EnumerateBlobs(ctx context.Context, dest chan<- blob.SizedRef, after string, limit int) error {
	defer close(dest)
	objs, err := s.client.EnumerateObjects(s.bucket, s.dirPrefix+after, limit)
	if err != nil {
		log.Printf("gstorage EnumerateObjects: %v", err)
		return err
	}
	for _, obj := range objs {
		dir, file := path.Split(obj.Key)
		if dir != s.dirPrefix {
			continue
		}
		br, ok := blob.Parse(file)
		if !ok {
			return fmt.Errorf("Non-Camlistore object named %q found in bucket", file)
		}
		select {
		case dest <- blob.SizedRef{Ref: br, Size: uint32(obj.Size)}:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

func (s *Storage) ReceiveBlob(br blob.Ref, source io.Reader) (blob.SizedRef, error) {
	var buf bytes.Buffer
	size, err := io.Copy(&buf, source)
	if err != nil {
		return blob.SizedRef{}, err
	}

	err = s.client.PutObject(
		&googlestorage.Object{Bucket: s.bucket, Key: s.dirPrefix + br.String()},
		ioutil.NopCloser(bytes.NewReader(buf.Bytes())))
	if err != nil {
		return blob.SizedRef{}, err
	}
	if s.cache != nil {
		// NoHash because it's already verified if we read it
		// without errors on the io.Copy above.
		blobserver.ReceiveNoHash(s.cache, br, bytes.NewReader(buf.Bytes()))
	}
	return blob.SizedRef{Ref: br, Size: uint32(size)}, nil
}

func (s *Storage) StatBlobs(dest chan<- blob.SizedRef, blobs []blob.Ref) error {
	// TODO: use cache
	var grp syncutil.Group
	gate := syncutil.NewGate(20) // arbitrary cap
	for i := range blobs {
		br := blobs[i]
		gate.Start()
		grp.Go(func() error {
			defer gate.Done()
			size, exists, err := s.client.StatObject(
				&googlestorage.Object{Bucket: s.bucket, Key: s.dirPrefix + br.String()})
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

func (s *Storage) Fetch(br blob.Ref) (rc io.ReadCloser, size uint32, err error) {
	if s.cache != nil {
		if rc, size, err = s.cache.Fetch(br); err == nil {
			return
		}
	}
	rc, sz, err := s.client.GetObject(&googlestorage.Object{Bucket: s.bucket, Key: s.dirPrefix + br.String()})
	if err != nil && sz > constants.MaxBlobSize {
		err = errors.New("object too big")
	}
	return rc, uint32(sz), err
}

func (s *Storage) SubFetch(br blob.Ref, offset, length int64) (rc io.ReadCloser, err error) {
	return s.client.GetPartialObject(googlestorage.Object{Bucket: s.bucket, Key: s.dirPrefix + br.String()}, offset, length)
}

func (s *Storage) RemoveBlobs(blobs []blob.Ref) error {
	if s.cache != nil {
		s.cache.RemoveBlobs(blobs)
	}
	gate := syncutil.NewGate(50) // arbitrary
	var grp syncutil.Group
	for i := range blobs {
		gate.Start()
		br := blobs[i]
		grp.Go(func() error {
			defer gate.Done()
			return s.client.DeleteObject(&googlestorage.Object{Bucket: s.bucket, Key: s.dirPrefix + br.String()})
		})
	}
	return grp.Err()
}

func init() {
	blobserver.RegisterStorageConstructor("googlecloudstorage", blobserver.StorageConstructor(newFromConfig))
}
