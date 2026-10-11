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

package client

import (
	"context"
	"io"
	"testing"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver/memory"
	"perkeep.org/pkg/test"
)

// countingStorage is a memory.Storage that counts receives and stats.
type countingStorage struct {
	memory.Storage
	receives, stats int
}

func (s *countingStorage) ReceiveBlob(ctx context.Context, br blob.Ref, source io.Reader) (blob.SizedRef, error) {
	s.receives++
	return s.Storage.ReceiveBlob(ctx, br, source)
}

func (s *countingStorage) StatBlobs(ctx context.Context, blobs []blob.Ref, fn func(blob.SizedRef) error) error {
	s.stats++
	return s.Storage.StatBlobs(ctx, blobs, fn)
}

func newStorageClient(t *testing.T) (*Client, *countingStorage) {
	t.Helper()
	sto := new(countingStorage)
	c, err := New(OptionUseStorageClient(sto))
	if err != nil {
		t.Fatal(err)
	}
	return c, sto
}

func TestUploadStorageClientStats(t *testing.T) {
	ctx := context.Background()
	c, sto := newStorageClient(t)
	tb := &test.Blob{Contents: "foo"}

	upload := func(skipStat bool) *PutResult {
		t.Helper()
		pr, err := c.Upload(ctx, &UploadHandle{
			BlobRef:  tb.BlobRef(),
			Contents: tb.Reader(),
			SkipStat: skipStat,
		})
		if err != nil {
			t.Fatal(err)
		}
		if pr.BlobRef != tb.BlobRef() || pr.Size != tb.Size() {
			t.Fatalf("PutResult = %v, %d; want %v, %d", pr.BlobRef, pr.Size, tb.BlobRef(), tb.Size())
		}
		return pr
	}

	if pr := upload(false); pr.Skipped {
		t.Error("first upload was skipped")
	}
	if sto.receives != 1 {
		t.Errorf("after first upload, receives = %d; want 1", sto.receives)
	}

	if pr := upload(false); !pr.Skipped {
		t.Error("second upload wasn't skipped")
	}
	if sto.receives != 1 {
		t.Errorf("after second upload, receives = %d; want 1", sto.receives)
	}

	stats := sto.stats
	upload(true)
	if sto.receives != 2 {
		t.Errorf("after SkipStat upload, receives = %d; want 2", sto.receives)
	}
	if sto.stats != stats {
		t.Errorf("SkipStat upload did %d stats; want 0", sto.stats-stats)
	}
}

// setTestPublicKey makes c's signer public key be tb, without the
// usual configuration.
func setTestPublicKey(c *Client, tb *test.Blob) {
	c.initSignerPublicKeyBlobrefOnce.Do(func() {})
	c.signerPublicKeyRef = tb.BlobRef()
	c.publicKeyArmored = tb.Contents
}

func TestUploadPublicKey(t *testing.T) {
	ctx := context.Background()
	key := &test.Blob{Contents: "pretend public key"}

	t.Run("new", func(t *testing.T) {
		c, sto := newStorageClient(t)
		setTestPublicKey(c, key)
		for range 3 {
			if err := c.UploadPublicKey(ctx); err != nil {
				t.Fatal(err)
			}
		}
		if sto.receives != 1 {
			t.Errorf("receives = %d; want 1", sto.receives)
		}
		if sto.stats != 1 {
			t.Errorf("stats = %d; want 1", sto.stats)
		}
	})

	t.Run("existing", func(t *testing.T) {
		c, sto := newStorageClient(t)
		setTestPublicKey(c, key)
		key.MustUpload(t, &sto.Storage)
		for range 3 {
			if err := c.UploadPublicKey(ctx); err != nil {
				t.Fatal(err)
			}
		}
		if sto.receives != 0 {
			t.Errorf("receives = %d; want 0", sto.receives)
		}
		if sto.stats != 1 {
			t.Errorf("stats = %d; want 1", sto.stats)
		}
	})
}
