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
	"time"

	"camlistore.org/pkg/blobref"
	"camlistore.org/pkg/blobserver"
	"camlistore.org/pkg/types"
)

// Fetcher is an in-memory implementation of the blobserver Storage
// interface.  It started as just a fetcher and grew. It also includes
// other convenience methods for testing.
type Fetcher struct {
	blobserver.SimpleBlobHubPartitionMap
	l      sync.Mutex
	m      map[string]*Blob // keyed by blobref string
	sorted []string         // blobrefs sorted
}

var _ blobserver.Storage = (*Fetcher)(nil)

func (tf *Fetcher) AddBlob(b *Blob) {
	tf.l.Lock()
	defer tf.l.Unlock()
	if tf.m == nil {
		tf.m = make(map[string]*Blob)
	}
	key := b.BlobRef().String()
	tf.m[key] = b
	tf.sorted = append(tf.sorted, key)
	sort.Strings(tf.sorted)
}

func (tf *Fetcher) FetchStreaming(ref *blobref.BlobRef) (file io.ReadCloser, size int64, err error) {
	return tf.Fetch(ref)
}

var dummyCloser = ioutil.NopCloser(nil)

func (tf *Fetcher) Fetch(ref *blobref.BlobRef) (file types.ReadSeekCloser, size int64, err error) {
	tf.l.Lock()
	defer tf.l.Unlock()
	if tf.m == nil {
		err = os.ErrNotExist
		return
	}
	tb, ok := tf.m[ref.String()]
	if !ok {
		err = os.ErrNotExist
		return
	}
	size = int64(len(tb.Contents))
	return struct {
		*io.SectionReader
		io.Closer
	}{
		io.NewSectionReader(strings.NewReader(tb.Contents), 0, size),
		dummyCloser,
	}, size, nil
}

func (tf *Fetcher) BlobContents(br *blobref.BlobRef) (contents string, ok bool) {
	tf.l.Lock()
	defer tf.l.Unlock()
	b, ok := tf.m[br.String()]
	if !ok {
		return
	}
	return b.Contents, true
}

func (tf *Fetcher) ReceiveBlob(br *blobref.BlobRef, source io.Reader) (blobref.SizedBlobRef, error) {
	sb := blobref.SizedBlobRef{}
	h := br.Hash()
	if h == nil {
		return sb, fmt.Errorf("Unsupported blobref hash for %s", br)
	}
	all, err := ioutil.ReadAll(io.TeeReader(source, h))
	if err != nil {
		return sb, err
	}
	if !br.HashMatches(h) {
		return sb, fmt.Errorf("Hash mismatch receiving blob %s", br)
	}
	blob := &Blob{Contents: string(all)}
	tf.AddBlob(blob)
	return blobref.SizedBlobRef{br, int64(len(all))}, nil
}

func (tf *Fetcher) StatBlobs(dest chan<- blobref.SizedBlobRef, blobs []*blobref.BlobRef, wait time.Duration) error {
	if wait != 0 {
		panic("non-zero Wait on test.Fetcher.StatBlobs not supported")
	}
	for _, br := range blobs {
		tf.l.Lock()
		b, ok := tf.m[br.String()]
		tf.l.Unlock()
		if ok {
			dest <- blobref.SizedBlobRef{br, int64(len(b.Contents))}
		}
	}
	return nil
}

// BlobrefStrings returns the sorted stringified blobrefs stored in this fetcher.
func (tf *Fetcher) BlobrefStrings() []string {
	tf.l.Lock()
	defer tf.l.Unlock()
	s := make([]string, len(tf.sorted))
	copy(s, tf.sorted)
	return s
}

func (tf *Fetcher) EnumerateBlobs(dest chan<- blobref.SizedBlobRef,
	after string,
	limit int,
	wait time.Duration) error {
	if wait != 0 {
		panic("TestFetcher can't wait")
	}
	defer close(dest)
	tf.l.Lock()
	defer tf.l.Unlock()
	n := 0
	for _, k := range tf.sorted {
		if k <= after {
			continue
		}
		b := tf.m[k]
		dest <- blobref.SizedBlobRef{b.BlobRef(), b.Size()}
		n++
		if limit > 0 && n == limit {
			break
		}
	}
	return nil
}

func (tf *Fetcher) RemoveBlobs(blobs []*blobref.BlobRef) error {
	tf.l.Lock()
	defer tf.l.Unlock()
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
