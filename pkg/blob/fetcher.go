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

package blob

import (
	"crypto"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"math"
	"os"
	"strings"
	"sync"
)

// Fetcher is the minimal interface for retrieving a blob from storage.
// The full storage interface is blobserver.Stoage.
type Fetcher interface {
	// Fetch returns a blob.  If the blob is not found then
	// os.ErrNotExist should be returned for the error (not a wrapped
	// error with a ErrNotExist inside)
	//
	// The contents are not guaranteed to match the digest of the
	// provided Ref (e.g. when streamed over HTTP). Paranoid
	// callers should verify them.
	//
	// The caller must close blob.
	Fetch(Ref) (blob io.ReadCloser, size uint32, err error)
}

func NewSerialFetcher(fetchers ...Fetcher) Fetcher {
	return &serialFetcher{fetchers}
}

func NewSimpleDirectoryFetcher(dir string) *DirFetcher {
	return &DirFetcher{dir, "camli"}
}

type serialFetcher struct {
	fetchers []Fetcher
}

func (sf *serialFetcher) Fetch(r Ref) (file io.ReadCloser, size uint32, err error) {
	for _, fetcher := range sf.fetchers {
		file, size, err = fetcher.Fetch(r)
		if err == nil {
			return
		}
	}
	return
}

type DirFetcher struct {
	directory, extension string
}

func (df *DirFetcher) Fetch(r Ref) (file io.ReadCloser, size uint32, err error) {
	fileName := fmt.Sprintf("%s/%s.%s", df.directory, r.String(), df.extension)
	var stat os.FileInfo
	stat, err = os.Stat(fileName)
	if err != nil {
		return
	}
	if stat.Size() > math.MaxUint32 {
		err = errors.New("file size too big")
		return
	}
	file, err = os.Open(fileName)
	if err != nil {
		return
	}
	size = uint32(stat.Size())
	return
}

// MemoryStore stores blobs in memory and is a Fetcher and
// Fetcher. Its zero value is usable.
type MemoryStore struct {
	lk sync.Mutex
	m  map[string]string
}

func (s *MemoryStore) AddBlob(hashtype crypto.Hash, data string) (Ref, error) {
	if hashtype != crypto.SHA1 {
		return Ref{}, errors.New("blobref: unsupported hash type")
	}
	hash := hashtype.New()
	hash.Write([]byte(data))
	bstr := fmt.Sprintf("sha1-%x", hash.Sum(nil))
	s.lk.Lock()
	defer s.lk.Unlock()
	if s.m == nil {
		s.m = make(map[string]string)
	}
	s.m[bstr] = data
	return MustParse(bstr), nil
}

func (s *MemoryStore) Fetch(b Ref) (file io.ReadCloser, size uint32, err error) {
	s.lk.Lock()
	defer s.lk.Unlock()
	if s.m == nil {
		return nil, 0, os.ErrNotExist
	}
	str, ok := s.m[b.String()]
	if !ok {
		return nil, 0, os.ErrNotExist
	}
	return ioutil.NopCloser(strings.NewReader(str)), uint32(len(str)), nil
}
