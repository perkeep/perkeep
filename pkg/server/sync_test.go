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

package server

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/synctest"

	"golang.org/x/sync/semaphore"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver"
	"perkeep.org/pkg/sorted"
	"perkeep.org/pkg/sorted/sqlite"
	"perkeep.org/pkg/test"
)

func syncQueueRef(n int) blob.Ref {
	return blob.MustParse(fmt.Sprintf("sha1-%040x", n))
}

func TestEnumerateQueuedBlobsSQLiteReentrant(t *testing.T) {
	test.TLog(t)
	synctest.Test(t, func(t *testing.T) {
		kv, err := sqlite.NewStorage(filepath.Join(t.TempDir(), "queue.sqlite"))
		if err != nil {
			t.Fatal(err)
		}
		defer kv.Close()
		const count = 2005
		batch := kv.BeginBatch()
		batch.Set("bad-blobref", "1")
		batch.Set(syncQueueRef(count+1).String(), "bad-size")
		batch.Set(syncQueueRef(count+2).String(), "4294967296")
		for i := 1; i <= count; i++ {
			batch.Set(syncQueueRef(i).String(), strconv.Itoa(i))
		}
		if err := kv.CommitBatch(batch); err != nil {
			t.Fatal(err)
		}
		sh := newSyncHandler("source", "destination", nil, nil, kv)
		dst := make(chan blob.SizedRef)
		intr := make(chan struct{})
		defer close(intr)
		errc := make(chan error, 1)
		go func() { errc <- sh.enumerateQueuedBlobs(dst, intr) }()
		synctest.Wait() // Sender is blocked before the receiver starts.
		if _, err := kv.Get(syncQueueRef(1).String()); err != nil {
			t.Fatal(err)
		}
		for i := 1; i <= count; i++ {
			want := blob.SizedRef{Ref: syncQueueRef(i), Size: uint32(i)}
			if got, ok := <-dst; !ok || got != want {
				t.Fatalf("blob %d = %v, %v; want %v", i, got, ok, want)
			}
			// A successful copy deletes from this same queue while the
			// enumerator may already be blocked on the next channel send.
			if err := kv.Delete(want.Ref.String()); err != nil {
				t.Fatal(err)
			}
		}
		if _, ok := <-dst; ok {
			t.Fatal("enumerator sent an extra blob")
		}
		if err := <-errc; err != nil {
			t.Fatal(err)
		}
		if _, err := kv.Get(syncQueueRef(1).String()); !errors.Is(err, sorted.ErrNotFound) {
			t.Fatalf("deleted queue entry: got %v; want ErrNotFound", err)
		}
	})
}

func TestEnumerateQueuedBlobsSQLiteInterrupt(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		kv, err := sqlite.NewStorage(filepath.Join(t.TempDir(), "queue.sqlite"))
		if err != nil {
			t.Fatal(err)
		}
		defer kv.Close()
		br := syncQueueRef(1)
		if err := kv.Set(br.String(), "1"); err != nil {
			t.Fatal(err)
		}
		sh := newSyncHandler("source", "destination", nil, nil, kv)
		dst := make(chan blob.SizedRef)
		intr := make(chan struct{})
		errc := make(chan error, 1)
		go func() { errc <- sh.enumerateQueuedBlobs(dst, intr) }()
		synctest.Wait()
		close(intr)
		if err := <-errc; err != nil {
			t.Fatalf("interrupted enumeration: %v", err)
		}
		if _, ok := <-dst; ok {
			t.Fatal("interrupted enumerator sent a blob")
		}
		if err := kv.Delete(br.String()); err != nil {
			t.Fatal(err)
		}
	})
}

// failingSetKV fails Set while fail is true.
type failingSetKV struct {
	sorted.KeyValue
	fail bool
}

var errQueueSet = errors.New("queue set failed")

func (kv *failingSetKV) Set(key, value string) error {
	if kv.fail {
		return errQueueSet
	}
	return kv.KeyValue.Set(key, value)
}

// queueWritesKV counts queue writes, failing CommitBatch while fail is
// true.
type queueWritesKV struct {
	sorted.KeyValue
	sets, deletes, commits int
	fail                   bool
}

func (kv *queueWritesKV) Set(key, value string) error {
	kv.sets++
	return kv.KeyValue.Set(key, value)
}

func (kv *queueWritesKV) Delete(key string) error {
	kv.deletes++
	return kv.KeyValue.Delete(key)
}

func (kv *queueWritesKV) CommitBatch(b sorted.BatchMutation) error {
	kv.commits++
	if kv.fail {
		return errQueueSet
	}
	return kv.KeyValue.CommitBatch(b)
}

// TestEnqueueBlobs checks that blobs received together are written to the
// queue together, skipping those already pending.
func TestEnqueueBlobs(t *testing.T) {
	kv := &queueWritesKV{KeyValue: sorted.NewMemoryKeyValue()}
	defer kv.Close()
	src := new(test.Fetcher)
	sh := newSyncHandler("source", "destination", src, new(test.Fetcher), kv)
	blobserver.GetHub(src).AddReceiveBlobsHook(sh.enqueueBlobs)
	var sbs []blob.SizedRef
	for i := range 4 {
		sbs = append(sbs, blob.SizedRef{Ref: blob.RefFromString(fmt.Sprint(i)), Size: 1})
	}
	if err := sh.enqueue(sbs[0]); err != nil {
		t.Fatal(err)
	}
	kv.sets = 0

	// One fails to be written: none of the new ones are pending.
	kv.fail = true
	err := blobserver.GetHub(src).NotifyBlobsReceived(sbs)
	if got := blobserver.BlobErr(err, sbs[0].Ref); got != nil {
		t.Errorf("already pending blob's error = %v; want nil", got)
	}
	for _, sb := range sbs[1:] {
		if got := blobserver.BlobErr(err, sb.Ref); !errors.Is(got, errQueueSet) {
			t.Errorf("BlobErr(%v) = %v; want %v", sb.Ref, got, errQueueSet)
		}
	}
	if st := sh.currentStatus(); st.BlobsToCopy != 1 {
		t.Errorf("after failed enqueue, %d blobs to copy; want 1", st.BlobsToCopy)
	}

	kv.fail, kv.commits = false, 0
	if err := blobserver.GetHub(src).NotifyBlobsReceived(sbs); err != nil {
		t.Fatal(err)
	}
	if kv.commits != 1 || kv.sets != 0 {
		t.Errorf("queue commits, sets = %d, %d; want 1, 0", kv.commits, kv.sets)
	}
	for _, sb := range sbs {
		if _, err := kv.Get(sb.Ref.String()); err != nil {
			t.Errorf("queue entry for %v: %v", sb.Ref, err)
		}
	}
	if st := sh.currentStatus(); st.BlobsToCopy != len(sbs) {
		t.Errorf("%d blobs to copy; want %d", st.BlobsToCopy, len(sbs))
	}
}

// TestEnqueueRetryAfterQueueError checks that a blob whose queue write
// failed is written by a retry, rather than taken as a duplicate.
// groupDest is a BlobsReceiver destination recording the sizes of its
// ReceiveBlobs calls, and failing the blobs in fail.
type groupDest struct {
	*test.Fetcher
	mu    sync.Mutex
	calls []int
	fail  map[blob.Ref]bool
}

var errDestFail = errors.New("dest failed")

func (d *groupDest) ReceiveBlobs(ctx context.Context, blobs []*blob.Blob) error {
	d.mu.Lock()
	d.calls = append(d.calls, len(blobs))
	d.mu.Unlock()
	be := &blobserver.BlobsError{Op: "receive"}
	for _, b := range blobs {
		if d.fail[b.Ref()] {
			be.Add(b.Ref(), errDestFail)
			continue
		}
		r, err := b.ReadAll(ctx)
		if err == nil {
			_, err = d.Fetcher.ReceiveBlob(ctx, b.Ref(), r)
		}
		be.Add(b.Ref(), err)
	}
	return be.OrNil()
}

// enqueueTestBlobs stores n new blobs in src and enqueues them in sh.
func enqueueTestBlobs(t *testing.T, sh *SyncHandler, src *test.Fetcher, n int) []blob.SizedRef {
	t.Helper()
	var sbs []blob.SizedRef
	for i := range n {
		tb := &test.Blob{Contents: fmt.Sprintf("blob %d", i)}
		src.AddBlob(tb)
		if err := sh.enqueue(tb.SizedRef()); err != nil {
			t.Fatal(err)
		}
		sbs = append(sbs, tb.SizedRef())
	}
	return sbs
}

// TestWriteFetchedGroups checks that the copy loop writes fetched blobs
// to the destination in groups, deleting each group's queue entries in
// one batch.
func TestWriteFetchedGroups(t *testing.T) {
	kv := &queueWritesKV{KeyValue: sorted.NewMemoryKeyValue()}
	defer kv.Close()
	src := new(test.Fetcher)
	dst := &groupDest{Fetcher: new(test.Fetcher)}
	sh := newSyncHandler("source", "destination", src, dst, kv)
	sbs := enqueueTestBlobs(t, sh, src, syncWriteGroupBlobs+44)
	bad := sbs[3].Ref
	dst.fail = map[blob.Ref]bool{bad: true}

	// Fetch them all first, so the grouping doesn't depend on timing.
	budget := semaphore.NewWeighted(syncWriteGroupBytes)
	fetched := make(chan fetchedBlob, len(sbs))
	for _, sb := range sbs {
		cs := sh.newCopyStatus(sb)
		b, err := sh.fetchBlob(context.Background(), cs, budget)
		if err != nil {
			t.Fatal(err)
		}
		fetched <- fetchedBlob{cs, b}
	}
	close(fetched)
	res := make(chan copyResult, len(sbs))
	sh.writeFetched(context.Background(), fetched, budget, res)

	if want := []int{syncWriteGroupBlobs, 44}; !slices.Equal(dst.calls, want) {
		t.Errorf("destination ReceiveBlobs calls of sizes %v; want %v", dst.calls, want)
	}
	if kv.deletes != 0 || kv.commits != 2 {
		t.Errorf("queue deletes, commits = %d, %d; want 0, 2", kv.deletes, kv.commits)
	}
	for range sbs {
		r := <-res
		if (r.sb.Ref == bad) != errors.Is(r.err, errDestFail) {
			t.Errorf("result for %v: %v", r.sb.Ref, r.err)
		}
	}
	// The failed blob stays queued, to try again; the others are done.
	for _, sb := range sbs {
		_, err := kv.Get(sb.Ref.String())
		if queued := err == nil; queued != (sb.Ref == bad) {
			t.Errorf("%v queued = %v", sb.Ref, queued)
		}
	}
	if st := sh.currentStatus(); st.BlobsToCopy != 1 {
		t.Errorf("%d blobs to copy; want 1", st.BlobsToCopy)
	}
	if !budget.TryAcquire(syncWriteGroupBytes) {
		t.Error("fetch budget not all released")
	}
}

// TestRunSyncCopiesQueued checks that a sync round copies all the queued
// blobs and empties the queue.
func TestRunSyncCopiesQueued(t *testing.T) {
	kv := sorted.NewMemoryKeyValue()
	defer kv.Close()
	src := new(test.Fetcher)
	dst := &groupDest{Fetcher: new(test.Fetcher)}
	sh := newSyncHandler("source", "destination", src, dst, kv)
	sbs := enqueueTestBlobs(t, sh, src, 600)
	if n := sh.runSync("queue", sh.enumerateQueuedBlobs); n != len(sbs) {
		t.Fatalf("runSync copied %d blobs; want %d", n, len(sbs))
	}
	if dst.NumBlobs() != len(sbs) {
		t.Errorf("destination has %d blobs; want %d", dst.NumBlobs(), len(sbs))
	}
	it := kv.Find("", "")
	for it.Next() {
		t.Errorf("still queued: %s", it.Key())
	}
	it.Close()
	if st := sh.currentStatus(); st.BlobsToCopy != 0 {
		t.Errorf("%d blobs to copy; want 0", st.BlobsToCopy)
	}
}

func TestEnqueueRetryAfterQueueError(t *testing.T) {
	kv := &failingSetKV{KeyValue: sorted.NewMemoryKeyValue(), fail: true}
	defer kv.Close()
	sh := newSyncHandler("source", "destination", new(test.Fetcher), new(test.Fetcher), kv)
	br := blob.RefFromString("foo")
	sb := blob.SizedRef{Ref: br, Size: 3}
	if err := sh.enqueue(sb); !errors.Is(err, errQueueSet) {
		t.Fatalf("enqueue with failing queue = %v; want %v", err, errQueueSet)
	}
	if st := sh.currentStatus(); st.BlobsToCopy != 0 || st.BytesToCopy != 0 {
		t.Fatalf("after failed enqueue, %d blobs (%d bytes) to copy; want none", st.BlobsToCopy, st.BytesToCopy)
	}
	kv.fail = false
	if err := sh.enqueue(sb); err != nil {
		t.Fatal(err)
	}
	if v, err := kv.Get(br.String()); err != nil || v != "3" {
		t.Fatalf("queue entry after retry = %q, %v; want \"3\"", v, err)
	}
	if st := sh.currentStatus(); st.BlobsToCopy != 1 || st.BytesToCopy != 3 {
		t.Fatalf("after retry, %d blobs (%d bytes) to copy; want 1 (3 bytes)", st.BlobsToCopy, st.BytesToCopy)
	}
	// A duplicate doesn't write the queue again.
	kv.fail = true
	if err := sh.enqueue(sb); err != nil {
		t.Fatalf("duplicate enqueue = %v; want nil", err)
	}
}

type syncQueueErrorKV struct {
	sorted.KeyValue
	closeErr error
}

func (kv *syncQueueErrorKV) Find(start, end string) sorted.Iterator {
	return &syncQueueErrorIter{Iterator: kv.KeyValue.Find(start, end), closeErr: kv.closeErr}
}

type syncQueueErrorIter struct {
	sorted.Iterator
	closeErr error
}

func (it *syncQueueErrorIter) Close() error {
	if err := it.Iterator.Close(); err != nil {
		return err
	}
	return it.closeErr
}

func TestEnumerateQueuedBlobsIteratorError(t *testing.T) {
	wantErr := errors.New("iterator failed")
	kv := &syncQueueErrorKV{KeyValue: sorted.NewMemoryKeyValue(), closeErr: wantErr}
	defer kv.Close()
	if err := kv.Set(syncQueueRef(1).String(), "1"); err != nil {
		t.Fatal(err)
	}
	sh := newSyncHandler("source", "destination", nil, nil, kv)
	dst := make(chan blob.SizedRef, 1)
	if err := sh.enumerateQueuedBlobs(dst, make(chan struct{})); !errors.Is(err, wantErr) {
		t.Fatalf("got %v; want %v", err, wantErr)
	}
	want := blob.SizedRef{Ref: syncQueueRef(1), Size: 1}
	if got, ok := <-dst; !ok || got != want {
		t.Fatalf("buffered blob = %v, %v; want %v", got, ok, want)
	}
	if _, ok := <-dst; ok {
		t.Fatal("enumerator sent an extra blob")
	}
}

func TestEnumerateQueuedBlobsInterruptSuppressesIteratorError(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		kv := &syncQueueErrorKV{
			KeyValue: sorted.NewMemoryKeyValue(),
			closeErr: errors.New("iterator failed"),
		}
		defer kv.Close()
		if err := kv.Set(syncQueueRef(1).String(), "1"); err != nil {
			t.Fatal(err)
		}
		sh := newSyncHandler("source", "destination", nil, nil, kv)
		dst := make(chan blob.SizedRef)
		intr := make(chan struct{})
		errc := make(chan error, 1)
		go func() { errc <- sh.enumerateQueuedBlobs(dst, intr) }()
		synctest.Wait() // Buffered blob blocks before the terminal error.
		close(intr)
		if err := <-errc; err != nil {
			t.Fatalf("interrupted enumeration: %v; want nil", err)
		}
		if _, ok := <-dst; ok {
			t.Fatal("interrupted enumerator sent a blob")
		}
	})
}

func TestRunSyncStopsQueuedEnumeratorOnFullWorkBuffer(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		kv := sorted.NewMemoryKeyValue()
		defer kv.Close()
		src, dst := new(test.Fetcher), new(test.Fetcher)
		releaseFetch := make(chan struct{})
		var fetchStarted sync.Once
		fetching := make(chan struct{})
		src.FetchErr = func() error {
			fetchStarted.Do(func() { close(fetching) })
			<-releaseFetch
			return nil
		}
		sh := newSyncHandler("source", "destination", src, dst, kv)
		sh.copierPoolSize = 1
		const count = 1100 // More than workch + enumch + active workers.
		for i := 0; i < count; i++ {
			body := fmt.Sprintf("blob %d", i)
			br := blob.RefFromString(body)
			sb, err := src.ReceiveBlob(context.Background(), br, strings.NewReader(body))
			if err != nil {
				t.Fatal(err)
			}
			if err := sh.enqueue(sb); err != nil {
				t.Fatal(err)
			}
		}
		enumDone := make(chan struct{})
		copied := make(chan int, 1)
		go func() {
			copied <- sh.runSync("queue", func(dst chan<- blob.SizedRef, intr <-chan struct{}) error {
				defer close(enumDone)
				return sh.enumerateQueuedBlobs(dst, intr)
			})
		}()
		<-fetching
		synctest.Wait()
		select {
		case <-enumDone:
		default:
			t.Error("full work buffer did not interrupt the queued enumerator")
		}
		select {
		case n := <-copied:
			t.Fatalf("runSync returned %d before copies could finish", n)
		default:
		}
		close(releaseFetch)
		n := <-copied
		// Depending on when the worker takes its first blob, either 1000 or
		// 1001 blobs fit in this batch. The remainder must stay queued.
		if n < 1000 || n > 1001 {
			t.Fatalf("copied %d blobs; want 1000 or 1001", n)
		}
		sh.mu.Lock()
		pending := len(sh.needCopy)
		sh.mu.Unlock()
		if pending != count-n {
			t.Fatalf("%d blobs pending; want %d", pending, count-n)
		}
		queued := 0
		for r := range sorted.ScanPaged(kv, "", "") {
			if _, err := r.Value(); err != nil {
				t.Fatal(err)
			}
			queued++
		}
		if queued != pending {
			t.Fatalf("%d blobs queued; want %d", queued, pending)
		}
	})
}
