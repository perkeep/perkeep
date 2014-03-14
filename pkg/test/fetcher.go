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

package test

import (
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"sort"
	"strings"
	"sync"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/blobserver"
	"camlistore.org/pkg/context"
	"camlistore.org/pkg/types"
)

// Fetcher is an in-memory implementation of the blobserver Storage
// interface.  It started as just a fetcher and grew. It also includes
// other convenience methods for testing.
type Fetcher struct {
	mu     sync.RWMutex
	m      map[string]*Blob // keyed by blobref string
	sorted []string         // blobrefs sorted

	// ReceiveErr optionally returns the error to return on receive.
	ReceiveErr error

	// FetchErr, if non-nil, specifies the error to return on the next fetch call.
	// If it returns nil, fetches proceed as normal.
	FetchErr func() error
}

var _ blobserver.Storage = (*Fetcher)(nil)

func (tf *Fetcher) AddBlob(b *Blob) {
	tf.mu.Lock()
	defer tf.mu.Unlock()
	if tf.m == nil {
		tf.m = make(map[string]*Blob)
	}
	key := b.BlobRef().String()
	_, had := tf.m[key]
	tf.m[key] = b
	if !had {
		tf.sorted = append(tf.sorted, key)
		sort.Strings(tf.sorted)
	}
}

func (tf *Fetcher) Fetch(ref blob.Ref) (file io.ReadCloser, size uint32, err error) {
	if tf.FetchErr != nil {
		if err = tf.FetchErr(); err != nil {
			return
		}
	}
	tf.mu.RLock()
	defer tf.mu.RUnlock()
	if tf.m == nil {
		err = os.ErrNotExist
		return
	}
	tb, ok := tf.m[ref.String()]
	if !ok {
		err = os.ErrNotExist
		return
	}
	size = uint32(len(tb.Contents))
	return struct {
		*io.SectionReader
		io.Closer
	}{
		io.NewSectionReader(strings.NewReader(tb.Contents), 0, int64(size)),
		types.NopCloser,
	}, size, nil
}

func (tf *Fetcher) BlobContents(br blob.Ref) (contents string, ok bool) {
	tf.mu.RLock()
	defer tf.mu.RUnlock()
	b, ok := tf.m[br.String()]
	if !ok {
		return
	}
	return b.Contents, true
}

func (tf *Fetcher) ReceiveBlob(br blob.Ref, source io.Reader) (blob.SizedRef, error) {
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
	if err := tf.ReceiveErr; err != nil {
		return sb, err
	}
	b := &Blob{Contents: string(all)}
	tf.AddBlob(b)
	return blob.SizedRef{br, uint32(len(all))}, nil
}

func (tf *Fetcher) StatBlobs(dest chan<- blob.SizedRef, blobs []blob.Ref) error {
	for _, br := range blobs {
		tf.mu.RLock()
		b, ok := tf.m[br.String()]
		tf.mu.RUnlock()
		if ok {
			dest <- blob.SizedRef{br, uint32(len(b.Contents))}
		}
	}
	return nil
}

func (tf *Fetcher) NumBlobs() int {
	tf.mu.RLock()
	defer tf.mu.RUnlock()
	return len(tf.m)
}

// BlobrefStrings returns the sorted stringified blobrefs stored in this fetcher.
func (tf *Fetcher) BlobrefStrings() []string {
	tf.mu.RLock()
	defer tf.mu.RUnlock()
	s := make([]string, len(tf.sorted))
	copy(s, tf.sorted)
	return s
}

func (tf *Fetcher) EnumerateBlobs(ctx *context.Context, dest chan<- blob.SizedRef, after string, limit int) error {
	defer close(dest)
	tf.mu.RLock()
	defer tf.mu.RUnlock()
	n := 0
	for _, k := range tf.sorted {
		if k <= after {
			continue
		}
		b := tf.m[k]
		select {
		case dest <- blob.SizedRef{b.BlobRef(), uint32(b.Size())}:
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

func (tf *Fetcher) RemoveBlobs(blobs []blob.Ref) error {
	tf.mu.Lock()
	defer tf.mu.Unlock()
	for _, br := range blobs {
		delete(tf.m, br.String())
	}
	tf.sorted = tf.sorted[:0]
	for k := range tf.m {
		tf.sorted = append(tf.sorted, k)
	}
	sort.Strings(tf.sorted)
	return nil
}
