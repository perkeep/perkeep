/*
Copyright 2013 The Camlistore Authors

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

package diskpacked

import (
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/blobserver"
	"camlistore.org/pkg/blobserver/storagetest"
	"camlistore.org/pkg/context"
	"camlistore.org/pkg/test"
)

type blobDetails struct {
	digest string
	data   string // hex-encoded
}

type pack struct {
	blobs []blobDetails
}

// TODO: why is this named pool00001? (--bradfitz)
var pool00001 = []blobDetails{
	{"sha1-04f029feccd2c5c3d3ef87329eb85606bbdd2698", "94"},
	{"sha1-db846319868cf27ecc444bcc34cf126c86bf9a07", "6396"},
	{"sha1-4316a49fc962f627350ca0a01532421b8b93831d", "b782e7a6"},
	{"sha1-74801cba6ffe31238f9995cc759b823aed8bd78c", "eedc50aebfa58de1"},
	{"sha1-bd2a193deeb56aa2554a53eda95d69a95e7bf642", "104c00d6cf9f486f277e8f0493759a21"},
}

func uploadTestBlobs(t *testing.T, s blobserver.Storage, blobs []blobDetails) {
	for _, b := range blobs {
		ref, ok := blob.Parse(b.digest)
		if !ok {
			t.Fatalf("Invalid blob ref: %s", b.digest)
		}
		data, err := hex.DecodeString(b.data)
		if err != nil {
			t.Fatalf("hex.DecodeString(): %v", err)
		}

		_, err = blobserver.Receive(s, ref, bytes.NewBuffer(data))
		if err != nil {
			t.Fatalf("blobserver.Receive(): %v", err)
		}
	}
}

func basename(i int) string {
	return fmt.Sprintf("pack-%05d.blobs", i)
}

func writePack(t *testing.T, dir string, i int, p pack) {
	fd, err := os.Create(filepath.Join(dir, basename(i)))
	if err != nil {
		t.Fatal(err)
	}
	defer fd.Close()

	for _, b := range p.blobs {
		data, err := hex.DecodeString(b.data)
		if err != nil {
			t.Fatal(err)
		}

		_, err = io.WriteString(fd, fmt.Sprintf("[%s %d]", b.digest,
			len(data)))
		if err != nil {
			t.Fatal(err)
		}

		_, err = fd.Write(data)
		if err != nil {
			t.Fatal(err)
		}
	}

}

func newTestStorage(t *testing.T, packs ...pack) (s *storage, clean func()) {
	restoreLogging := test.TLog(t)
	dir, err := ioutil.TempDir("", "diskpacked-test")
	if err != nil {
		t.Fatal(err)
	}

	for i, p := range packs {
		writePack(t, dir, i, p)
	}

	s, err = newStorage(dir, 0, nil)
	if err != nil {
		t.Fatal(err)
	}

	clean = func() {
		s.Close()
		os.RemoveAll(dir)
		restoreLogging()
	}
	return s, clean
}

// It verifies the size and hash of each
// before returning and fails the test if any of the checks fail. It
// also fails the test if StreamBlobs returns a non-nil error.
func streamAll(t *testing.T, s *storage) []*blob.Blob {
	var blobs []*blob.Blob
	ctx := context.New()
	ch := make(chan blobserver.BlobAndToken)
	errCh := make(chan error, 1)

	go func() { errCh <- s.StreamBlobs(ctx, ch, "") }()

	for bt := range ch {
		verifySizeAndHash(t, bt.Blob)
		blobs = append(blobs, bt.Blob)
	}
	if err := <-errCh; err != nil {
		t.Fatalf("StreamBlobs error = %v", err)
	}
	return blobs
}

// Tests the streaming of all blobs in a storage, with hash verification.
func TestBasicStreaming(t *testing.T) {
	s, clean := newTestStorage(t, pack{pool00001})
	defer clean()

	expected := len(pool00001)
	blobs := streamAll(t, s)
	if len(blobs) != expected {
		t.Fatalf("Wrong blob count: Expected %d, got %d", expected,
			len(blobs))
	}
	wantRefs := make([]blob.SizedRef, len(blobs))
	for i, b := range blobs {
		wantRefs[i] = b.SizedRef()
	}
	storagetest.TestStreamer(t, s, storagetest.WantSizedRefs(wantRefs))
}

func verifySizeAndHash(t *testing.T, blob *blob.Blob) {
	hash := sha1.New()
	r := blob.Open()
	n, err := io.Copy(hash, r)
	if err != nil {
		t.Fatal(err)
	}
	r.Close()

	if uint32(n) != blob.Size() {
		t.Fatalf("read %d bytes from blob %v; want %v", n, blob.Ref(), blob.Size())
	}

	if !blob.SizedRef().HashMatches(hash) {
		t.Fatalf("read wrong bytes from blobref %v (digest mismatch)", blob.Ref())
	}
}

// Tests that we can correctly switch over to the next pack if we
// still need to stream more blobs when a pack reaches EOF.
func TestStreamMultiplePacks(t *testing.T) {
	s, clean := newTestStorage(t, pack{pool00001}, pack{pool00001})
	defer clean()
	storagetest.TestStreamer(t, s, storagetest.WantN(len(pool00001)+len(pool00001)))
}

func TestStreamSkipRemovedBlobs(t *testing.T) {
	// Note: This is the only streaming test that makes use of the
	// index (for RemoveBlobs() to succeed). The others do create
	// an indexed storage but they do not use the index to stream
	// (nor should they use it). The streaming in this test is
	// done by reading the underlying diskpacks.
	s, cleanup := newTempDiskpacked(t)
	defer cleanup()

	uploadTestBlobs(t, s, pool00001)

	ref, ok := blob.Parse(pool00001[0].digest)
	if !ok {
		t.Fatalf("blob.Parse: %s", pool00001[0].digest)
	}

	err := s.RemoveBlobs([]blob.Ref{ref})
	if err != nil {
		t.Fatalf("RemoveBlobs: %v", err)
	}

	diskpackedSto := s.(*storage)
	expected := len(pool00001) - 1 // We've deleted 1
	storagetest.TestStreamer(t, diskpackedSto, storagetest.WantN(expected))
}
