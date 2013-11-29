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

/*
Package localdisk registers the "filesystem" blobserver storage type,
storing blobs in a forest of sharded directories at the specified root.

Example low-level config:

     "/storage/": {
         "handler": "storage-filesystem",
         "handlerArgs": {
            "path": "/var/camlistore/blobs"
          }
     },

*/
package localdisk

import (
	"fmt"
	"io"
	"os"
	"sync"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/blobserver"
	"camlistore.org/pkg/blobserver/local"
	"camlistore.org/pkg/jsonconfig"
	"camlistore.org/pkg/types"
)

// DiskStorage implements the blobserver.Storage interface using the
// local filesystem.
type DiskStorage struct {
	root string

	// dirLockMu must be held for writing when deleting an empty directory
	// and for read when receiving blobs.
	dirLockMu *sync.RWMutex

	// gen will be nil if partition != ""
	gen *local.Generationer
}

// New returns a new local disk storage implementation at the provided
// root directory, which must already exist.
func New(root string) (*DiskStorage, error) {
	// Local disk.
	fi, err := os.Stat(root)
	if os.IsNotExist(err) {
		return nil, fmt.Errorf("Storage root %q doesn't exist", root)
	}
	if err != nil {
		return nil, fmt.Errorf("Failed to stat directory %q: %v", root, err)
	}
	if !fi.IsDir() {
		return nil, fmt.Errorf("Storage root %q exists but is not a directory.", root)
	}
	ds := &DiskStorage{
		root:      root,
		dirLockMu: new(sync.RWMutex),
		gen:       local.NewGenerationer(root),
	}
	if err := ds.migrate3to2(); err != nil {
		return nil, fmt.Errorf("Error updating localdisk format: %v", err)
	}
	if _, _, err := ds.StorageGeneration(); err != nil {
		return nil, fmt.Errorf("Error initialization generation for %q: %v", root, err)
	}
	return ds, nil
}

func newFromConfig(_ blobserver.Loader, config jsonconfig.Obj) (storage blobserver.Storage, err error) {
	path := config.RequiredString("path")
	if err := config.Validate(); err != nil {
		return nil, err
	}
	return New(path)
}

func init() {
	blobserver.RegisterStorageConstructor("filesystem", blobserver.StorageConstructor(newFromConfig))
}

func (ds *DiskStorage) tryRemoveDir(dir string) {
	ds.dirLockMu.Lock()
	defer ds.dirLockMu.Unlock()
	os.Remove(dir) // ignore error
}

func (ds *DiskStorage) FetchStreaming(blob blob.Ref) (io.ReadCloser, int64, error) {
	return ds.Fetch(blob)
}

func (ds *DiskStorage) Fetch(blob blob.Ref) (types.ReadSeekCloser, int64, error) {
	fileName := ds.blobPath(blob)
	stat, err := os.Stat(fileName)
	if os.IsNotExist(err) {
		return nil, 0, os.ErrNotExist
	}
	file, err := os.Open(fileName)
	if err != nil {
		if os.IsNotExist(err) {
			err = os.ErrNotExist
		}
		return nil, 0, err
	}
	return file, stat.Size(), nil
}

func (ds *DiskStorage) RemoveBlobs(blobs []blob.Ref) error {
	for _, blob := range blobs {
		fileName := ds.blobPath(blob)
		err := os.Remove(fileName)
		switch {
		case err == nil:
			continue
		case os.IsNotExist(err):
			// deleting already-deleted file; harmless.
			continue
		default:
			return err
		}
	}
	return nil
}
