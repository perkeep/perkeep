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
	"os"
	"sort"
	"strings"
)

type readBlobRequest struct {
	ch      chan *blobref.SizedBlobRef
	after   string
	remain  *uint // limit countdown
	dirRoot string

	// Not used on initial request, only on recursion
	blobPrefix, pathInto string
}

type enumerateError struct {
	msg string
	err os.Error
}

func (ee *enumerateError) String() string {
	return fmt.Sprintf("Enumerate error: %s: %v", ee.msg, ee.err)
}

func readBlobs(opts readBlobRequest) os.Error {
	dirFullPath := opts.dirRoot + "/" + opts.pathInto
	dir, err := os.Open(dirFullPath, os.O_RDONLY, 0)
	if err != nil {
		return &enumerateError{"localdisk: opening directory " + dirFullPath, err}
	}
	defer dir.Close()
	names, err := dir.Readdirnames(32768)
	if err != nil {
		return &enumerateError{"localdisk: readdirnames of " + dirFullPath, err}
	}
	sort.SortStrings(names)
	for _, name := range names {
		if *opts.remain == 0 {
			return nil
		}

		fullPath := dirFullPath + "/" + name
		fi, err := os.Stat(fullPath)
		if err != nil {
			return &enumerateError{"localdisk: stat of file " + fullPath, err}
		}

		if fi.IsDirectory() {
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
				if newBlobPrefix[0:compareLen] < opts.after[0:compareLen] {
					continue
				}
			}
			ropts := opts
			ropts.blobPrefix = newBlobPrefix
			ropts.pathInto = opts.pathInto + "/" + name
			readBlobs(ropts)
			continue
		}

		if fi.IsRegular() && strings.HasSuffix(name, ".dat") {
			blobName := name[0 : len(name)-4]
			if blobName <= opts.after {
				continue
			}
			blobRef := blobref.Parse(blobName)
			if blobRef != nil {
				opts.ch <- &blobref.SizedBlobRef{BlobRef: blobRef, Size: fi.Size}
				(*opts.remain)--
			}
			continue
		}
	}

	if opts.pathInto == "" {
		opts.ch <- nil
	}
	return nil
}

func (ds *diskStorage) EnumerateBlobs(dest chan *blobref.SizedBlobRef, partition blobserver.Partition, after string, limit uint, waitSeconds int) os.Error {
	dirRoot := ds.root
	if partition != "" {
		dirRoot += "/partition/" + string(partition) + "/"
	}
	limitMutable := limit
	var err os.Error
	doScan := func() {
		err = readBlobs(readBlobRequest{
			ch:      dest,
			dirRoot: dirRoot,
			after:   after,
			remain:  &limitMutable,
		})
	}
	doScan()
	if err == nil && limitMutable == limit && waitSeconds > 0 {
		// TODO: sleep w/ blobhub + select
		doScan()
		return nil
	}
	return err
}
