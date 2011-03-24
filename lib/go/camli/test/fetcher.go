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
	"camli/blobref"
	"os"
	"sync"
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

func (tf *Fetcher) Fetch(ref *blobref.BlobRef) (file blobref.ReadSeekCloser, size int64, err os.Error) {
	tf.l.Lock()
	defer tf.l.Unlock()
	if tf.m == nil {
		err = os.ENOENT
		return
	}
	tb, ok := tf.m[ref.String()]
	if !ok {
		err = os.ENOENT
		return
	}
	file = &strReader{tb.Contents, 0}
	size = int64(len(tb.Contents))
	return
}

type strReader struct {
	s   string
	pos int
}

func (sr *strReader) Close() os.Error { return nil }

func (sr *strReader) Seek(offset int64, whence int) (ret int64, err os.Error) {
	// Note: ignoring 64-bit offsets.  test data should be tiny.
	switch whence {
	case 0:
		sr.pos = int(offset)
	case 1:
		sr.pos += int(offset)
	case 2:
		sr.pos = len(sr.s) + int(offset)
	}
	ret = int64(sr.pos)
	return
}

func (sr *strReader) Read(p []byte) (n int, err os.Error) {
	if sr.pos >= len(sr.s) {
		err = os.EOF
		return
	}
	n = copy(p, sr.s[sr.pos:])
	if n == 0 {
		err = os.EOF
	}
	sr.pos += n
	return
}
