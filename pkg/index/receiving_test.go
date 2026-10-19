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
	"strings"
	"sync"
	"testing"
	"testing/synctest"

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
