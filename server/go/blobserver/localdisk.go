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

package main

import (
	"camli/blobref"
	"camli/blobserver"
	"fmt"
	"log"
	"os"
	"regexp"
	"sort"
	"strings"
)

type diskStorage struct {
	Root string
}

func (ds *diskStorage) Fetch(blob *blobref.BlobRef) (blobref.ReadSeekCloser, int64, os.Error) {
	fileName := BlobFileName(blob)
	stat, err := os.Stat(fileName)
	if err == os.ENOENT {
		return nil, 0, err
	}
	file, err := os.Open(fileName, os.O_RDONLY, 0)
	if err != nil {
		return nil, 0, err
	}
	return file, stat.Size, nil
}

func (ds *diskStorage) Remove(partition blobserver.Partition, blobs []*blobref.BlobRef) os.Error {
	for _, blob := range blobs {
		fileName := PartitionBlobFileName(partition, blob)
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

type readBlobRequest struct {
	ch     chan *blobref.SizedBlobRef
	after  string
	remain *uint         // limit countdown
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
		return &enumerateError{"opening directory " + dirFullPath, err}
	}
	defer dir.Close()
	names, err := dir.Readdirnames(32768)
	if err != nil {
		return &enumerateError{"readdirnames of " + dirFullPath, err}
	}
	sort.SortStrings(names)
	for _, name := range names {
		if *opts.remain == 0 {
			return nil
		}

		fullPath := dirFullPath + "/" + name
		fi, err := os.Stat(fullPath)
		if err != nil {
			return &enumerateError{"stat of file " + fullPath, err}
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
			ropts.pathInto = opts.pathInto+"/"+name
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

func (ds *diskStorage) EnumerateBlobs(dest chan *blobref.SizedBlobRef, partition blobserver.Partition, after string, limit uint) os.Error {
	dirRoot := *flagStorageRoot
	if partition != "" {
		dirRoot += "/partition/" + string(partition) + "/"
	}
	limitMutable := limit
	return readBlobs(readBlobRequest{
	   ch: dest,
	   dirRoot: dirRoot,
	   after: after,
	   remain: &limitMutable,
	})
}

func newDiskStorage(root string) *diskStorage {
	return &diskStorage{Root: root}
}

var kGetPutPattern *regexp.Regexp = regexp.MustCompile(`^/camli/([a-z0-9]+)-([a-f0-9]+)$`)

func BlobFileBaseName(b *blobref.BlobRef) string {
	return fmt.Sprintf("%s-%s.dat", b.HashName(), b.Digest())
}

func blobPartitionDirName(partitionDirSlash string, b *blobref.BlobRef) string {
	d := b.Digest()
	if len(d) < 6 {
		d = d + "______"
	}
	return fmt.Sprintf("%s/%s%s/%s/%s",
		*flagStorageRoot, partitionDirSlash,
		b.HashName(), d[0:3], d[3:6])
}

func BlobDirectoryName(b *blobref.BlobRef) string {
	return blobPartitionDirName("", b)
}

func BlobFileName(b *blobref.BlobRef) string {
	return fmt.Sprintf("%s/%s-%s.dat", BlobDirectoryName(b), b.HashName(), b.Digest())
}

func BlobPartitionDirectoryName(partition blobserver.Partition, b *blobref.BlobRef) string {
	return blobPartitionDirName("partition/" + string(partition) + "/", b)
}

func PartitionBlobFileName(partition blobserver.Partition, b *blobref.BlobRef) string {
	return fmt.Sprintf("%s/%s-%s.dat", BlobPartitionDirectoryName(partition, b), b.HashName(), b.Digest())
}

func BlobFromUrlPath(path string) *blobref.BlobRef {
	return blobref.FromPattern(kGetPutPattern, path)
}
