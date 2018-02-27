/*
Copyright 2018 The Perkeep Authors.

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

/*
Package overlay registers the "overlay" blobserver storage type
that presents storage that is the result of overlaying a
storage ("upper") on top of another storage ("lower").
All changes go to the upper storage. The lower storage is never changed.

The optional "deleted" KeyValue store may be provided to keep track of
deleted blobs. When "deleted" is missing, deletion returns an error.

Example usage:

  "/bs/": {
    "handler": "storage-overlay",
    "handlerArgs": {
      "lower": "/sto-base/",
      "upper": "/bs-local-changes/",
      "deleted": {
        "file": "/volume1/camlistore/home/var/camlistore/blobs/deleted.leveldb",
        "type": "leveldb"
      }
    }
  }
*/
package overlay // import "perkeep.org/pkg/blobserver/overlay"

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"time"

	"go4.org/jsonconfig"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver"
	"perkeep.org/pkg/sorted"
)

func init() {
	blobserver.RegisterStorageConstructor("overlay", blobserver.StorageConstructor(newFromConfig))
}

// readOnlyStorage is a blobserver.Storage with write methods removed.
type readOnlyStorage interface {
	blob.Fetcher
	blobserver.BlobEnumerator
	blobserver.BlobStatter
}

type overlayStorage struct {
	lower readOnlyStorage

	// deleted stores refs deleted from lower
	deleted sorted.KeyValue

	// read-write storage for changes
	upper blobserver.Storage
}

func newFromConfig(ld blobserver.Loader, conf jsonconfig.Obj) (blobserver.Storage, error) {
	var (
		lowerPrefix = conf.RequiredString("lower")
		upperPrefix = conf.RequiredString("upper")
		deletedConf = conf.OptionalObject("deleted")
	)
	if err := conf.Validate(); err != nil {
		return nil, err
	}

	lower, err := ld.GetStorage(lowerPrefix)
	if err != nil {
		return nil, fmt.Errorf("failed to load lower at %s: %v", lowerPrefix, err)
	}
	upper, err := ld.GetStorage(upperPrefix)
	if err != nil {
		return nil, fmt.Errorf("failed to load upper at %s: %v", upperPrefix, err)
	}
	var deleted sorted.KeyValue
	if len(deletedConf) != 0 {
		deleted, err = sorted.NewKeyValueMaybeWipe(deletedConf)
		if err != nil {
			return nil, fmt.Errorf("failed to setup deleted: %v", err)
		}
	}

	sto := &overlayStorage{
		lower:   lower,
		upper:   upper,
		deleted: deleted,
	}

	return sto, nil
}

func (sto *overlayStorage) Close() error {
	if sto.deleted == nil {
		return nil
	}
	return sto.deleted.Close()
}

// ReceiveBlob stores received blobs on the upper layer.
func (sto *overlayStorage) ReceiveBlob(ctx context.Context, br blob.Ref, src io.Reader) (sb blob.SizedRef, err error) {
	sb, err = sto.upper.ReceiveBlob(ctx, br, src)
	if err == nil && sto.deleted != nil {
		err = sto.deleted.Delete(br.String())
	}
	return sb, err
}

// RemoveBlobs marks the given blobs as deleted, and removes them if they are in the upper layer.
func (sto *overlayStorage) RemoveBlobs(ctx context.Context, blobs []blob.Ref) error {
	if sto.deleted == nil {
		return blobserver.ErrNotImplemented
	}

	err := sto.upper.RemoveBlobs(ctx, blobs)
	if err != nil {
		return err
	}

	m := sto.deleted.BeginBatch()
	for _, br := range blobs {
		m.Set(br.String(), "1")
	}
	return sto.deleted.CommitBatch(m)
}

func (sto *overlayStorage) isDeleted(br blob.Ref) bool {
	if sto.deleted == nil {
		return false
	}

	_, err := sto.deleted.Get(br.String())
	if err == nil {
		return true
	}

	if err != sorted.ErrNotFound {
		log.Printf("overlayStorage error accessing deleted: %v", err)
	}

	return false
}

// Fetch the blob by trying first the upper and then lower.
// The lower storage is checked only if the blob was not deleleted in sto itself.
func (sto *overlayStorage) Fetch(ctx context.Context, br blob.Ref) (file io.ReadCloser, size uint32, err error) {
	if sto.isDeleted(br) {
		return nil, 0, os.ErrNotExist
	}

	file, size, err = sto.upper.Fetch(ctx, br)
	if err != os.ErrNotExist {
		return file, size, err
	}

	return sto.lower.Fetch(ctx, br)
}

// StatBlobs on all BlobStatter reads sequentially, returning the first error.
func (sto *overlayStorage) StatBlobs(ctx context.Context, blobs []blob.Ref, f func(blob.SizedRef) error) error {
	exists := make([]blob.Ref, 0, len(blobs))
	for _, br := range blobs {
		if !sto.isDeleted(br) {
			exists = append(exists, br)
		}
	}

	seen := make(map[blob.Ref]struct{}, len(exists))

	err := sto.upper.StatBlobs(ctx, exists, func(sbr blob.SizedRef) error {
		seen[sbr.Ref] = struct{}{}
		return f(sbr)
	})

	if err != nil {
		return err
	}

	lowerBlobs := make([]blob.Ref, 0, len(exists))
	for _, br := range exists {
		if _, s := seen[br]; !s {
			lowerBlobs = append(lowerBlobs, br)
		}
	}

	return sto.lower.StatBlobs(ctx, lowerBlobs, f)
}

// EnumerateBlobs enumerates blobs of the lower and upper layers.
func (sto *overlayStorage) EnumerateBlobs(ctx context.Context, dest chan<- blob.SizedRef, after string, limit int) error {
	defer close(dest)

	enums := []blobserver.BlobEnumerator{sto.lower, sto.upper}

	// Ensure that we send limit blobs if possible.
	sent := 0
	for sent < limit {
		ch := make(chan blob.SizedRef)
		errch := make(chan error, 1)
		go func() {
			errch <- blobserver.MergedEnumerate(ctx, ch, enums, after, limit-sent)
		}()

		var last blob.Ref

		// Yield all blobs that weren't deleted from ch to destch.
		seen := 0
		for sbr := range ch {
			seen++
			if !sto.isDeleted(sbr.Ref) {
				log.Println(sent, sbr.Ref)
				dest <- sbr
				sent++
			}
			last = sbr.Ref
		}

		if err := <-errch; err != nil {
			return err
		}

		// if no blob was received, enumeration is finished
		if seen == 0 {
			return nil
		}

		// resume enumeration after the last blob seen
		after = last.String()
	}

	return nil
}

func (sto *overlayStorage) StorageGeneration() (initTime time.Time, random string, err error) {
	if gener, ok := sto.upper.(blobserver.Generationer); ok {
		return gener.StorageGeneration()
	}
	err = blobserver.GenerationNotSupportedError(fmt.Sprintf("blobserver.Generationer not implemented on %T", sto.upper))
	return
}

func (sto *overlayStorage) ResetStorageGeneration() error {
	if gener, ok := sto.upper.(blobserver.Generationer); ok {
		return gener.ResetStorageGeneration()
	}
	return blobserver.GenerationNotSupportedError(fmt.Sprintf("blobserver.Generationer not implemented on %T", sto.upper))
}
