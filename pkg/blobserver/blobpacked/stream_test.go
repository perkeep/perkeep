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

package blobpacked

import (
	"bytes"
	"reflect"
	"strconv"
	"testing"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/blobserver"
	"camlistore.org/pkg/blobserver/storagetest"
	"camlistore.org/pkg/schema"
	"camlistore.org/pkg/sorted"
	"camlistore.org/pkg/test"
	"golang.org/x/net/context"
)

func TestStreamBlobs(t *testing.T) {
	small := new(test.Fetcher)
	s := &storage{
		small: small,
		large: new(test.Fetcher),
		meta:  sorted.NewMemoryKeyValue(),
		log:   test.NewLogger(t, "blobpacked: "),
	}
	s.init()

	all := map[blob.Ref]bool{}
	const nBlobs = 10
	for i := 0; i < nBlobs; i++ {
		b := &test.Blob{strconv.Itoa(i)}
		b.MustUpload(t, small)
		all[b.BlobRef()] = true
	}
	ctx, cancel := context.WithCancel(context.TODO())
	defer cancel()
	token := "" // beginning

	got := map[blob.Ref]bool{}
	dest := make(chan blobserver.BlobAndToken, 16)
	done := make(chan bool)
	go func() {
		defer close(done)
		for bt := range dest {
			got[bt.Blob.Ref()] = true
		}
	}()
	err := s.StreamBlobs(ctx, dest, token)
	if err != nil {
		t.Fatalf("StreamBlobs = %v", err)
	}
	<-done
	if !reflect.DeepEqual(got, all) {
		t.Errorf("Got blobs %v; want %v", got, all)
	}
	storagetest.TestStreamer(t, s, storagetest.WantN(nBlobs))
}

type subfetcherStorage interface {
	blobserver.Storage
	blob.SubFetcher
}

func enumStore() subfetcherStorage {
	// Hide the BlobStreamer (and any other) interface impl beyond
	// just the blobserver.Storage base (which has Enumerator, but not Streamer)
	return struct{ subfetcherStorage }{new(test.Fetcher)}
}

type streamerStorage interface {
	subfetcherStorage
	blobserver.BlobStreamer
}

func streamStore() streamerStorage {
	return new(test.Fetcher)
}

func TestStreamBlobs_Loose_Enumerate(t *testing.T) {
	testStreamBlobs(t, enumStore(), enumStore() /* unused */, populateLoose)
}

func TestStreamBlobs_Loose_Streamed(t *testing.T) {
	testStreamBlobs(t, streamStore(), enumStore() /* unused */, populateLoose)
}

func TestStreamBlobs_Packed_Enumerate(t *testing.T) {
	testStreamBlobs(t, enumStore(), enumStore(), populatePacked)
}

func TestStreamBlobs_Packed_Streamed(t *testing.T) {
	testStreamBlobs(t, streamStore(), streamStore(), populatePacked)
}

// 2 packed files
func TestStreamBlobs_Packed2_Streamed(t *testing.T) {
	testStreamBlobs(t, streamStore(), streamStore(), populatePacked2)
}

func testStreamBlobs(t *testing.T,
	small blobserver.Storage,
	large subFetcherStorage,
	populate func(*testing.T, *storage) []storagetest.StreamerTestOpt) {
	s := &storage{
		small: small,
		large: large,
		meta:  sorted.NewMemoryKeyValue(),
		log:   test.NewLogger(t, "blobpacked: "),
	}
	s.init()
	wants := populate(t, s)
	storagetest.TestStreamer(t, s, wants...)
}

func populateLoose(t *testing.T, s *storage) (wants []storagetest.StreamerTestOpt) {
	const nBlobs = 10
	for i := 0; i < nBlobs; i++ {
		(&test.Blob{strconv.Itoa(i)}).MustUpload(t, s)
	}
	return append(wants, storagetest.WantN(nBlobs))
}

func populatePacked(t *testing.T, s *storage) (wants []storagetest.StreamerTestOpt) {
	const fileSize = 5 << 20
	const fileName = "foo.dat"
	fileContents := randBytes(fileSize)
	_, err := schema.WriteFileFromReader(s, fileName, bytes.NewReader(fileContents))
	if err != nil {
		t.Fatalf("WriteFileFromReader: %v", err)
	}
	return nil
}

func populatePacked2(t *testing.T, s *storage) (wants []storagetest.StreamerTestOpt) {
	const fileSize = 1 << 20
	data := randBytes(fileSize)
	_, err := schema.WriteFileFromReader(s, "first-half.dat", bytes.NewReader(data[:fileSize/2]))
	if err != nil {
		t.Fatalf("WriteFileFromReader: %v", err)
	}
	_, err = schema.WriteFileFromReader(s, "second-half.dat", bytes.NewReader(data[fileSize/2:]))
	if err != nil {
		t.Fatalf("WriteFileFromReader: %v", err)
	}
	return nil
}
