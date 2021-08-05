/*
Copyright 2011 The Perkeep Authors

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
	"context"
	"crypto/sha1"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path"
	"strings"
	"time"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver"
	"perkeep.org/pkg/blobserver/memory"
	"perkeep.org/pkg/constants"

	"cloud.google.com/go/storage"
	"go4.org/cloud/google/gcsutil"
	"go4.org/ctxutil"
	"go4.org/jsonconfig"
	"go4.org/oauthutil"
	"go4.org/syncutil"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/option"
)

type Storage struct {
	bucket string // the gs bucket containing blobs
	// optional "directory" where the blobs are stored, instead of at the root of the bucket.
	// gcs is actually flat, which in effect just means that all the objects should have this
	// dirPrefix as a prefix of their key.
	// If non empty, it should be a slash separated path with a trailing slash and no starting
	// slash.
	dirPrefix string
	client    *storage.Client
	cache     *memory.Storage // or nil for no cache

	// an OAuth-authenticated HTTP client, for methods that can't yet use a
	// *storage.Client
	baseHTTPClient *http.Client

	// For blobserver.Generationer:
	genTime   time.Time
	genRandom string
}

var (
	_ blob.SubFetcher               = (*Storage)(nil)
	_ blobserver.Generationer       = (*Storage)(nil)
	_ blobserver.MaxEnumerateConfig = (*Storage)(nil)
)

func (s *Storage) MaxEnumerate() int { return 1000 }

func (s *Storage) StorageGeneration() (time.Time, string, error) {
	return s.genTime, s.genRandom, nil
}
func (s *Storage) ResetStorageGeneration() error { return errors.New("not supported") }

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

	var (
		ctx = context.Background()
		ts  oauth2.TokenSource
		cl  *storage.Client
		err error
	)
	if clientID == "auto" {
		ts, err = google.DefaultTokenSource(ctx, storage.ScopeReadWrite)
		if err != nil {
			return nil, err
		}
		cl, err = storage.NewClient(ctx)
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
		ts = oauthutil.NewRefreshTokenSource(&oauth2.Config{
			Scopes:       []string{storage.ScopeReadWrite},
			Endpoint:     google.Endpoint,
			ClientID:     clientID,
			ClientSecret: clientSecret,
			RedirectURL:  oauthutil.TitleBarRedirectURL,
		}, refreshToken)
		cl, err = storage.NewClient(ctx, option.WithTokenSource(ts))
		if err != nil {
			return nil, err
		}
	}

	gs.baseHTTPClient = oauth2.NewClient(ctx, ts)
	gs.client = cl

	if cacheSize != 0 {
		gs.cache = memory.NewCache(cacheSize)
	}

	ba, err := gs.client.Bucket(gs.bucket).Attrs(ctx)
	if err != nil {
		return nil, fmt.Errorf("error statting bucket %q: %v", gs.bucket, err)
	}
	hash := sha1.New()
	fmt.Fprintf(hash, "%v%v", ba.Created, ba.MetaGeneration)
	gs.genRandom = fmt.Sprintf("%x", hash.Sum(nil))
	gs.genTime = ba.Created

	return gs, nil
}

// TODO(mpl, bradfitz): use a *storage.Client in EnumerateBlobs, instead of hitting the
// XML API, once we have an efficient replacement for the "marker" from the XML API. See
// https://github.com/GoogleCloudPlatform/gcloud-golang/issues/197

func (s *Storage) EnumerateBlobs(ctx context.Context, dest chan<- blob.SizedRef, after string, limit int) error {
	defer close(dest)
	ectx := context.WithValue(ctx, ctxutil.HTTPClient, s.baseHTTPClient)
	objs, err := gcsutil.EnumerateObjects(ectx, s.bucket, s.dirPrefix+after, limit)
	if err != nil {
		log.Printf("gstorage EnumerateObjects: %v", err)
		return err
	}
	for _, obj := range objs {
		dir, file := path.Split(obj.Name)
		if dir != s.dirPrefix {
			continue
		}
		br, ok := blob.Parse(file)
		if !ok {
			return fmt.Errorf("Non-Perkeep object named %q found in bucket", file)
		}
		select {
		case dest <- blob.SizedRef{Ref: br, Size: uint32(obj.Size)}:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

func (s *Storage) ReceiveBlob(ctx context.Context, br blob.Ref, source io.Reader) (blob.SizedRef, error) {
	var buf bytes.Buffer
	size, err := io.Copy(&buf, source)
	if err != nil {
		return blob.SizedRef{}, err
	}

	w := s.client.Bucket(s.bucket).Object(s.dirPrefix + br.String()).NewWriter(ctx)
	if _, err := io.Copy(w, bytes.NewReader(buf.Bytes())); err != nil {
		return blob.SizedRef{}, err
	}
	if err := w.Close(); err != nil {
		return blob.SizedRef{}, err
	}

	if s.cache != nil {
		// NoHash because it's already verified if we read it
		// without errors on the io.Copy above.
		blobserver.ReceiveNoHash(ctx, s.cache, br, bytes.NewReader(buf.Bytes()))
	}
	return blob.SizedRef{Ref: br, Size: uint32(size)}, nil
}

var statGate = syncutil.NewGate(20) // arbitrary cap

func (s *Storage) StatBlobs(ctx context.Context, blobs []blob.Ref, fn func(blob.SizedRef) error) error {
	// TODO: use cache
	return blobserver.StatBlobsParallelHelper(ctx, blobs, fn, statGate, func(br blob.Ref) (sb blob.SizedRef, err error) {
		attrs, err := s.client.Bucket(s.bucket).Object(s.dirPrefix + br.String()).Attrs(ctx)
		if err == storage.ErrObjectNotExist {
			return sb, nil
		}
		if err != nil {
			return sb, err
		}
		size := attrs.Size
		if size > constants.MaxBlobSize {
			return sb, fmt.Errorf("blob %s stat size too large (%d)", br, size)
		}
		return blob.SizedRef{Ref: br, Size: uint32(size)}, nil
	})
}

func (s *Storage) Fetch(ctx context.Context, br blob.Ref) (rc io.ReadCloser, size uint32, err error) {
	if s.cache != nil {
		if rc, size, err = s.cache.Fetch(ctx, br); err == nil {
			return
		}
	}
	r, err := s.client.Bucket(s.bucket).Object(s.dirPrefix + br.String()).NewReader(ctx)
	if err == storage.ErrObjectNotExist {
		return nil, 0, os.ErrNotExist
	}
	if err != nil {
		return nil, 0, err
	}
	if r.Attrs.Size >= 1<<32 {
		r.Close()
		return nil, 0, errors.New("object larger than a uint32")
	}
	size = uint32(r.Attrs.Size)
	if size > constants.MaxBlobSize {
		r.Close()
		return nil, size, errors.New("object too big")
	}
	return r, size, nil
}

func (s *Storage) SubFetch(ctx context.Context, br blob.Ref, offset, length int64) (rc io.ReadCloser, err error) {
	if offset < 0 || length < 0 {
		return nil, blob.ErrNegativeSubFetch
	}
	ctx = context.WithValue(ctx, ctxutil.HTTPClient, s.baseHTTPClient)
	rc, err = gcsutil.GetPartialObject(ctx, gcsutil.Object{Bucket: s.bucket, Key: s.dirPrefix + br.String()}, offset, length)
	if err == gcsutil.ErrInvalidRange {
		return nil, blob.ErrOutOfRangeOffsetSubFetch
	}
	if err == storage.ErrObjectNotExist {
		return nil, os.ErrNotExist
	}
	return rc, err
}

func (s *Storage) RemoveBlobs(ctx context.Context, blobs []blob.Ref) error {
	if s.cache != nil {
		s.cache.RemoveBlobs(ctx, blobs)
	}
	gate := syncutil.NewGate(50) // arbitrary
	var grp syncutil.Group
	for i := range blobs {
		gate.Start()
		br := blobs[i]
		grp.Go(func() error {
			defer gate.Done()
			err := s.client.Bucket(s.bucket).Object(s.dirPrefix + br.String()).Delete(ctx)
			if err == storage.ErrObjectNotExist {
				return nil
			}
			return err
		})
	}
	return grp.Err()
}

func init() {
	blobserver.RegisterStorageConstructor("googlecloudstorage", blobserver.StorageConstructor(newFromConfig))
}
