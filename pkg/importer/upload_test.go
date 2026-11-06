/*
Copyright 2026 The Perkeep Authors

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

package importer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"testing"
	"testing/synctest"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver"
	"perkeep.org/pkg/blobserver/memory"
)

// batchStorage is memory storage that records the batches of blobs it
// receives, and blocks receiving them until release is closed, failing
// those of fail.
type batchStorage struct {
	*memory.Storage
	release chan struct{}
	fail    blob.Ref

	mu      sync.Mutex
	batches []int // sizes of the batches received
}

func (s *batchStorage) ReceiveBlobs(ctx context.Context, blobs []*blob.Blob) error {
	<-s.release
	s.mu.Lock()
	s.batches = append(s.batches, len(blobs))
	s.mu.Unlock()
	be := &blobserver.BlobsError{Op: "receive"}
	for _, b := range blobs {
		if b.Ref() == s.fail {
			be.Add(b.Ref(), errors.New("failed"))
			continue
		}
		r, err := b.ReadAll(ctx)
		if err != nil {
			return err
		}
		if _, err := s.Storage.ReceiveBlob(ctx, b.Ref(), r); err != nil {
			return err
		}
	}
	return be.OrNil()
}

func testBlob(s string) *blob.Blob {
	return blob.NewBlob(blob.RefFromString(s), uint32(len(s)), func(context.Context) ([]byte, error) { return []byte(s), nil })
}

// TestUploadQueueBatches tests that blobs uploaded while others are being
// stored are stored together, after them, and that each upload gets its
// own blob's result.
func TestUploadQueueBatches(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		dst := &batchStorage{Storage: new(memory.Storage), release: make(chan struct{})}
		var q uploadQueue
		errs := make(chan error, 11)
		upload := func(b *blob.Blob) { errs <- q.upload(context.Background(), dst, b) }

		go upload(testBlob("first"))
		synctest.Wait() // the first is being stored, blocked
		dst.fail = blob.RefFromString("blob 3")
		for i := range 10 {
			go upload(testBlob(fmt.Sprintf("blob %d", i)))
		}
		synctest.Wait() // the others are waiting
		close(dst.release)
		var failed int
		for range 11 {
			if err := <-errs; err != nil {
				failed++
			}
		}
		if fmt.Sprint(dst.batches) != "[1 10]" {
			t.Errorf("batches = %v; want [1 10]", dst.batches)
		}
		if failed != 1 {
			t.Errorf("%d uploads failed; want 1", failed)
		}
		rc, _, err := dst.Fetch(context.Background(), blob.RefFromString("blob 7"))
		if err != nil {
			t.Fatal(err)
		}
		got, _ := io.ReadAll(rc)
		rc.Close()
		if string(got) != "blob 7" {
			t.Errorf("stored %q", got)
		}
	})
}

// TestUploadQueueCanceled tests that an upload whose context is done stops
// waiting.
func TestUploadQueueCanceled(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		dst := &batchStorage{Storage: new(memory.Storage), release: make(chan struct{})}
		var q uploadQueue
		ctx, cancel := context.WithCancel(context.Background())
		errc := make(chan error, 1)
		go func() { errc <- q.upload(ctx, dst, testBlob("slow")) }()
		synctest.Wait()
		cancel()
		if err := <-errc; !errors.Is(err, context.Canceled) {
			t.Errorf("upload = %v; want context.Canceled", err)
		}
		close(dst.release)
		synctest.Wait()
	})
}
