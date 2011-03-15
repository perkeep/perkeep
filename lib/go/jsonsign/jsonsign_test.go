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

package jsonsign

import (
	"camli/blobref"
	. "camli/testing"

	"os"
	"sync"
	"testing"
)

var unsigned = `{"camliVersion": 1,
"camliType": "foo"
}`

type TestFetcher struct {
	l  sync.Mutex
	m  map[string]*blobref.TestBlob
}

func (tf *TestFetcher) AddBlob(b *blobref.TestBlob) {
	tf.l.Lock()
	defer tf.l.Unlock()
	if tf.m == nil {
		tf.m = make(map[string]*blobref.TestBlob)
	}
	tf.m[b.BlobRef().String()] = b
}

func (tf *TestFetcher) Fetch(ref *blobref.BlobRef) (file blobref.ReadSeekCloser, size int64, err os.Error) {
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
	file = &strReader{tb.Val, 0}
	size = int64(len(tb.Val))
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
	remain := len(sr.s) - sr.pos 
	if remain <= 0 {
		err = os.EOF
		return
	}
	toCopy := len(p)
	if remain < toCopy {
		toCopy = remain
	}
	copy(p, sr.s[sr.pos:sr.pos+toCopy])
	return
}

var testFetcher = &TestFetcher{}

func TestSigningBadInput(t *testing.T) {
	sr := newRequest()

	sr.UnsignedJson = ""
	_, err := sr.Sign()
	ExpectErrorContains(t, err, "json parse error", "empty input")

	sr.UnsignedJson = "{}"
	_, err = sr.Sign()
	ExpectErrorContains(t, err, "json lacks \"camliSigner\" key", "just braces")

	sr.UnsignedJson = `{"camliSigner": 123}`
	_, err = sr.Sign()
	ExpectErrorContains(t, err, "\"camliSigner\" key is malformed or unsupported", "camliSigner 123")

	sr.UnsignedJson = `{"camliSigner": ""}`
	_, err = sr.Sign()
	ExpectErrorContains(t, err, "\"camliSigner\" key is malformed or unsupported", "empty camliSigner")
}

func newRequest() *SignRequest {
	return &SignRequest{
	UnsignedJson: "",
        Fetcher: testFetcher,
	UseAgent: false,
	ServerMode: true,
	}
}

func TestSigning(t *testing.T) {
	sr := newRequest()
	// TODO: finish test
	got, err := sr.Sign()
	if err != nil {
		//t.Logf("Error signing: %v", err)
	}
	t.Logf("TODO; finish these tests; got: %s", got)
}
