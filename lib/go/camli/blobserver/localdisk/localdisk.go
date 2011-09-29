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

package localdisk

import (
	"fmt"
	"log"
	"io"
	"os"
	"regexp"

	"camli/blobref"
	"camli/blobserver"
	"camli/jsonconfig"
)

type DiskStorage struct {
	*blobserver.SimpleBlobHubPartitionMap
	root string

	// the sub-partition (queue) to write to / read from, or "" for none.
	partition string

	// queue partitions to mirror new blobs into (when partition
	// above is the empty string)
	mirrorPartitions []*DiskStorage
}

func New(root string) (storage *DiskStorage, err os.Error) {
	// Local disk.
	fi, staterr := os.Stat(root)
	if staterr != nil || !fi.IsDirectory() {
		err = os.NewError(fmt.Sprintf("Storage root %q doesn't exist or is not a directory.", root))
		return
	}
	storage = &DiskStorage{
		SimpleBlobHubPartitionMap: &blobserver.SimpleBlobHubPartitionMap{},
		root:                      root,
	}
	return
}

func newFromConfig(_ blobserver.Loader, config jsonconfig.Obj) (storage blobserver.Storage, err os.Error) {
	sto := &DiskStorage{
		SimpleBlobHubPartitionMap: &blobserver.SimpleBlobHubPartitionMap{},
		root:                      config.RequiredString("path"),
	}
	if err := config.Validate(); err != nil {
		return nil, err
	}
	fi, err := os.Stat(sto.root)
	if err != nil {
		return nil, fmt.Errorf("Failed to stat directory %q: %v", sto.root, err)
	}
	if !fi.IsDirectory() {
		return nil, fmt.Errorf("Path %q isn't a directory", sto.root)
	}
	return sto, nil
}

func init() {
	blobserver.RegisterStorageConstructor("filesystem", blobserver.StorageConstructor(newFromConfig))
}

var validQueueName = regexp.MustCompile(`^[a-zA-Z0-9\-\_]+$`)

func (ds *DiskStorage) CreateQueue(name string) (blobserver.Storage, os.Error) {
	if !validQueueName.MatchString(name) {
		return nil, fmt.Errorf("invalid queue name %q", name)
	}
	if ds.partition != "" {
		return nil, fmt.Errorf("can't create queue %q on existing queue %q",
			name, ds.partition)
	}
	q := &DiskStorage{
		SimpleBlobHubPartitionMap: &blobserver.SimpleBlobHubPartitionMap{},
		root:                      ds.root,
		partition:                 "queue-" + name,
	}
	baseDir := ds.PartitionRoot(q.partition)
	if err := os.MkdirAll(baseDir, 0700); err != nil {
		return nil, fmt.Errorf("failed to create queue base dir: %v", err)
	}

	ds.mirrorPartitions = append(ds.mirrorPartitions, q)
	return q, nil
}

func (ds *DiskStorage) FetchStreaming(blob *blobref.BlobRef) (io.ReadCloser, int64, os.Error) {
	return ds.Fetch(blob)
}

func (ds *DiskStorage) Fetch(blob *blobref.BlobRef) (blobref.ReadSeekCloser, int64, os.Error) {
	fileName := ds.blobPath("", blob)
	stat, err := os.Stat(fileName)
	if errorIsNoEnt(err) {
		return nil, 0, os.ENOENT
	}
	file, err := os.Open(fileName)
	if err != nil {
		if errorIsNoEnt(err) {
			err = os.ENOENT
		}
		return nil, 0, err
	}
	return file, stat.Size, nil
}

func (ds *DiskStorage) RemoveBlobs(blobs []*blobref.BlobRef) os.Error {
	for _, blob := range blobs {
		fileName := ds.blobPath(ds.partition, blob)
		err := os.Remove(fileName)
		switch {
		case err == nil:
			continue
		case errorIsNoEnt(err):
			log.Printf("Deleting already-deleted file; harmless.")
			continue
		default:
			return err
		}
	}
	return nil
}
