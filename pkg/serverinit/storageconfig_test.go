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

package serverinit

import (
	"context"
	"testing"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver"
	"perkeep.org/pkg/test"
)

// blobsRecorder is a BlobsReceiver that counts its ReceiveBlobs calls.
type blobsRecorder struct {
	*test.Fetcher
	calls int
}

func (r *blobsRecorder) ReceiveBlobs(ctx context.Context, blobs []*blob.Blob) error {
	r.calls++
	return blobserver.ReceiveBlobsUnchecked(ctx, r.Fetcher, blobs)
}

// TestStorageAndConfigReceiveBlobs checks that storageAndConfig passes
// ReceiveBlobs through to the storage it wraps.
func TestStorageAndConfigReceiveBlobs(t *testing.T) {
	rec := &blobsRecorder{Fetcher: new(test.Fetcher)}
	sto := &storageAndConfig{Storage: rec}
	blobs := []*blob.Blob{(&test.Blob{Contents: "a"}).Blob(), (&test.Blob{Contents: "b"}).Blob()}
	if err := blobserver.ReceiveBlobs(context.Background(), sto, blobs); err != nil {
		t.Fatal(err)
	}
	if rec.calls != 1 {
		t.Errorf("wrapped ReceiveBlobs called %d times; want 1", rec.calls)
	}
	if rec.NumBlobs() != 2 {
		t.Errorf("stored %d blobs; want 2", rec.NumBlobs())
	}
}
