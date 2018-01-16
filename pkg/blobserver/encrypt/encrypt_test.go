/*
Copyright 2013 The Perkeep Authors

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

package encrypt

/*
Dev notes:

$ devcam put --path=/enc/ blob dev-camput
sha1-282c0feceeb5cdf4c5086c191b15356fadfb2392
$ devcam get --path=/enc/ sha1-282c0feceeb5cdf4c5086c191b15356fadfb2392
$ find /tmp/camliroot-$USER/port3179/encblob/
$ ./dev-camtool sync --src=http://localhost:3179/enc/ --dest=stdout
*/

import (
	"context"
	"encoding/binary"
	"fmt"
	"io/ioutil"
	"strings"
	"sync"
	"testing"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver"
	"perkeep.org/pkg/blobserver/storagetest"
	"perkeep.org/pkg/sorted"
	"perkeep.org/pkg/test"
)

var ctxbg = context.Background()

func TestSetPassphrase(t *testing.T) {
	scryptN = 1 << 10
	s := storage{}
	if s.key != [32]byte{} {
		t.Fail()
	}
	s.setPassphrase([]byte("foo"))
	fooPass := s.key
	if fooPass == [32]byte{} {
		t.Fail()
	}
	s.setPassphrase([]byte("bar"))
	if fooPass == s.key {
		t.Fail()
	}
}

var testPass = []byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15}

type testStorage struct {
	sto   *storage
	blobs *test.Fetcher
	meta  *test.Fetcher

	mu sync.Mutex
	iv uint64
}

// fetchOrErrorString fetches br from sto and returns its body as a string.
// If an error occurs the stringified error is returned, prefixed by "Error: ".
func (ts *testStorage) fetchOrErrorString(br blob.Ref) string {
	rc, _, err := ts.sto.Fetch(ctxbg, br)
	var slurp []byte
	if err == nil {
		defer rc.Close()
		slurp, err = ioutil.ReadAll(rc)
	}
	if err != nil {
		return fmt.Sprintf("Error: %v", err)
	}
	return string(slurp)
}

func newTestStorage() *testStorage {
	sto := &storage{
		index:     sorted.NewMemoryKeyValue(),
		smallMeta: &metaBlobHeap{},
	}
	scryptN = 1 << 10
	sto.setPassphrase(testPass)
	ts := &testStorage{
		sto:   sto,
		blobs: new(test.Fetcher),
		meta:  new(test.Fetcher),
	}
	sto.blobs = ts.blobs
	sto.meta = ts.meta
	sto.testRand = func(b []byte) (int, error) {
		ts.mu.Lock()
		defer ts.mu.Unlock()
		ts.iv++
		binary.BigEndian.PutUint64(b, ts.iv)
		return len(b), nil
	}
	return ts
}

func TestStorage(t *testing.T) {
	storagetest.TestOpt(t, storagetest.Opts{
		New: func(t *testing.T) (sto blobserver.Storage, cleanup func()) {
			return newTestStorage().sto, func() {}
		},
	})
}

func TestBadPass(t *testing.T) {
	ts := newTestStorage()
	mustPanic(t, "tried to set empty passphrase", func() { ts.sto.setPassphrase([]byte("")) })

	for i := range ts.sto.key {
		ts.sto.key[i] = 0
	}
	tb := &test.Blob{"foo"}
	mustPanic(t, "no passphrase set", func() { tb.MustUpload(t, ts.sto) })
}

func TestEncrypt(t *testing.T) {
	ts := newTestStorage()

	const blobData = "foofoofoo"
	tb := &test.Blob{blobData}
	tb.MustUpload(t, ts.sto)
	if got := ts.fetchOrErrorString(tb.BlobRef()); got != blobData {
		t.Errorf("Fetching plaintext blobref %v = %v; want %q", tb.BlobRef(), got, blobData)
	}

	// Make sure the plaintext doesn't show up anywhere.
	for _, bs := range []*test.Fetcher{ts.meta, ts.blobs} {
		c := make(chan blob.SizedRef)
		go bs.EnumerateBlobs(context.TODO(), c, "", 0)
		for sb := range c {
			data, ok := bs.BlobContents(sb.Ref)
			if !ok {
				panic("where did it go?")
			}
			if strings.Contains(data, blobData) {
				t.Error("plaintext found in storage")
			}
		}
	}

	const blobData2 = "bar"
	tb2 := &test.Blob{blobData2}
	tb2.MustUpload(t, ts.sto)
	if got := ts.fetchOrErrorString(tb2.BlobRef()); got != blobData2 {
		t.Errorf("Fetching plaintext blobref %v = %v; want %q", tb2.BlobRef(), got, blobData2)
	}

	missingError := "Error: file does not exist"
	tb3 := &test.Blob{"xxx"}
	if got := ts.fetchOrErrorString(tb3.BlobRef()); got != missingError {
		t.Errorf("Fetching missing blobref %v; want %q", got, missingError)
	}

	ctx := context.Background()
	got, err := blobserver.StatBlobs(ctx, ts.sto, []blob.Ref{tb3.BlobRef(), tb.BlobRef(), tb2.BlobRef()})
	if err != nil {
		t.Fatalf("StatBlobs: %v", err)
	}
	if sr := got[tb.BlobRef()]; sr != tb.SizedRef() {
		t.Errorf("tb stat = %v; want %v", sr, tb.SizedRef())
	}
	if sr := got[tb2.BlobRef()]; sr != tb2.SizedRef() {
		t.Errorf("tb2 stat = %v; want %v", sr, tb2.SizedRef())
	}
	if sr, ok := got[tb3.BlobRef()]; ok {
		t.Errorf("unexpected stat response for tb3: %v", sr)
	}
	if len(got) != 2 {
		t.Errorf("unexpected blobs in stat response")
	}

	c := make(chan blob.SizedRef)
	go func() {
		if err := ts.sto.EnumerateBlobs(context.TODO(), c, "", 0); err != nil {
			t.Fatal(err)
		}
	}()
	if sr := <-c; sr != tb2.SizedRef() {
		t.Errorf("%s != %s", sr, tb2.SizedRef())
	}
	if sr := <-c; sr != tb.SizedRef() {
		t.Errorf("%s != %s", sr, tb.SizedRef())
	}
	if _, ok := <-c; ok {
		t.Error("did not close the channel")
	}
}

func TestLoadMeta(t *testing.T) {
	ts := newTestStorage()
	const blobData = "foo"
	tb := &test.Blob{blobData}
	tb.MustUpload(t, ts.sto)
	const blobData2 = "bar"
	tb2 := &test.Blob{blobData2}
	tb2.MustUpload(t, ts.sto)
	meta, blobs := ts.meta, ts.blobs

	ts = newTestStorage()
	ts.meta, ts.blobs = meta, blobs
	ts.sto.meta, ts.sto.blobs = meta, blobs
	if err := ts.sto.readAllMetaBlobs(); err != nil {
		t.Fatal(err)
	}
	if got := ts.fetchOrErrorString(tb.BlobRef()); got != blobData {
		t.Errorf("Fetching plaintext blobref %v = %v; want %q", tb.BlobRef(), got, blobData)
	}
	if got := ts.fetchOrErrorString(tb2.BlobRef()); got != blobData2 {
		t.Errorf("Fetching plaintext blobref %v = %v; want %q", tb2.BlobRef(), got, blobData2)
	}
}

func mustPanic(t *testing.T, msg string, f func()) {
	defer func() {
		err := recover()
		if err == nil {
			t.Errorf("function did not panic, wanted %q", msg)
		} else if err != msg {
			t.Errorf("got panic %v, wanted %q", err, msg)
		}
	}()
	f()
}
