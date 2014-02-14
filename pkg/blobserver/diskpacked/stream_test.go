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
	"camlistore.org/pkg/context"
)

type blobDetails struct {
	digest string
	data   string // hex-encoded
}

type pack struct {
	blobs []blobDetails
}

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
	}

	return
}

// Streams all blobs until the total size of the blobs transfered
// equals or exceeds limit (in bytes) or the storage runs out of
// blobs, and returns them. It verifies the size and hash of each
// before returning and fails the test if any of the checks fail. It
// also fails the test if StreamBlobs returns a non-nil error.
func getAllUpToLimit(t *testing.T, s *storage, tok string, limit int64) (blobs []*blob.Blob, contToken string) {
	ctx := context.New()
	ch := make(chan *blob.Blob)
	nextCh := make(chan string, 1)
	errCh := make(chan error, 1)

	go func() {
		next, err := s.StreamBlobs(ctx, ch, tok, limit)

		nextCh <- next
		errCh <- err
	}()

	blobs = make([]*blob.Blob, 0, 32)
	for blob := range ch {
		verifySizeAndHash(t, blob)
		blobs = append(blobs, blob)
	}

	contToken = <-nextCh

	if err := <-errCh; err != nil {
		t.Fatal(err)
	}

	return

}

// Tests the streaming of all blobs in a storage, with hash verification.
func TestBasicStreaming(t *testing.T) {
	s, clean := newTestStorage(t, pack{pool00001})
	defer clean()

	limit := int64(999999)
	expected := len(pool00001)
	blobs, next := getAllUpToLimit(t, s, "", limit)

	if len(blobs) != expected {
		t.Fatalf("Wrong blob count: Expected %d, got %d", expected,
			len(blobs))
	}

	if next != "" {
		t.Fatalf("Expected empty continuation token, got: %s", next)
	}

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
		t.Fatalf("Wrong blob size. Expected %d, got %d",
			blob.Size(), n)
	}

	if !blob.SizedRef().HashMatches(hash) {
		t.Fatal("Blob has wrong digest")
	}
}

// Tests that StreamBlobs respects the byte limit (limitBytes)
func TestLimitBytes(t *testing.T) {
	s, clean := newTestStorage(t, pack{pool00001})
	defer clean()

	limit := int64(1) // This should cause us to get only the 1st blob.
	expected := 1
	blobs, next := getAllUpToLimit(t, s, "", limit)

	if len(blobs) != expected {
		t.Fatalf("Wrong blob count: Expected %d, got %d", expected,
			len(blobs))
	}

	// For pool00001, the header + data of the first blob is has len 50
	expectedContToken := "0 50"
	if next != expectedContToken {
		t.Fatalf("Unexpected continuation token. Expected \"%s\", got \"%s\"", expectedContToken, next)
	}
}

func TestSeekToContToken(t *testing.T) {
	s, clean := newTestStorage(t, pack{pool00001})
	defer clean()

	limit := int64(999999)
	expected := len(pool00001) - 1
	blobs, next := getAllUpToLimit(t, s, "0 50", limit)

	if len(blobs) != expected {
		t.Fatalf("Wrong blob count: Expected %d, got %d", expected,
			len(blobs))
	}

	if next != "" {
		t.Fatalf("Unexpected continuation token. Expected \"%s\", got \"%s\"", "", next)
	}
}

// Tests that we can correctly switch over to the next pack if we
// still need to stream more blobs when a pack reaches EOF.
func TestStreamMultiplePacks(t *testing.T) {
	s, clean := newTestStorage(t, pack{pool00001}, pack{pool00001})
	defer clean()

	limit := int64(999999)
	expected := 2 * len(pool00001)
	blobs, _ := getAllUpToLimit(t, s, "", limit)

	if len(blobs) != expected {
		t.Fatalf("Wrong blob count: Expected %d, got %d", expected,
			len(blobs))
	}
}

func TestSkipRemovedBlobs(t *testing.T) {
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
		t.Fatalf("blobserver.Storage.RemoveBlobs(): %v", err)
	}

	diskpackedSto := s.(*storage)

	limit := int64(999999)
	expected := len(pool00001) - 1 // We've deleted 1
	blobs, _ := getAllUpToLimit(t, diskpackedSto, "", limit)

	if len(blobs) != expected {
		t.Fatalf("Wrong blob count: Expected %d, got %d", expected,
			len(blobs))
	}

}
