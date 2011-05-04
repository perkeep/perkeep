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
	"crypto"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
)

var _ = log.Printf

type Fetcher interface {
	// Fetch returns a blob.  If the blob is not found then
	// os.ENOENT should be returned for the error (not a wrapped
	// error with a ENOENT inside)
	Fetch(*BlobRef) (file ReadSeekCloser, size int64, err os.Error)
}

type StreamingFetcher interface {
	// Fetch returns a blob.  If the blob is not found then
	// os.ENOENT should be returned for the error (not a wrapped
	// error with a ENOENT inside)
	FetchStreaming(*BlobRef) (file io.ReadCloser, size int64, err os.Error)
}

func NewSerialFetcher(fetchers ...Fetcher) Fetcher {
	return &serialFetcher{fetchers}
}

func NewSerialStreamingFetcher(fetchers ...StreamingFetcher) StreamingFetcher {
	return &serialStreamingFetcher{fetchers}
}

func NewSimpleDirectoryFetcher(dir string) *DirFetcher {
	return &DirFetcher{dir, "camli"}
}

func NewConfigDirFetcher() *DirFetcher {
	configDir := filepath.Join(os.Getenv("HOME"), ".camli", "keyblobs")
	return NewSimpleDirectoryFetcher(configDir)
}

type serialFetcher struct {
	fetchers []Fetcher
}

func (sf *serialFetcher) Fetch(b *BlobRef) (file ReadSeekCloser, size int64, err os.Error) {
	for _, fetcher := range sf.fetchers {
		file, size, err = fetcher.Fetch(b)
		if err == nil {
			return
		}
	}
	return

}

type serialStreamingFetcher struct {
	fetchers []StreamingFetcher
}

func (sf *serialStreamingFetcher) FetchStreaming(b *BlobRef) (file io.ReadCloser, size int64, err os.Error) {
	for _, fetcher := range sf.fetchers {
		file, size, err = fetcher.FetchStreaming(b)
		if err == nil {
			return
		}
	}
	return
}

type DirFetcher struct {
	directory, extension string
}

func (df *DirFetcher) FetchStreaming(b *BlobRef) (file io.ReadCloser, size int64, err os.Error) {
	return df.Fetch(b)
}

func (df *DirFetcher) Fetch(b *BlobRef) (file ReadSeekCloser, size int64, err os.Error) {
	fileName := fmt.Sprintf("%s/%s.%s", df.directory, b.String(), df.extension)
	var stat *os.FileInfo
	stat, err = os.Stat(fileName)
	if err != nil {
		return
	}
	file, err = os.Open(fileName)
	if err != nil {
		return
	}
	size = stat.Size
	return
}

// MemoryStore stores blobs in memory and is a Fetcher and
// StreamingFetcher. Its zero value is usable.
type MemoryStore struct {
	m map[string][]byte
}

func (s *MemoryStore) AddBlob(hashtype crypto.Hash, data []byte) os.Error {
	if hashtype != crypto.SHA1 {
		return os.NewError("blobref: unsupported hash type")
	}
	hash := hashtype.New()
	hash.Write(data)
	bstr := fmt.Sprintf("sha1-%x", hash.Sum())
	s.m[bstr] = data
	log.Printf("added %s", bstr)
	return nil
}

