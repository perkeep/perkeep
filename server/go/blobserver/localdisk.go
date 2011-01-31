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
	"fmt"
	"regexp"
	"os"
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

func newDiskStorage(root string) *diskStorage {
	return &diskStorage{Root: root}
}

var kGetPutPattern *regexp.Regexp = regexp.MustCompile(`^/camli/([a-z0-9]+)-([a-f0-9]+)$`)

func BlobFileBaseName(b *blobref.BlobRef) string {
	return fmt.Sprintf("%s-%s.dat", b.HashName(), b.Digest())
}

func BlobDirectoryName(b *blobref.BlobRef) string {
	d := b.Digest()
	if len(d) < 6 {
		d = d + "______"
	}
	return fmt.Sprintf("%s/%s/%s/%s", *flagStorageRoot, b.HashName(), d[0:3], d[3:6])
}

func BlobFileName(b *blobref.BlobRef) string {
	return fmt.Sprintf("%s/%s-%s.dat", BlobDirectoryName(b), b.HashName(), b.Digest())
}

func BlobFromUrlPath(path string) *blobref.BlobRef {
	return blobref.FromPattern(kGetPutPattern, path)
}
