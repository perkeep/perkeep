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
package localdisk // import "camlistore.org/pkg/blobserver/localdisk"

import (
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sync"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/blobserver"
	"camlistore.org/pkg/blobserver/local"
	"camlistore.org/pkg/osutil"

	"go4.org/jsonconfig"
	"go4.org/syncutil"
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

	// tmpFileGate limits the number of temporary files open at the same
	// time, so we don't run into the max set by ulimit. It is nil on
	// systems (Windows) where we don't know the maximum number of open
	// file descriptors.
	tmpFileGate *syncutil.Gate
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

const (
	// We refuse to create a DiskStorage when the user's ulimit is lower than
	// minFDLimit. 100 is ridiculously low, but the default value on OSX is 256, and we
	// don't want to fail by default, so our min value has to be lower than 256.
	minFDLimit         = 100
	recommendedFDLimit = 1024
)

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
	ul, err := osutil.MaxFD()
	if err != nil {
		if err == osutil.ErrNotSupported {
			// Do not set the gate on Windows, since we don't know the ulimit.
			return ds, nil
		}
		return nil, err
	}
	if ul < minFDLimit {
		return nil, fmt.Errorf("The max number of open file descriptors on your system (ulimit -n) is too low. Please fix it with 'ulimit -S -n X' with X being at least %d.", recommendedFDLimit)
	}
	// Setting the gate to 80% of the ulimit, to leave a bit of room for other file ops happening in Camlistore.
	// TODO(mpl): make this used and enforced Camlistore-wide. Issue #837.
	ds.tmpFileGate = syncutil.NewGate(int(ul * 80 / 100))
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

// u32 converts n to an uint32, or panics if n is out of range
func u32(n int64) uint32 {
	if n < 0 || n > math.MaxUint32 {
		panic("bad size " + fmt.Sprint(n))
	}
	return uint32(n)
}

// length -1 means entire file
func (ds *DiskStorage) fetch(br blob.Ref, offset, length int64) (rc io.ReadCloser, size uint32, err error) {
	fileName := ds.blobPath(br)
	stat, err := os.Stat(fileName)
	if os.IsNotExist(err) {
		return nil, 0, os.ErrNotExist
	}
	size = u32(stat.Size())
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
