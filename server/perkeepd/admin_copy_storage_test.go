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

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"go4.org/jsonconfig"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver"
	"perkeep.org/pkg/blobserver/memory"
	"perkeep.org/pkg/test"
)

func TestCopyStorageGoogleDriveRegistered(t *testing.T) {
	// Reject the missing configuration before any network access. Importing
	// the backend here would mask a missing production registration.
	_, err := blobserver.CreateStorage("googledrive", nil, jsonconfig.Obj{})
	if err == nil || !strings.Contains(err.Error(), `Missing required config key "auth"`) {
		t.Fatalf("Google Drive storage registration: %v", err)
	}
}

// corruptingStorage is a Storage that lists one more blob than its
// underlying storage has, and serves the wrong contents for it.
type corruptingStorage struct {
	*memory.Storage
	bad blob.SizedRef
}

func (s *corruptingStorage) EnumerateBlobs(ctx context.Context, dest chan<- blob.SizedRef, after string, limit int) error {
	return blobserver.MergedEnumerate(ctx, dest, []blobserver.BlobEnumerator{s.Storage, oneBlob{s.bad}}, after, limit)
}

func (s *corruptingStorage) Fetch(ctx context.Context, br blob.Ref) (io.ReadCloser, uint32, error) {
	if br == s.bad.Ref {
		return io.NopCloser(strings.NewReader("not the contents")), s.bad.Size, nil
	}
	return s.Storage.Fetch(ctx, br)
}

type oneBlob struct{ sb blob.SizedRef }

func (o oneBlob) EnumerateBlobs(ctx context.Context, dest chan<- blob.SizedRef, after string, limit int) error {
	defer close(dest)
	if limit > 0 && o.sb.Ref.String() > after {
		dest <- o.sb
	}
	return nil
}

var (
	errEnumFailed = errors.New("listing failed")
	errStatFailed = errors.New("stat failed")
)

// failingEnum is a Storage whose listing fails.
type failingEnum struct{ *memory.Storage }

func (failingEnum) EnumerateBlobs(ctx context.Context, dest chan<- blob.SizedRef, after string, limit int) error {
	close(dest)
	return errEnumFailed
}

// failingStat is a Storage whose stats fail.
type failingStat struct{ *memory.Storage }

func (failingStat) StatBlobs(ctx context.Context, blobs []blob.Ref, fn func(blob.SizedRef) error) error {
	return errStatFailed
}

func TestCopyMissingBlobs(t *testing.T) {
	ctx := t.Context()
	a, b, c := &test.Blob{Contents: "a"}, &test.Blob{Contents: "b"}, &test.Blob{Contents: "c"}
	bad := (&test.Blob{Contents: "corrupt in the source"}).SizedRef()
	src := &corruptingStorage{Storage: new(memory.Storage), bad: bad}
	for _, tb := range []*test.Blob{a, b, c} {
		tb.MustUpload(t, src.Storage)
	}
	dstMem := new(memory.Storage)
	b.MustUpload(t, dstMem)
	// The destination is never listed, only statted.
	dst := failingEnum{dstMem}

	var log strings.Builder
	res, err := copyMissingBlobs(ctx, src, dst, copyOpts{workers: 2, dryRun: true, log: &log})
	if err != nil {
		t.Fatal(err)
	}
	if res.seen != 4 || res.missing != 3 || res.copied != 0 {
		t.Errorf("dry run: %+v; want 4 seen, 3 missing, none copied", res)
	}
	if n := dstMem.NumBlobs(); n != 1 {
		t.Errorf("after dry run, destination has %d blobs; want 1", n)
	}

	res, err = copyMissingBlobs(ctx, src, dst, copyOpts{workers: 2, dryRun: false, log: &log})
	if err != nil {
		t.Fatal(err)
	}
	if res.missing != 3 || res.copied != 2 || res.failed != 1 {
		t.Errorf("copy: %+v; want 3 missing, 2 copied, 1 failed", res)
	}
	if !strings.Contains(log.String(), bad.Ref.String()+": "+errDigestMismatch.Error()) {
		t.Errorf("log doesn't report the corrupt blob:\n%s", log.String())
	}
	for _, tb := range []*test.Blob{a, b, c} {
		if _, _, err := dstMem.Fetch(ctx, tb.BlobRef()); err != nil {
			t.Errorf("destination is missing %v: %v", tb.BlobRef(), err)
		}
	}
	if _, _, err := dstMem.Fetch(ctx, bad.Ref); err == nil {
		t.Errorf("destination has the corrupt blob")
	}

	// Only the corrupt blob is still missing.
	res, err = copyMissingBlobs(ctx, src, dst, copyOpts{workers: 2, dryRun: true, log: &log})
	if err != nil {
		t.Fatal(err)
	}
	if res.missing != 1 {
		t.Errorf("second dry run: %+v; want 1 missing", res)
	}

	if _, err := copyMissingBlobs(ctx, src, failingStat{dstMem}, copyOpts{workers: 2, dryRun: false, log: &log}); !errors.Is(err, errStatFailed) {
		t.Errorf("with failing destination stats, error = %v; want %v", err, errStatFailed)
	}
	if _, err := copyMissingBlobs(ctx, failingEnum{src.Storage}, dst, copyOpts{workers: 2, dryRun: false, log: &log}); !errors.Is(err, errEnumFailed) {
		t.Errorf("with a failing source listing, error = %v; want %v", err, errEnumFailed)
	}
}

// TestCopyMissingBlobsBatches checks copying more blobs than fit in one
// batch of stats.
func TestCopyMissingBlobsBatches(t *testing.T) {
	ctx := t.Context()
	src, dst := new(memory.Storage), new(memory.Storage)
	const n = 2*statBatchSize + 50
	for i := range n {
		tb := &test.Blob{Contents: fmt.Sprint("blob ", i)}
		tb.MustUpload(t, src)
		if i%3 == 0 {
			tb.MustUpload(t, dst)
		}
	}
	wantMissing := n - (n+2)/3
	var log strings.Builder
	res, err := copyMissingBlobs(ctx, src, failingEnum{dst}, copyOpts{workers: 4, dryRun: false, log: &log})
	if err != nil {
		t.Fatal(err)
	}
	if res.seen != n || res.missing != int64(wantMissing) || res.copied != int64(wantMissing) || res.failed != 0 {
		t.Errorf("result = %+v; want %d seen, %d missing and copied", res, n, wantMissing)
	}
	if got := dst.NumBlobs(); got != n {
		t.Errorf("destination has %d blobs; want %d", got, n)
	}
}

func TestCopyMissingBlobsLimit(t *testing.T) {
	ctx := t.Context()
	src, dst := new(memory.Storage), new(memory.Storage)
	for i := range 250 {
		(&test.Blob{Contents: fmt.Sprint("blob ", i)}).MustUpload(t, src)
	}
	var log strings.Builder
	res, err := copyMissingBlobs(ctx, src, dst, copyOpts{workers: 2, dryRun: true, limit: 120, log: &log})
	if err != nil {
		t.Fatal(err)
	}
	if res.seen != 120 || res.missing != 120 {
		t.Errorf("result = %+v; want 120 seen and missing", res)
	}
}
