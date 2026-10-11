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

package cloudstorage

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver"
	"perkeep.org/pkg/blobserver/memory"
	"perkeep.org/pkg/blobserver/storagetest"
	"perkeep.org/pkg/test"

	"cloud.google.com/go/storage"
	"google.golang.org/api/option"
)

const fakeBucket = "fake-bucket"

// newFakeStorage returns a Storage backed by a fake GCS server.
func newFakeStorage(t *testing.T, dirPrefix string, cache bool) (*Storage, *fakeGCS) {
	t.Helper()
	fake := newFakeGCS(fakeBucket)
	hc := httptest.NewTestServer(t, fake).Client()
	cl, err := storage.NewClient(t.Context(), option.WithHTTPClient(hc))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cl.Close()
		hc.CloseIdleConnections()
	})
	s := &Storage{
		bucket:         fakeBucket,
		dirPrefix:      dirPrefix,
		client:         cl,
		baseHTTPClient: hc,
	}
	if cache {
		s.cache = memory.NewCache(1 << 20)
	}
	return s, fake
}

func TestStorageFake(t *testing.T) {
	storagetest.Test(t, func(t *testing.T) blobserver.Storage {
		s, _ := newFakeStorage(t, "", true)
		return s
	})
}

func TestStorageFakeWithBucketDir(t *testing.T) {
	storagetest.Test(t, func(t *testing.T) blobserver.Storage {
		s, fake := newFakeStorage(t, "bl/obs/", false)
		// Objects outside the "directory" must not be touched
		// or have any influence.
		fake.put("a", []byte("a"))
		fake.put("c", []byte("c"))
		t.Cleanup(func() {
			for _, name := range []string{"a", "c"} {
				if _, ok := fake.get(name); !ok {
					t.Errorf("object %q outside of bucket dir was removed", name)
				}
			}
		})
		return s
	})
}

// errReader is an io.Reader that fails the test if it's read.
type errReader struct{ t *testing.T }

func (r errReader) Read([]byte) (int, error) {
	r.t.Error("unexpected read of blob source")
	return 0, errors.New("unexpected read")
}

func TestReceiveBlobExisting(t *testing.T) {
	ctx := context.Background()
	s, fake := newFakeStorage(t, "dir/", false)
	tb := &test.Blob{Contents: "foo"}
	name := "dir/" + tb.BlobRef().String()

	sb, err := blobserver.Receive(ctx, s, tb.BlobRef(), tb.Reader())
	if err != nil {
		t.Fatal(err)
	}
	tb.AssertMatches(t, sb)
	if n := fake.numUploads(name); n != 1 {
		t.Fatalf("after first ReceiveBlob, uploads = %d; want 1", n)
	}

	// The second time, the existing object should be noticed
	// without reading the source or uploading anything.
	sb, err = s.ReceiveBlob(ctx, tb.BlobRef(), errReader{t})
	if err != nil {
		t.Fatal(err)
	}
	tb.AssertMatches(t, sb)
	if n := fake.numUploads(name); n != 1 {
		t.Errorf("after second ReceiveBlob, uploads = %d; want 1", n)
	}
}

// TestReceiveBlobConcurrentCreate tests that ReceiveBlob succeeds
// without overwriting anything when the blob is created by someone
// else after ReceiveBlob checks for its existence but before it
// uploads.
func TestReceiveBlobConcurrentCreate(t *testing.T) {
	ctx := context.Background()
	s, fake := newFakeStorage(t, "", true)
	tb := &test.Blob{Contents: "foo"}
	name := tb.BlobRef().String()

	// Make ReceiveBlob's stat miss, with the object then
	// appearing before the upload. The contents differ so we
	// can tell whether it was overwritten.
	const otherContents = "uploaded concurrently"
	fake.afterStat = func(statName string) {
		if statName == name {
			fake.put(name, []byte(otherContents))
		}
	}

	sb, err := blobserver.Receive(ctx, s, tb.BlobRef(), tb.Reader())
	if err != nil {
		t.Fatal(err)
	}
	tb.AssertMatches(t, sb)
	if n := fake.numUploads(name); n != 1 {
		t.Errorf("uploads = %d; want 1", n)
	}
	if got, _ := fake.get(name); string(got) != otherContents {
		t.Errorf("object contents = %q; want %q (not overwritten)", got, otherContents)
	}
}

func TestReceiveBlobStatError(t *testing.T) {
	ctx := context.Background()
	s, fake := newFakeStorage(t, "", false)
	fake.statErr = http.StatusForbidden
	tb := &test.Blob{Contents: "foo"}

	if _, err := s.ReceiveBlob(ctx, tb.BlobRef(), errReader{t}); err == nil {
		t.Fatal("ReceiveBlob succeeded; want error")
	}
	if n := fake.numUploads(tb.BlobRef().String()); n != 0 {
		t.Errorf("uploads = %d; want 0", n)
	}
	err := s.StatBlobs(ctx, tb.BlobRefSlice(), func(blob.SizedRef) error {
		t.Error("unexpected StatBlobs callback")
		return nil
	})
	if err == nil {
		t.Error("StatBlobs succeeded; want error")
	}
}
