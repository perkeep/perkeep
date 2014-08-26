/*
Copyright 2014 The Camlistore Authors

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

// Package memory registers the "memory" blobserver storage type, storing blobs
// in an in-memory map.
package memory

import (
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"sort"
	"sync"
	"sync/atomic"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/blobserver"
	"camlistore.org/pkg/context"
	"camlistore.org/pkg/jsonconfig"
	"camlistore.org/pkg/types"
)

// Storage is an in-memory implementation of the blobserver Storage
// interface. It also includes other convenience methods used by
// tests.
type Storage struct {
	mu     sync.RWMutex        // guards following 2 fields.
	m      map[blob.Ref][]byte // maps blob ref to its contents
	sorted []string            // blobrefs sorted

	blobsFetched int64 // atomic
	bytesFetched int64 // atomic
}

func init() {
	blobserver.RegisterStorageConstructor("memory", blobserver.StorageConstructor(newFromConfig))
}

func newFromConfig(_ blobserver.Loader, config jsonconfig.Obj) (blobserver.Storage, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	return &Storage{}, nil
}

func (s *Storage) Fetch(ref blob.Ref) (file io.ReadCloser, size uint32, err error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.m == nil {
		err = os.ErrNotExist
		return
	}
	b, ok := s.m[ref]
	if !ok {
		err = os.ErrNotExist
		return
	}
	size = uint32(len(b))
	atomic.AddInt64(&s.blobsFetched, 1)
	atomic.AddInt64(&s.bytesFetched, int64(len(b)))

	return struct {
		*io.SectionReader
		io.Closer
	}{
		io.NewSectionReader(bytes.NewReader(b), 0, int64(size)),
		types.NopCloser,
	}, size, nil
}

func (s *Storage) SubFetch(ref blob.Ref, offset, length int64) (io.ReadCloser, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	b, ok := s.m[ref]
	if !ok {
		return nil, os.ErrNotExist
	}
	atomic.AddInt64(&s.blobsFetched, 1)
	atomic.AddInt64(&s.bytesFetched, length)

	return struct {
		*io.SectionReader
		io.Closer
	}{
		io.NewSectionReader(bytes.NewReader(b), offset, int64(length)),
		types.NopCloser,
	}, nil
}

func (s *Storage) ReceiveBlob(br blob.Ref, source io.Reader) (blob.SizedRef, error) {
	sb := blob.SizedRef{}
	h := br.Hash()
	if h == nil {
		return sb, fmt.Errorf("Unsupported blobref hash for %s", br)
	}
	all, err := ioutil.ReadAll(io.TeeReader(source, h))
	if err != nil {
		return sb, err
	}
	if !br.HashMatches(h) {
		// This is a somewhat redundant check, since
		// blobserver.Receive now does it. But for testing code,
		// it's worth the cost.
		return sb, fmt.Errorf("Hash mismatch receiving blob %s", br)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.m == nil {
		s.m = make(map[blob.Ref][]byte)
	}
	_, had := s.m[br]
	if !had {
		s.m[br] = all
		s.sorted = append(s.sorted, br.String())
		sort.Strings(s.sorted)
	}
	return blob.SizedRef{br, uint32(len(all))}, nil
}

func (s *Storage) StatBlobs(dest chan<- blob.SizedRef, blobs []blob.Ref) error {
	for _, br := range blobs {
		s.mu.RLock()
		b, ok := s.m[br]
		s.mu.RUnlock()
		if ok {
			dest <- blob.SizedRef{br, uint32(len(b))}
		}
	}
	return nil
}

func (s *Storage) EnumerateBlobs(ctx *context.Context, dest chan<- blob.SizedRef, after string, limit int) error {
	defer close(dest)
	s.mu.RLock()
	defer s.mu.RUnlock()
	n := 0
	for _, k := range s.sorted {
		if k <= after {
			continue
		}
		br := blob.MustParse(k)
		select {
		case dest <- blob.SizedRef{br, uint32(len(s.m[br]))}:
		case <-ctx.Done():
			return context.ErrCanceled
		}
		n++
		if limit > 0 && n == limit {
			break
		}
	}
	return nil
}

func (s *Storage) RemoveBlobs(blobs []blob.Ref) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, br := range blobs {
		delete(s.m, br)
	}
	s.sorted = s.sorted[:0]
	for k := range s.m {
		s.sorted = append(s.sorted, k.String())
	}
	sort.Strings(s.sorted)
	return nil
}

// BlobContents returns as a string the contents of the blob br.
func (s *Storage) BlobContents(br blob.Ref) (contents string, ok bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	b, ok := s.m[br]
	if !ok {
		return
	}
	return string(b), true
}

// NumBlobs returns the number of blobs stored in s.
func (s *Storage) NumBlobs() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.m)
}

// SumBlobSize returns the total size in bytes of all the blobs in s.
func (s *Storage) SumBlobSize() int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var n int64
	for _, b := range s.m {
		n += int64(len(b))
	}
	return n
}

// BlobrefStrings returns the sorted stringified blobrefs stored in s.
func (s *Storage) BlobrefStrings() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sorted := make([]string, len(s.sorted))
	copy(sorted, s.sorted)
	return sorted
}

// Stats returns the number of blobs and number of bytes that were fetched from s.
func (s *Storage) Stats() (blobsFetched, bytesFetched int64) {
	return atomic.LoadInt64(&s.blobsFetched), atomic.LoadInt64(&s.bytesFetched)
}
