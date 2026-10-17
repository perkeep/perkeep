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
	"strings"
	"testing"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver/memory"
	"perkeep.org/pkg/constants"
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

// TestUploadStats checks that Upload doesn't ask the server whether it
// has a blob, and that UploadIfMissing does, uploading only if not.
func TestUploadStats(t *testing.T) {
	ctx := context.Background()
	c, sto := newStorageClient(t)
	foo, bar := &test.Blob{Contents: "foo"}, &test.Blob{Contents: "bar"}
	handle := func(tb *test.Blob) *UploadHandle {
		return &UploadHandle{BlobRef: tb.BlobRef(), Size: tb.Size(), Contents: strings.NewReader(tb.Contents)}
	}
	check := func(what string, pr *PutResult, err error, tb *test.Blob, skipped bool, receives, stats int) {
		t.Helper()
		if err != nil {
			t.Fatalf("%s: %v", what, err)
		}
		if pr.BlobRef != tb.BlobRef() || pr.Size != tb.Size() || pr.Skipped != skipped {
			t.Errorf("%s: PutResult = %v, %d, skipped %v; want %v, %d, skipped %v", what, pr.BlobRef, pr.Size, pr.Skipped, tb.BlobRef(), tb.Size(), skipped)
		}
		if sto.receives != receives || sto.stats != stats {
			t.Errorf("%s: receives, stats = %d, %d; want %d, %d", what, sto.receives, sto.stats, receives, stats)
		}
	}

	pr, err := c.Upload(ctx, handle(foo))
	check("first Upload", pr, err, foo, false, 1, 0)
	pr, err = c.Upload(ctx, handle(foo))
	check("second Upload", pr, err, foo, false, 2, 0)
	pr, err = c.UploadIfMissing(ctx, handle(foo))
	check("UploadIfMissing of a stored blob", pr, err, foo, true, 2, 1)
	pr, err = c.UploadIfMissing(ctx, handle(bar))
	check("UploadIfMissing of a new blob", pr, err, bar, false, 3, 2)
}

// readerAtOnly is an io.ReaderAt (and io.Closer) that isn't an io.Reader.
type readerAtOnly struct {
	r      *strings.Reader
	closed bool
}

func (r *readerAtOnly) ReadAt(p []byte, off int64) (int, error) { return r.r.ReadAt(p, off) }
func (r *readerAtOnly) Close() error                            { r.closed = true; return nil }

// TestUploadReaderAt checks that Upload reads a handle's Contents as an
// io.ReaderAt, afresh for each upload, and doesn't close them.
func TestUploadReaderAt(t *testing.T) {
	ctx := context.Background()
	c, sto := newStorageClient(t)
	tb := &test.Blob{Contents: "foo"}
	ra := &readerAtOnly{r: strings.NewReader(tb.Contents)}
	h := &UploadHandle{BlobRef: tb.BlobRef(), Size: tb.Size(), Contents: ra}
	for i := range 2 {
		if _, err := c.Upload(ctx, h); err != nil {
			t.Fatalf("upload %d: %v", i, err)
		}
	}
	if sto.receives != 2 {
		t.Errorf("receives = %d; want 2", sto.receives)
	}
	if ra.closed {
		t.Error("Upload closed the handle's Contents")
	}
}

// TestUploadHandleCheck checks that Upload accepts a Size of 0 only for
// the empty blob, of any hash type, and rejects other handles that can't
// be right before doing anything.
func TestUploadHandleCheck(t *testing.T) {
	ctx := context.Background()
	c, sto := newStorageClient(t)
	for _, alg := range []string{"sha1", "sha224", "sha256"} {
		h, err := blob.NewHashOfType(alg)
		if err != nil {
			t.Fatal(err)
		}
		empty := blob.RefFromHash(h)
		if _, err := c.Upload(ctx, &UploadHandle{BlobRef: empty}); err != nil {
			t.Errorf("Upload of empty %s blob %v: %v", alg, empty, err)
		}
	}
	foo := &test.Blob{Contents: "foo"}
	for _, tt := range []struct {
		name string
		h    *UploadHandle
		want string
	}{
		{"unset Size", &UploadHandle{BlobRef: foo.BlobRef(), Contents: strings.NewReader("foo")}, "is Size unset?"},
		{"no Contents", &UploadHandle{BlobRef: foo.BlobRef(), Size: foo.Size()}, "no Contents"},
		{"too big", &UploadHandle{BlobRef: foo.BlobRef(), Size: constants.MaxBlobSize + 1, Contents: strings.NewReader("foo")}, "over the max blob size"},
		{"no BlobRef", &UploadHandle{Size: foo.Size(), Contents: strings.NewReader("foo")}, "no valid BlobRef"},
	} {
		receives := sto.receives
		for _, upload := range []func(context.Context, *UploadHandle) (*PutResult, error){c.Upload, c.UploadIfMissing} {
			if _, err := upload(ctx, tt.h); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("%s: error = %v; want one containing %q", tt.name, err, tt.want)
			}
		}
		if sto.receives != receives {
			t.Errorf("%s: blob was received", tt.name)
		}
	}
}

func TestNewUploadHandleFromReader(t *testing.T) {
	tb := &test.Blob{Contents: "foo"}
	h, err := NewUploadHandleFromReader(tb.BlobRef(), tb.Reader())
	if err != nil {
		t.Fatal(err)
	}
	got := make([]byte, h.Size)
	if _, err := h.Contents.ReadAt(got, 0); err != nil || h.BlobRef != tb.BlobRef() || string(got) != tb.Contents {
		t.Errorf("handle = %v, %d bytes %q, %v; want %v, %q", h.BlobRef, h.Size, got, err, tb.BlobRef(), tb.Contents)
	}
	if _, err := NewUploadHandleFromReader(tb.BlobRef(), strings.NewReader(strings.Repeat("x", constants.MaxBlobSize+1))); err == nil {
		t.Error("NewUploadHandleFromReader accepted a blob over the max size")
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
