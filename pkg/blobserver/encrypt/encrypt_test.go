/*
Copyright 2013 Google Inc.

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

import (
	"encoding/binary"
	"fmt"
	"io/ioutil"
	"sync"
	"testing"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/sorted"
	"camlistore.org/pkg/test"
)

var testKey = []byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15}

type testStorage struct {
	sto   *storage
	blobs *test.Fetcher
	meta  *test.Fetcher

	mu sync.Mutex // guards iv
	iv uint64
}

// fetchOrErrorString fetches br from sto and returns its body as a string.
// If an error occurs the stringified error is returned, prefixed by "Error: ".
func (ts *testStorage) fetchOrErrorString(br blob.Ref) string {
	rc, _, err := ts.sto.Fetch(br)
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
		index: sorted.NewMemoryKeyValue(),
	}
	if err := sto.setKey(testKey); err != nil {
		panic(err)
	}
	ts := &testStorage{
		sto:   sto,
		blobs: new(test.Fetcher),
		meta:  new(test.Fetcher),
	}
	sto.blobs = ts.blobs
	sto.meta = ts.meta
	sto.testRandIV = func() []byte {
		ts.mu.Lock()
		defer ts.mu.Unlock()
		var ret [16]byte
		ts.iv++
		binary.BigEndian.PutUint64(ret[8:], ts.iv)
		return ret[:]
	}
	return ts
}

func TestEncryptBasic(t *testing.T) {
	ts := newTestStorage()
	const blobData = "foo"
	tb := &test.Blob{blobData}
	tb.MustUpload(t, ts.sto)

	if got := ts.fetchOrErrorString(tb.BlobRef()); got != blobData {
		t.Errorf("Fetching plaintext blobref %v = %v; want %q", tb.BlobRef(), got, blobData)
	}

	if g, w := fmt.Sprintf("%q", ts.meta.BlobrefStrings()), `["sha1-370c753f7158504d11d8941efff4129112f2f975"]`; g != w {
		t.Errorf("meta blobs = %v; want %v", g, w)
	}
	if g, w := fmt.Sprintf("%q", ts.blobs.BlobrefStrings()), `["sha1-64f05b6b313162b01db154fcc7b83238eb36c343"]`; g != w {
		t.Errorf("enc blobs = %v; want %v", g, w)
	}

	// Make sure plainBR doesn't show up anywhere.
	plainBR := tb.BlobRef().String()
	for _, br := range append(ts.meta.BlobrefStrings(), ts.blobs.BlobrefStrings()...) {
		if br == plainBR {
			t.Fatal("plaintext blobref found in storage")
		}
	}
}
