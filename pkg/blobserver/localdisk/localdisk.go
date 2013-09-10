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
	"regexp"
	"sync"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/blobserver"
	"camlistore.org/pkg/jsonconfig"
	"camlistore.org/pkg/types"
)

// DiskStorage implements the blobserver.Storage interface using the
// local filesystem.
type DiskStorage struct {
	root string

	// the sub-partition (queue) to write to / read from, or "" for none.
	partition string

	// queue partitions to mirror new blobs into (when partition
	// above is the empty string)
	mirrorPartitions []*DiskStorage

	// dirLockMu must be held for writing when deleting an empty directory
	// and for read when receiving blobs.
	// The same lock is shared between queues created from a parent,
	// since they interact with overlapping sets of directories.
	dirLockMu *sync.RWMutex
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

var validQueueName = regexp.MustCompile(`^[a-zA-Z0-9\-\_]+$`)

func (ds *DiskStorage) tryRemoveDir(dir string) {
	ds.dirLockMu.Lock()
	defer ds.dirLockMu.Unlock()
	os.Remove(dir) // ignore error
}

func (ds *DiskStorage) CreateQueue(name string) (blobserver.Storage, error) {
	if !validQueueName.MatchString(name) {
		return nil, fmt.Errorf("invalid queue name %q", name)
	}
	if ds.partition != "" {
		return nil, fmt.Errorf("can't create queue %q on existing queue %q",
			name, ds.partition)
	}
	q := &DiskStorage{
		root:      ds.root,
		partition: "queue-" + name,
		dirLockMu: ds.dirLockMu, // see comment on DiskStorage type
	}
	baseDir := ds.PartitionRoot(q.partition)
	if err := os.MkdirAll(baseDir, 0700); err != nil {
		return nil, fmt.Errorf("failed to create queue base dir: %v", err)
	}

	ds.mirrorPartitions = append(ds.mirrorPartitions, q)
	return q, nil
}

func (ds *DiskStorage) FetchStreaming(blob blob.Ref) (io.ReadCloser, int64, error) {
	return ds.Fetch(blob)
}

func (ds *DiskStorage) Fetch(blob blob.Ref) (types.ReadSeekCloser, int64, error) {
	fileName := ds.blobPath("", blob)
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
		fileName := ds.blobPath(ds.partition, blob)
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
