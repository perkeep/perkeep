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
	"os"
	"path/filepath"
	"sort"
	"strings"

	"camlistore.org/pkg/blob"
)

type readBlobRequest struct {
	ch      chan<- blob.SizedRef
	after   string
	remain  *int // limit countdown
	dirRoot string

	// Not used on initial request, only on recursion
	blobPrefix, pathInto string
}

type enumerateError struct {
	msg string
	err error
}

func (ee *enumerateError) Error() string {
	return fmt.Sprintf("Enumerate error: %s: %v", ee.msg, ee.err)
}

func (ds *DiskStorage) readBlobs(opts readBlobRequest) error {
	dirFullPath := filepath.Join(opts.dirRoot, opts.pathInto)
	dir, err := os.Open(dirFullPath)
	if err != nil {
		return &enumerateError{"localdisk: opening directory " + dirFullPath, err}
	}
	defer dir.Close()
	names, err := dir.Readdirnames(-1)
	if err == nil && len(names) == 0 {
		// remove empty blob dir if we are in a queue but not the queue root itself
		if strings.Contains(dirFullPath, "queue-") &&
			!strings.Contains(filepath.Base(dirFullPath), "queue-") {
			go ds.tryRemoveDir(dirFullPath)
		}
		return nil
	}
	if err != nil {
		return &enumerateError{"localdisk: readdirnames of " + dirFullPath, err}
	}
	sort.Strings(names)
	for _, name := range names {
		if *opts.remain == 0 {
			return nil
		}
		if name == "partition" || name == "cache" {
			// "partition" is actually used by this package but
			// the "cache" directory is just a hack: it's used
			// by the serverconfig/genconfig code, as a default
			// location for most users to put their thumbnail
			// cache.  For now we just also skip it here.
			continue
		}
		fullPath := dirFullPath + "/" + name
		fi, err := os.Stat(fullPath)
		if err != nil {
			return &enumerateError{"localdisk: stat of file " + fullPath, err}
		}

		if fi.IsDir() {
			var newBlobPrefix string
			if opts.blobPrefix == "" {
				newBlobPrefix = name + "-"
			} else {
				newBlobPrefix = opts.blobPrefix + name
			}
			if len(opts.after) > 0 {
				compareLen := len(newBlobPrefix)
				if len(opts.after) < compareLen {
					compareLen = len(opts.after)
				}
				if newBlobPrefix[:compareLen] < opts.after[:compareLen] {
					continue
				}
			}
			ropts := opts
			ropts.blobPrefix = newBlobPrefix
			ropts.pathInto = opts.pathInto + "/" + name
			ds.readBlobs(ropts)
			continue
		}

		if !fi.IsDir() && strings.HasSuffix(name, ".dat") {
			blobName := name[:len(name)-len(".dat")]
			if blobName <= opts.after {
				continue
			}
			if blobRef, ok := blob.Parse(blobName); ok {
				opts.ch <- blob.SizedRef{Ref: blobRef, Size: fi.Size()}
				(*opts.remain)--
			}
			continue
		}
	}

	return nil
}

func (ds *DiskStorage) EnumerateBlobs(dest chan<- blob.SizedRef, after string, limit int) error {
	defer close(dest)
	if limit == 0 {
		log.Printf("Warning: localdisk.EnumerateBlobs called with a limit of 0")
	}

	dirRoot := ds.PartitionRoot(ds.partition)
	limitMutable := limit
	return ds.readBlobs(readBlobRequest{
		ch:      dest,
		dirRoot: dirRoot,
		after:   after,
		remain:  &limitMutable,
	})
}
