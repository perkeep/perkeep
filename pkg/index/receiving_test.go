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

package index

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver"
	"perkeep.org/pkg/sorted"
	"perkeep.org/pkg/test"
)

// haveBlockingKV is a KeyValue whose Gets of "have:" rows wait for
// release while it's non-nil, and which counts its batch commits.
type haveBlockingKV struct {
	sorted.KeyValue

	mu      sync.Mutex
	release chan struct{}
	commits int
}

func (kv *haveBlockingKV) Get(key string) (string, error) {
	kv.mu.Lock()
	release := kv.release
	kv.mu.Unlock()
	if release != nil && strings.HasPrefix(key, "have:") {
		<-release
	}
	return kv.KeyValue.Get(key)
}

func (kv *haveBlockingKV) CommitBatch(b sorted.BatchMutation) error {
	kv.mu.Lock()
	kv.commits++
	kv.mu.Unlock()
	return kv.KeyValue.CommitBatch(b)
}

// TestReceiveTwiceAtOnce checks that a blob received again while it's
// being received waits for that, and then skips it as indexed, rather than
// indexing it twice, for both ReceiveBlob and ReceiveBlobs.
func TestReceiveTwiceAtOnce(t *testing.T) {
	t.Setenv("CAMLI_REDO_INDEX_ON_RECEIVE", "false")
	for _, batch := range []bool{false, true} {
		name := "ReceiveBlob"
		if batch {
			name = "ReceiveBlobs"
		}
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				kv := &haveBlockingKV{KeyValue: sorted.NewMemoryKeyValue()}
				ix, err := New(kv)
				if err != nil {
					t.Fatal(err)
				}
				ix.Logger = test.NewLogger(t, "")
				ix.InitBlobSource(new(test.Fetcher))
				t.Cleanup(func() { ix.Close() })

				b := &test.Blob{Contents: "some blob"}
				receive := func() error {
					if batch {
						return ix.ReceiveBlobs(context.Background(), []*blob.Blob{b.Blob()})
					}
					_, err := ix.ReceiveBlob(context.Background(), b.BlobRef(), b.Reader())
					return err
				}
				release := make(chan struct{})
				kv.release = release
				kv.commits = 0
				var wg sync.WaitGroup
				for range 2 {
					wg.Go(func() {
						if err := receive(); err != nil {
							t.Error(err)
						}
					})
				}
				synctest.Wait()
				kv.mu.Lock()
				kv.release = nil
				kv.mu.Unlock()
				close(release)
				wg.Wait()

				if kv.commits != 1 {
					t.Errorf("index committed %d times; want 1", kv.commits)
				}
			})
		})
	}
}

// TestClaimReceiveContext checks that claimReceive gives up waiting for
// a blob that's being received when its context is done.
func TestClaimReceiveContext(t *testing.T) {
	ix := &Index{}
	br := blob.RefFromString("some blob")
	release, err := ix.claimReceive(context.Background(), []blob.Ref{br})
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ix.claimReceive(ctx, []blob.Ref{br}); err != context.Canceled {
		t.Errorf("claimReceive of a claimed blob with a canceled context = %v; want %v", err, context.Canceled)
	}
	other := blob.RefFromString("other blob")
	release2, err := ix.claimReceive(ctx, []blob.Ref{other})
	if err != nil {
		t.Errorf("claimReceive of an unclaimed blob = %v; want nil", err)
	} else {
		release2()
	}
}

// TestIndexReceived checks that the index indexes the schema blobs
// received into its blob source, and only those, both when they're
// received alone and together.
func TestIndexReceived(t *testing.T) {
	ix := NewMemoryIndex()
	ix.Logger = test.NewLogger(t, "")
	src := new(test.Fetcher)
	ix.InitBlobSource(src)
	ix.hookBlobSource(src)
	indexed := func(br blob.Ref) bool {
		v, err := ix.s.Get("have:" + br.String())
		return err == nil && strings.HasSuffix(v, "|indexed")
	}

	alone := &test.Blob{Contents: `{"camliVersion": 1, "camliType": "static-set", "members": []}`}
	data := &test.Blob{Contents: "not a schema blob"}
	if _, err := blobserver.Receive(context.Background(), src, alone.BlobRef(), alone.Reader()); err != nil {
		t.Fatal(err)
	}
	if !indexed(alone.BlobRef()) {
		t.Error("schema blob received alone isn't indexed")
	}

	together := &test.Blob{Contents: `{"camliVersion": 1, "camliType": "static-set", "members": [], "x": 1}`}
	if err := blobserver.ReceiveBlobs(context.Background(), src, []*blob.Blob{together.Blob(), data.Blob()}); err != nil {
		t.Fatal(err)
	}
	if !indexed(together.BlobRef()) {
		t.Error("schema blob received with another isn't indexed")
	}
	if indexed(data.BlobRef()) {
		t.Error("blob that isn't a schema blob was indexed; want it left to the sync to the index")
	}
}

// TestSubscribeSchemaChanges checks that a SubscribeSchemaChanges
// channel gets a signal whenever the index indexes a schema blob,
// whichever way the blob reached the index, and none otherwise.
func TestSubscribeSchemaChanges(t *testing.T) {
	ix := NewMemoryIndex()
	ix.Logger = test.NewLogger(t, "")
	src := new(test.Fetcher)
	ix.InitBlobSource(src)
	ix.hookBlobSource(src)
	changed := ix.SubscribeSchemaChanges()
	unread := ix.SubscribeSchemaChanges() // nobody reads it
	want := func(what string, has bool) {
		t.Helper()
		select {
		case <-changed:
			if !has {
				t.Errorf("%s: the channel has a value; want none", what)
			}
		default:
			if has {
				t.Errorf("%s: the channel has no value; want one", what)
			}
		}
	}
	ctx := context.Background()
	// do runs a step, and fails the test if it takes longer than 10
	// seconds. The index must never block sending to a channel that
	// nobody reads; if it did, the test would hang here.
	do := func(what string, f func() error) {
		t.Helper()
		errc := make(chan error, 1)
		go func() { errc <- f() }()
		select {
		case err := <-errc:
			if err != nil {
				t.Fatalf("%s: %v", what, err)
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("%s: still running after 10s", what)
		}
	}
	schemaBlob := func(n int) *test.Blob {
		return &test.Blob{Contents: fmt.Sprintf(`{"camliVersion": 1, "camliType": "static-set", "members": [], "n": %d}`, n)}
	}

	want("before any blob", false)

	// From the receive hook on the blob source, as an upload.
	b := schemaBlob(1)
	do("receive b", func() error { _, err := blobserver.Receive(ctx, src, b.BlobRef(), b.Reader()); return err })
	want("schema blob received into the blob source", true)

	// The same blob again, sent to the index directly, as a
	// storage-replica config or a sync does for blobs that the hook
	// has indexed already.
	do("receive b again", func() error { _, err := blobserver.ReceiveNoHash(ctx, ix, b.BlobRef(), b.Reader()); return err })
	want("schema blob already indexed", false)

	data := &test.Blob{Contents: "not a schema blob"}
	do("receive data", func() error { _, err := blobserver.Receive(ctx, ix, data.BlobRef(), data.Reader()); return err })
	want("blob that isn't a schema blob", false)

	// From a sync to the index, which doesn't notify any blob hub.
	// Two blobs give one signal.
	b2, b3 := schemaBlob(2), schemaBlob(3)
	do("sync", func() error { return blobserver.ReceiveBlobsUnchecked(ctx, ix, []*blob.Blob{b2.Blob(), b3.Blob()}) })
	want("two schema blobs from a sync", true)
	want("after receiving their value", false)

	// Indexed late: a file whose chunk arrives after it.
	chunk := &test.Blob{Contents: "foo"}
	file := &test.Blob{Contents: fmt.Sprintf(`{"camliVersion": 1, "camliType": "file", "fileName": "f.txt", "parts": [{"blobRef": %q, "size": 3}]}`, chunk.BlobRef())}
	do("receive file", func() error { _, err := blobserver.Receive(ctx, src, file.BlobRef(), file.Reader()); return err })
	want("file before its chunk", false)
	do("receive chunk", func() error { _, err := blobserver.Receive(ctx, src, chunk.BlobRef(), chunk.Reader()); return err })
	do("sync chunk", func() error { return blobserver.ReceiveBlobsUnchecked(ctx, ix, []*blob.Blob{chunk.Blob()}) })
	do("indexing late", func() error { ix.awaitReadyIdle(); return nil })
	want("file indexed once its chunk arrived", true)

	// The channel that nobody reads holds one signal, and didn't
	// block any of the indexing above.
	if len(unread) != 1 {
		t.Errorf("the channel nothing received from has %d values; want 1", len(unread))
	}
}

// TestSchemaChangeSignaledAfterCommit checks that the index signals a
// schema blob only once the blob's rows are committed, so that a search
// the signal starts sees them. The index prepares a blob without its
// lock and commits it with the lock held; the test holds the read lock
// while the index finishes preparing a file, so no signal may come.
func TestSchemaChangeSignaledAfterCommit(t *testing.T) {
	ix := NewMemoryIndex()
	ix.Logger = test.NewLogger(t, "")
	src := &gateFetcher{Fetcher: new(test.Fetcher), fetching: make(chan struct{}), proceed: make(chan struct{})}
	ix.InitBlobSource(src)
	changed := ix.SubscribeSchemaChanges()
	chunk := &test.Blob{Contents: "foo"}
	src.Fetcher.AddBlob(chunk)
	file := &test.Blob{Contents: fmt.Sprintf(`{"camliVersion": 1, "camliType": "file", "fileName": "f.txt", "parts": [{"blobRef": %q, "size": 3}]}`, chunk.BlobRef())}
	errc := make(chan error, 1)
	go func() {
		_, err := blobserver.ReceiveNoHash(context.Background(), ix, file.BlobRef(), file.Reader())
		errc <- err
	}()

	<-src.fetching // preparing the file, which fetches its part
	unlock := ix.RLock()
	close(src.proceed)
	// Correct code can't signal while the read lock is held, however
	// long this waits; it waits only so that an early signal arrives.
	select {
	case <-changed:
		t.Error("signaled before the file's rows could be committed")
	case <-time.After(100 * time.Millisecond):
	}
	unlock()
	if err := <-errc; err != nil {
		t.Fatal(err)
	}
	select {
	case <-changed:
	default:
		t.Error("no signal once the file was indexed")
	}
}

// gateFetcher is a test.Fetcher whose first fetch sends on fetching,
// then waits for proceed to be closed.
type gateFetcher struct {
	*test.Fetcher
	fetching, proceed chan struct{}
	once              sync.Once
}

func (f *gateFetcher) Fetch(ctx context.Context, br blob.Ref) (io.ReadCloser, uint32, error) {
	f.once.Do(func() {
		f.fetching <- struct{}{}
		<-f.proceed
	})
	return f.Fetcher.Fetch(ctx, br)
}
