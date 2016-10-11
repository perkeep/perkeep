/*
Copyright 2016 The Camlistore Authors

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

package b2

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/blobserver"
	"camlistore.org/pkg/blobserver/memory"
	"camlistore.org/pkg/constants"

	"github.com/FiloSottile/b2"
	"go4.org/jsonconfig"
	"go4.org/syncutil"
	"golang.org/x/net/context"
)

type Storage struct {
	cl *b2.Client
	b  *b2.BucketInfo
	// optional "directory" where the blobs are stored, instead of at the root of the bucket.
	// b2 is actually flat, which in effect just means that all the objects should have this
	// dirPrefix as a prefix of their key.
	// If non empty, it should be a slash separated path with a trailing slash and no starting
	// slash.
	dirPrefix string
	cache     *memory.Storage // or nil for no cache
}

func newFromConfig(_ blobserver.Loader, config jsonconfig.Obj) (blobserver.Storage, error) {
	var (
		auth      = config.RequiredObject("auth")
		bucket    = config.RequiredString("bucket")
		cacheSize = config.OptionalInt64("cacheSize", 32<<20)

		accountID = auth.RequiredString("account_id")
		appKey    = auth.RequiredString("application_key")
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

	cl, err := b2.NewClient(accountID, appKey, nil)
	if err != nil {
		return nil, err
	}
	b, err := cl.BucketByName(bucket, true)
	if err != nil {
		return nil, err
	}

	s := &Storage{
		cl: cl, b: b,
		dirPrefix: dirPrefix,
	}

	if cacheSize != 0 {
		s.cache = memory.NewCache(cacheSize)
	}

	return s, nil
}

func (s *Storage) EnumerateBlobs(ctx context.Context, dest chan<- blob.SizedRef, after string, limit int) error {
	defer close(dest)
	l := s.b.ListFiles(s.dirPrefix + after)
	l.SetPageCount(limit)
	for i := 0; i < limit && l.Next(); i++ {
		fi := l.FileInfo()
		dir, file := path.Split(fi.Name)
		if dir != s.dirPrefix {
			continue
		}
		if file == after {
			i--
			continue // ListFiles starting point is *included*
		}
		br, ok := blob.Parse(file)
		if !ok {
			return fmt.Errorf("b2: non-Camlistore object named %q found in bucket", file)
		}
		select {
		case dest <- blob.SizedRef{Ref: br, Size: uint32(fi.ContentLength)}:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return l.Err()
}

func (s *Storage) ReceiveBlob(br blob.Ref, source io.Reader) (blob.SizedRef, error) {
	var buf bytes.Buffer
	size, err := io.Copy(&buf, source)
	if err != nil {
		return blob.SizedRef{}, err
	}

	b := bytes.NewReader(buf.Bytes())
	fi, err := s.b.Upload(b, s.dirPrefix+br.String(), "")
	if err != nil {
		return blob.SizedRef{}, err
	}

	if int64(fi.ContentLength) != size {
		return blob.SizedRef{}, fmt.Errorf("b2: expected ContentLength %d, got %d", size, fi.ContentLength)
	}
	if br.HashName() == "sha1" && fi.ContentSHA1 != br.Digest() {
		return blob.SizedRef{}, fmt.Errorf("b2: expected ContentSHA1 %s, got %s", br.Digest(), fi.ContentSHA1)
	}

	if s.cache != nil {
		// NoHash because it's already verified if we read it without
		// errors from the source, and uploaded it without mismatch.
		blobserver.ReceiveNoHash(s.cache, br, &buf)
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
			fi, err := s.b.GetFileInfoByName(s.dirPrefix + br.String())
			if err == b2.FileNotFoundError {
				return nil
			}
			if err != nil {
				return err
			}
			if br.HashName() == "sha1" && fi.ContentSHA1 != br.Digest() {
				return errors.New("b2: remote ContentSHA1 mismatch")
			}
			size := fi.ContentLength
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
	r, fi, err := s.cl.DownloadFileByName(s.b.Name, s.dirPrefix+br.String())
	if err, ok := err.(*b2.Error); ok && err.Status == 404 {
		return nil, 0, os.ErrNotExist
	}
	if err != nil {
		return nil, 0, err
	}

	if br.HashName() == "sha1" && fi.ContentSHA1 != br.Digest() {
		return nil, 0, errors.New("b2: remote ContentSHA1 mismatch")
	}

	if fi.ContentLength >= 1<<32 {
		r.Close()
		return nil, 0, errors.New("object larger than a uint32")
	}
	size = uint32(fi.ContentLength)
	if size > constants.MaxBlobSize {
		r.Close()
		return nil, size, errors.New("object too big")
	}
	return r, size, nil
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
			fi, err := s.b.GetFileInfoByName(s.dirPrefix + br.String())
			if err != nil {
				return err
			}
			if fi == nil {
				return nil
			}
			if br.HashName() == "sha1" && fi.ContentSHA1 != br.Digest() {
				return errors.New("b2: remote ContentSHA1 mismatch")
			}
			return s.cl.DeleteFile(fi.ID, fi.Name)
		})
	}
	return grp.Err()
}

func init() {
	blobserver.RegisterStorageConstructor("b2", blobserver.StorageConstructor(newFromConfig))
}
