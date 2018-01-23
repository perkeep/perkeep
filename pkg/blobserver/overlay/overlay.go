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
that acts as a staging area which is read-write layer on top of a
base storage. The base storage is never changed.

Example usage:

  "/bs/": {
    "handler": "storage-overlay",
    "handlerArgs": {
      "base": "/sto-base/",
      "overlay": "/bs-local-changes/",
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
	base readOnlyStorage

	// deleted keeps refs deleted from base
	deleted sorted.KeyValue

	// read-write storage for changes
	stage blobserver.Storage
}

func newFromConfig(ld blobserver.Loader, conf jsonconfig.Obj) (blobserver.Storage, error) {
	var (
		sourcePrefix = conf.RequiredString("base")
		stagePrefix  = conf.RequiredString("stage")
		deletedConf  = conf.RequiredObject("deleted")
	)
	if err := conf.Validate(); err != nil {
		return nil, err
	}

	base, err := ld.GetStorage(sourcePrefix)
	if err != nil {
		return nil, fmt.Errorf("failed to load base at %s: %v", sourcePrefix, err)
	}
	stage, err := ld.GetStorage(stagePrefix)
	if err != nil {
		return nil, fmt.Errorf("failed to load stage at %s: %v", stagePrefix, err)
	}
	deleted, err := sorted.NewKeyValueMaybeWipe(deletedConf)
	if err != nil {
		return nil, fmt.Errorf("failed to setup deleted: %v", err)
	}

	sto := &overlayStorage{
		base:    base,
		stage:   stage,
		deleted: deleted,
	}

	return sto, nil
}

func (sto *overlayStorage) Close() error {
	return sto.deleted.Close()
}

// ReceiveBlob stores received blobs on the stage layer.
func (sto *overlayStorage) ReceiveBlob(ctx context.Context, br blob.Ref, src io.Reader) (sb blob.SizedRef, err error) {
	sb, err = sto.stage.ReceiveBlob(ctx, br, src)
	if err == nil {
		err = sto.deleted.Delete(br.String())
	}
	return sb, err
}

// RemoveBlobs marks the given blobs as deleted, and removes them if they are in the stage layer.
func (sto *overlayStorage) RemoveBlobs(ctx context.Context, blobs []blob.Ref) error {
	err := sto.stage.RemoveBlobs(ctx, blobs)
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
	_, err := sto.deleted.Get(br.String())
	if err == nil {
		return true
	}

	if err != sorted.ErrNotFound {
		log.Printf("overlayStorage error accessing deleted: %v", err)
	}

	return false
}

// Fetch the blob by trying first the stage and then base.
// The base storage is checked only if the blob was not deleleted in sto itself.
func (sto *overlayStorage) Fetch(ctx context.Context, br blob.Ref) (file io.ReadCloser, size uint32, err error) {
	if sto.isDeleted(br) {
		return nil, 0, os.ErrNotExist
	}

	file, size, err = sto.stage.Fetch(ctx, br)
	if err != os.ErrNotExist {
		return file, size, err
	}

	return sto.base.Fetch(ctx, br)
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

	err := sto.stage.StatBlobs(ctx, exists, func(sbr blob.SizedRef) error {
		seen[sbr.Ref] = struct{}{}
		return f(sbr)
	})

	if err != nil {
		return err
	}

	baseBlobs := exists[:0]
	for _, br := range exists {
		if _, s := seen[br]; !s {
			baseBlobs = append(baseBlobs, br)
		}
	}

	return sto.base.StatBlobs(ctx, baseBlobs, f)
}

// EnumerateBlobs enumerates blobs of the base and stage layers.
func (sto *overlayStorage) EnumerateBlobs(ctx context.Context, dest chan<- blob.SizedRef, after string, limit int) error {
	defer close(dest)

	enums := []blobserver.BlobEnumerator{sto.base, sto.stage}

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
	if gener, ok := sto.stage.(blobserver.Generationer); ok {
		return gener.StorageGeneration()
	}
	err = blobserver.GenerationNotSupportedError(fmt.Sprintf("blobserver.Generationer not implemented on %T", sto.stage))
	return
}

func (sto *overlayStorage) ResetStorageGeneration() error {
	if gener, ok := sto.stage.(blobserver.Generationer); ok {
		return gener.ResetStorageGeneration()
	}
	return blobserver.GenerationNotSupportedError(fmt.Sprintf("blobserver.Generationer not implemented on %T", sto.stage))
}
