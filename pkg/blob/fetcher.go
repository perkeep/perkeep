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
	"bytes"
	"crypto"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"math"
	"os"
	"strings"
	"sync"

	"camlistore.org/pkg/constants"
	"camlistore.org/pkg/types"
)

// TODO: rename StreamingFetcher to be Fetcher (the common case)

// TODO: add FetcherAt / FetchAt (for HTTP range requests).  But then how
// to make all SeekFetcer also be a FetchAt? By hand?

type SeekFetcher interface {
	// Fetch returns a blob.  If the blob is not found then
	// os.ErrNotExist should be returned for the error (not a wrapped
	// error with a ErrNotExist inside)
	//
	// The caller should close blob.
	Fetch(Ref) (blob types.ReadSeekCloser, size uint32, err error)
}

// SeekTester is the interface implemented by storage implementations that don't
// know until runtime whether or not their StreamingFetcher happens to also
// return a ReadCloser that's also a ReadSeekCloser.
type SeekTester interface {
	IsFetcherASeeker() bool
}

// fetcherToSeekerWrapper wraps a StreamingFetcher and converts it into
// a SeekFetcher if SeekTester has confirmed the interface conversion
// is safe.
type fetcherToSeekerWrapper struct {
	StreamingFetcher
}

func (w *fetcherToSeekerWrapper) Fetch(r Ref) (file types.ReadSeekCloser, size uint32, err error) {
	rc, size, err := w.StreamingFetcher.FetchStreaming(r)
	if err != nil {
		return
	}
	file, ok := rc.(types.ReadSeekCloser)
	if ok {
		return
	}
	// we must make it seekable
	var slurp bytes.Buffer
	n, err := io.CopyN(&slurp, rc, constants.MaxBlobSize+1)
	if err != nil && err != io.EOF {
		return nil, 0, err
	}
	if n > constants.MaxBlobSize {
		return nil, 0, fmt.Errorf("blob %v too big", r)
	}
	return struct {
		io.ReadSeeker
		io.Closer
	}{
		bytes.NewReader(slurp.Bytes()),
		types.NopCloser,
	}, uint32(n), err
}

// StreamingFetcher is the minimal interface for retrieving a blob from storage.
// The full storage interface is blobserver.Stoage.
type StreamingFetcher interface {
	// FetchStreaming returns a blob.  If the blob is not found then
	// os.ErrNotExist should be returned for the error (not a wrapped
	// error with a ErrNotExist inside)
	//
	// The caller should close blob.
	FetchStreaming(Ref) (blob io.ReadCloser, size uint32, err error)
}

func NewSerialFetcher(fetchers ...SeekFetcher) SeekFetcher {
	return &serialFetcher{fetchers}
}

func NewSerialStreamingFetcher(fetchers ...StreamingFetcher) StreamingFetcher {
	return &serialStreamingFetcher{fetchers}
}

func NewSimpleDirectoryFetcher(dir string) *DirFetcher {
	return &DirFetcher{dir, "camli"}
}

type serialFetcher struct {
	fetchers []SeekFetcher
}

func (sf *serialFetcher) Fetch(r Ref) (file types.ReadSeekCloser, size uint32, err error) {
	for _, fetcher := range sf.fetchers {
		file, size, err = fetcher.Fetch(r)
		if err == nil {
			return
		}
	}
	return

}

type serialStreamingFetcher struct {
	fetchers []StreamingFetcher
}

func (sf *serialStreamingFetcher) FetchStreaming(r Ref) (file io.ReadCloser, size uint32, err error) {
	for _, fetcher := range sf.fetchers {
		file, size, err = fetcher.FetchStreaming(r)
		if err == nil {
			return
		}
	}
	return
}

type DirFetcher struct {
	directory, extension string
}

func (df *DirFetcher) FetchStreaming(r Ref) (file io.ReadCloser, size uint32, err error) {
	return df.Fetch(r)
}

func (df *DirFetcher) Fetch(r Ref) (file types.ReadSeekCloser, size uint32, err error) {
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
// StreamingFetcher. Its zero value is usable.
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

func (s *MemoryStore) FetchStreaming(b Ref) (file io.ReadCloser, size uint32, err error) {
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

// SeekerFromStreamingFetcher returns the most efficient implementation of a seeking fetcher
// from a provided streaming fetcher.
func SeekerFromStreamingFetcher(f StreamingFetcher) SeekFetcher {
	if sk, ok := f.(SeekFetcher); ok {
		return sk
	}
	if tester, ok := f.(SeekTester); ok && tester.IsFetcherASeeker() {
		return &fetcherToSeekerWrapper{f}
	}
	return bufferingSeekFetcherWrapper{f}
}

// bufferingSeekFetcherWrapper is a SeekFetcher that implements
// seeking on a wrapped streaming-only fetcher by buffering the
// content into memory, optionally spilling to disk if local disk is
// available.  In practice, most blobs will be "small" (able to fit in
// memory).
type bufferingSeekFetcherWrapper struct {
	sf StreamingFetcher
}

func (b bufferingSeekFetcherWrapper) Fetch(br Ref) (rsc types.ReadSeekCloser, size uint32, err error) {
	rc, size, err := b.sf.FetchStreaming(br)
	if err != nil {
		return nil, 0, err
	}
	defer rc.Close()

	const tryDiskThreshold = 32 << 20
	if size > tryDiskThreshold {
		// TODO(bradfitz): disk spilling, if a temp file can be made
	}

	// Buffer all to memory
	var buf bytes.Buffer
	n, err := io.Copy(&buf, rc)
	if err != nil {
		return nil, 0, fmt.Errorf("Error reading blob %s: %v", br, err)
	}
	if n != int64(size) {
		return nil, 0, fmt.Errorf("Read %d bytes of %s; expected %s", n, br, size)
	}
	return struct {
		io.ReadSeeker
		io.Closer
	}{
		ReadSeeker: io.NewSectionReader(bytes.NewReader(buf.Bytes()), 0, int64(size)),
		Closer:     types.NopCloser,
	}, size, nil
}
