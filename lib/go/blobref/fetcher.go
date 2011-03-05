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

package blobref

import (
	"fmt"
	"os"
)

type Fetcher interface {
	// Fetch returns a blob.  If the blob is not found then
	// os.ENOENT should be returned for the error (not a wrapped
	// error with a ENOENT inside)
	Fetch(*BlobRef) (file ReadSeekCloser, size int64, err os.Error)
}

func NewSimpleDirectoryFetcher(dir string) Fetcher {
	return &dirFetcher{dir, "camli"}
}

type dirFetcher struct {
	directory, extension string
}

func (df *dirFetcher) Fetch(b *BlobRef) (file ReadSeekCloser, size int64, err os.Error) {
	fileName := fmt.Sprintf("%s/%s.%s", df.directory, b.String(), df.extension)
	var stat *os.FileInfo
	stat, err = os.Stat(fileName)
	if err != nil {
		return
	}
	file, err = os.Open(fileName, os.O_RDONLY, 0)
	if err != nil {
		return
	}
	size = stat.Size
	return
}
