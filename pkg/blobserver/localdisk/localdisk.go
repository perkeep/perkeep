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
	"path/filepath"
	"sync"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/blobserver"
	"camlistore.org/pkg/blobserver/local"
	"camlistore.org/pkg/osutil"
	"camlistore.org/pkg/types"
	"go4.org/jsonconfig"
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

func (ds *DiskStorage) String() string {
	return fmt.Sprintf("\"filesystem\" file-per-blob at %s", ds.root)
}

// IsDir reports whether root is a localdisk (file-per-blob) storage directory.
func IsDir(root string) (bool, error) {
	if osutil.DirExists(filepath.Join(root, blob.RefFromString("").HashName())) {
		return true, nil
	}
	return false, nil
}

// New returns a new local disk storage implementation at the provided
// root directory, which must already exist.
func New(root string) (*DiskStorage, error) {
	// Local disk.
	fi, err := os.Stat(root)
	if os.IsNotExist(err) {
		// As a special case, we auto-created the "packed" directory for subpacked.
		if filepath.Base(root) == "packed" {
			if err := os.Mkdir(root, 0700); err != nil {
				return nil, fmt.Errorf("failed to mkdir packed directory: %v", err)
			}
			fi, err = os.Stat(root)
		} else {
			return nil, fmt.Errorf("Storage root %q doesn't exist", root)
		}
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

func (ds *DiskStorage) Fetch(br blob.Ref) (io.ReadCloser, uint32, error) {
	return ds.fetch(br, 0, -1)
}

func (ds *DiskStorage) SubFetch(br blob.Ref, offset, length int64) (io.ReadCloser, error) {
	if offset < 0 || length < 0 {
		return nil, blob.ErrNegativeSubFetch
	}
	rc, _, err := ds.fetch(br, offset, length)
	return rc, err
}

// length -1 means entire file
func (ds *DiskStorage) fetch(br blob.Ref, offset, length int64) (rc io.ReadCloser, size uint32, err error) {
	fileName := ds.blobPath(br)
	stat, err := os.Stat(fileName)
	if os.IsNotExist(err) {
		return nil, 0, os.ErrNotExist
	}
	size = types.U32(stat.Size())
	file, err := os.Open(fileName)
	if err != nil {
		if os.IsNotExist(err) {
			err = os.ErrNotExist
		}
		return nil, 0, err
	}
	// normal Fetch:
	if length < 0 {
		return file, size, nil
	}
	// SubFetch:
	if offset < 0 || offset > stat.Size() {
		if offset < 0 {
			return nil, 0, blob.ErrNegativeSubFetch
		}
		return nil, 0, blob.ErrOutOfRangeOffsetSubFetch
	}
	return struct {
		io.Reader
		io.Closer
	}{
		io.NewSectionReader(file, offset, length),
		file,
	}, 0 /* unused */, err
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
