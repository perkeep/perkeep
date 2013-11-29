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
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
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

func (ds *DiskStorage) migrate3to2() error {
	sha1root := filepath.Join(ds.root, "sha1")
	f, err := os.Open(sha1root)
	if os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	names, err := f.Readdirnames(-1)
	if err != nil {
		return err
	}
	f.Close()
	var three []string
	for _, name := range names {
		if len(name) == 3 {
			three = append(three, name)
		}
	}
	if len(three) == 0 {
		return nil
	}
	sort.Strings(three)
	made := make(map[string]bool) // dirs made
	for i, dir := range three {
		oldDir := make(map[string]bool)
		log.Printf("Migrating structure of %d/%d directories in %s; doing %q", i+1, len(three), sha1root, dir)
		fullDir := filepath.Join(sha1root, dir)
		err := filepath.Walk(fullDir, func(path string, fi os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			baseName := filepath.Base(path)
			if !(fi.Mode().IsRegular() && strings.HasSuffix(baseName, ".dat")) {
				return nil
			}
			br, ok := blob.Parse(strings.TrimSuffix(baseName, ".dat"))
			if !ok {
				return nil
			}
			dir := ds.blobDirectory(br)
			if !made[dir] {
				if err := os.MkdirAll(dir, 0700); err != nil {
					return err
				}
				made[dir] = true
			}
			dst := ds.blobPath(br)
			if fi, err := os.Stat(dst); !os.IsNotExist(err) {
				return fmt.Errorf("Expected %s to not exist; got stat %v, %v", fi, err)
			}
			if err := os.Rename(path, dst); err != nil {
				return err
			}
			oldDir[filepath.Dir(path)] = true
			return nil
		})
		if err != nil {
			return err
		}
		tryDel := make([]string, 0, len(oldDir))
		for dir := range oldDir {
			tryDel = append(tryDel, dir)
		}
		sort.Sort(sort.Reverse(byStringLength(tryDel)))
		for _, dir := range tryDel {
			if err := os.Remove(dir); err != nil {
				log.Printf("Failed to remove old dir %s: %v", dir, err)
			}
		}
		if err := os.Remove(fullDir); err != nil {
			log.Printf("Failed to remove old dir %s: %v", fullDir, err)
		}
	}
	return nil
}

type byStringLength []string

func (s byStringLength) Len() int           { return len(s) }
func (s byStringLength) Less(i, j int) bool { return len(s[i]) < len(s[j]) }
func (s byStringLength) Swap(i, j int)      { s[i], s[j] = s[j], s[i] }

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
