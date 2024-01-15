/*
Copyright 2011 The Perkeep Authors

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
package localdisk // import "perkeep.org/pkg/blobserver/localdisk"

import (
	"bytes"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"perkeep.org/internal/osutil"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver"
	"perkeep.org/pkg/blobserver/files"
	"perkeep.org/pkg/blobserver/local"

	"go4.org/jsonconfig"
	"go4.org/syncutil"
)

// TODO: rename DiskStorage to Storage.

// DiskStorage implements the blobserver.Storage interface using the
// local filesystem.
type DiskStorage struct {
	blobserver.Storage
	blob.SubFetcher

	root string

	// gen will be nil if partition != ""
	gen *local.Generationer
}

// Validate we implement expected interfaces.
var (
	_ blobserver.Storage = (*DiskStorage)(nil)
	_ blob.SubFetcher    = (*DiskStorage)(nil) // for blobpacked; Issue 1136
)

func (ds *DiskStorage) String() string {
	return fmt.Sprintf("\"filesystem\" file-per-blob at %s", ds.root)
}

// IsDir reports whether root is a localdisk (file-per-blob) storage directory.
func IsDir(root string) (bool, error) {
	if osutil.DirExists(filepath.Join(root, "sha1")) {
		return true, nil
	}
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
		return nil, fmt.Errorf("storage root %q exists but is not a directory", root)
	}
	fileSto := files.NewStorage(files.OSFS(), root)
	ds := &DiskStorage{
		Storage:    fileSto,
		SubFetcher: fileSto,
		root:       root,
		gen:        local.NewGenerationer(root),
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
		return nil, fmt.Errorf("the max number of open file descriptors on your system (ulimit -n) is too low. Please fix it with 'ulimit -S -n X' with X being at least %d", recommendedFDLimit)
	}
	// Setting the gate to 80% of the ulimit, to leave a bit of room for other file ops happening in Perkeep.
	// TODO(mpl): make this used and enforced Perkeep-wide. Issue #837.
	fileSto.SetNewFileGate(syncutil.NewGate(int(ul * 80 / 100)))

	err = ds.checkFS()
	if err != nil {
		return nil, err
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

// checkFS verifies the DiskStorage root storage path
// operations include: stat, read/write file, mkdir, delete (files and directories)
//
// TODO: move this into the files package too?
func (ds *DiskStorage) checkFS() (ret error) {
	tempdir, err := os.MkdirTemp(ds.root, "")
	if err != nil {
		return fmt.Errorf("localdisk check: unable to create tempdir in %s, err=%v", ds.root, err)
	}
	defer func() {
		err := os.RemoveAll(tempdir)
		if err != nil {
			cleanErr := fmt.Errorf("localdisk check: unable to clean temp dir: %v", err)
			if ret == nil {
				ret = cleanErr
			} else {
				log.Printf("WARNING: %v", cleanErr)
			}
		}
	}()

	tempfile := filepath.Join(tempdir, "FILE.tmp")
	filename := filepath.Join(tempdir, "FILE")
	data := []byte("perkeep rocks")
	err = os.WriteFile(tempfile, data, 0644)
	if err != nil {
		return fmt.Errorf("localdisk check: unable to write into %s, err=%v", ds.root, err)
	}

	out, err := os.ReadFile(tempfile)
	if err != nil {
		return fmt.Errorf("localdisk check: unable to read from %s, err=%v", tempfile, err)
	}
	if !bytes.Equal(out, data) {
		return fmt.Errorf("localdisk check: tempfile contents didn't match, got=%q", out)
	}
	if _, err := os.Lstat(filename); !os.IsNotExist(err) {
		return fmt.Errorf("localdisk check: didn't expect file to exist, Lstat had other error, err=%v", err)
	}
	if err := os.Rename(tempfile, filename); err != nil {
		return fmt.Errorf("localdisk check: rename failed, err=%v", err)
	}
	if _, err := os.Lstat(filename); err != nil {
		return fmt.Errorf("localdisk check: after rename passed Lstat had error, err=%v", err)
	}
	return nil
}
