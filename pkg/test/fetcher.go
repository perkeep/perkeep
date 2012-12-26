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
	"strings"
	"sync"
	"time"

	"camlistore.org/pkg/blobref"
)

type Fetcher struct {
	l sync.Mutex
	m map[string]*Blob
}

func (tf *Fetcher) AddBlob(b *Blob) {
	tf.l.Lock()
	defer tf.l.Unlock()
	if tf.m == nil {
		tf.m = make(map[string]*Blob)
	}
	tf.m[b.BlobRef().String()] = b
}

func (tf *Fetcher) FetchStreaming(ref *blobref.BlobRef) (file io.ReadCloser, size int64, err error) {
	return tf.Fetch(ref)
}

var dummyCloser = ioutil.NopCloser(nil)

func (tf *Fetcher) Fetch(ref *blobref.BlobRef) (file blobref.ReadSeekCloser, size int64, err error) {
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
