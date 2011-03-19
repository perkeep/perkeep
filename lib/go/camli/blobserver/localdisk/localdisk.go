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
	"camli/blobref"
	"camli/blobserver"
	"fmt"
	"log"
	"os"
	"sync"
)

type diskStorage struct {
	root string

	hubLock sync.Mutex
	hubMap  map[string]blobserver.BlobHub
}

func New(root string) (storage blobserver.Storage, err os.Error) {
	// Local disk.
	fi, staterr := os.Stat(root)
	if staterr != nil || !fi.IsDirectory() {
		err = os.NewError(fmt.Sprintf("Storage root %q doesn't exist or is not a directory.", root))
		return
	}
	storage = &diskStorage{
		root:   root,
		hubMap: make(map[string]blobserver.BlobHub),
	}
	return
}

func (ds *diskStorage) GetBlobHub(partition blobserver.Partition) blobserver.BlobHub {
	name := ""
	if partition != nil {
		name = partition.Name()
	}
	ds.hubLock.Lock()
	defer ds.hubLock.Unlock()
	if hub, ok := ds.hubMap[name]; ok {
		return hub
	}

	// TODO: in the future, allow for different blob hub
	// implementations rather than the
	// everything-in-memory-on-a-single-machine SimpleBlobHub.
	hub := new(blobserver.SimpleBlobHub)
	ds.hubMap[name] = hub
	return hub
}

func (ds *diskStorage) Fetch(blob *blobref.BlobRef) (blobref.ReadSeekCloser, int64, os.Error) {
	fileName := ds.blobPath(nil, blob)
	stat, err := os.Stat(fileName)
	if errorIsNoEnt(err) {
		return nil, 0, os.ENOENT
	}
	file, err := os.Open(fileName, os.O_RDONLY, 0)
	if err != nil {
		if errorIsNoEnt(err) {
			err = os.ENOENT
		}
		return nil, 0, err
	}
	return file, stat.Size, nil
}

func (ds *diskStorage) Remove(partition blobserver.Partition, blobs []*blobref.BlobRef) os.Error {
	for _, blob := range blobs {
		fileName := ds.blobPath(partition, blob)
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
