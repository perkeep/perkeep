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
	"crypto/sha1"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"testing/synctest"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver"
	"perkeep.org/pkg/blobserver/memory"
	"perkeep.org/pkg/health"
	"perkeep.org/pkg/sorted"
	"perkeep.org/pkg/test"
)

// newHealthSyncHandler returns a sync handler from src to dst with its
// health checks registered with a new Monitor, which has ended startup.
// It doesn't copy blobs on its own.
func newHealthSyncHandler(t *testing.T, src *memory.Storage, dst blobReceiverEnumerator) (*SyncHandler, *health.Monitor) {
	sh := newSyncHandler("/src/", "/dst/", src, dst, sorted.NewMemoryKeyValue())
	mon := health.New()
	t.Cleanup(func() { mon.Close() })
	_, toFetcher := dst.(blob.Fetcher)
	sh.registerChecks(mon, "/sync-test/", false, toFetcher, 0)
	mon.EndStartup()
	return sh, mon
}

func checkStatus(t *testing.T, mon *health.Monitor, id string) health.CheckStatus {
	t.Helper()
	for _, c := range mon.Status().Checks {
		if c.ID == id {
			return c
		}
	}
	t.Fatalf("no check %q in %+v", id, mon.Status().Checks)
	return health.CheckStatus{}
}

// runCheck runs the check with the given ID, and returns its status
// once it's done.
func runCheck(t *testing.T, mon *health.Monitor, id string) health.CheckStatus {
	t.Helper()
	if err := mon.RunNow(id); err != nil {
		t.Fatal(err)
	}
	synctest.Wait()
	return checkStatus(t, mon, id)
}

func uploadBlobs(t *testing.T, sto *memory.Storage, contents ...string) {
	for _, s := range contents {
		(&test.Blob{Contents: s}).MustUpload(t, sto)
	}
}

func TestSyncValidateCheck(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		src, dst := new(memory.Storage), new(memory.Storage)
		uploadBlobs(t, src, "a", "b", "c")
		uploadBlobs(t, dst, "a")
		sh, mon := newHealthSyncHandler(t, src, dst)
		synctest.Wait()
		if c := checkStatus(t, mon, "sync.validate[sync-test]"); c.State != health.StateUnchecked || c.Schedule != health.ScheduleOff {
			t.Errorf("before running, validate = %v, schedule %q; want unchecked, off", c.State, c.Schedule)
		}

		c := runCheck(t, mon, "sync.validate[sync-test]")
		if c.State != health.StateWarning || !strings.Contains(c.Message, "shards had problems") || !strings.Contains(c.Message, "missing from the destination; queued them to copy") {
			t.Errorf("validate = %v %q; want warning about shards with missing blobs", c.State, c.Message)
		}
		if c.Total == 0 || c.Done != c.Total {
			t.Errorf("validate progress = %d/%d; want all shards", c.Done, c.Total)
		}
		// The source only has sha224 blobs, so only those are sharded.
		if want := fmt.Sprintf("of %d: 256 digest prefixes × {sha224}", c.Total); c.Total != 256 || !strings.Contains(c.ProgressHint, want) {
			t.Errorf("validate progress hint = %q; want it to contain %q", c.ProgressHint, want)
		}
		sh.mu.Lock()
		queued := len(sh.needCopy)
		sh.mu.Unlock()
		if queued != 2 {
			t.Errorf("queued %d blobs to copy; want 2", queued)
		}

		// The sync page's button runs the same check.
		uploadBlobs(t, dst, "b", "c")
		sh.startFullValidation()
		synctest.Wait()
		if c := checkStatus(t, mon, "sync.validate[sync-test]"); c.State != health.StateOK {
			t.Errorf("after copying the rest, validate = %v %q; want ok", c.State, c.Message)
		}
	})
}

// blockingEnum is a memory.Storage whose listing doesn't end until it's
// canceled, like a slow listing of a big storage.
type blockingEnum struct{ *memory.Storage }

func (blockingEnum) EnumerateBlobs(ctx context.Context, dest chan<- blob.SizedRef, after string, limit int) error {
	defer close(dest)
	<-ctx.Done()
	return ctx.Err()
}

// TestSyncValidateCancel checks that canceling a validation whose
// listings are in progress stops it.
func TestSyncValidateCancel(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		src := new(memory.Storage)
		sh := newSyncHandler("/src/", "/dst/", blockingEnum{src}, blockingEnum{new(memory.Storage)}, sorted.NewMemoryKeyValue())
		mon := health.New()
		defer mon.Close()
		sh.registerChecks(mon, "/sync-test/", false, false, 0)
		mon.EndStartup()
		if err := mon.RunNow("sync.validate[sync-test]"); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		if c := checkStatus(t, mon, "sync.validate[sync-test]"); c.State != health.StateRunning {
			t.Fatalf("validate = %v; want running", c.State)
		}
		if err := mon.Cancel("sync.validate[sync-test]"); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		if c := checkStatus(t, mon, "sync.validate[sync-test]"); c.State != health.StateCanceled {
			t.Errorf("after Cancel, validate = %v %q; want canceled", c.State, c.Message)
		}
		if c := checkStatus(t, mon, "sync.validate[sync-test]"); !c.Paused || c.PassSince.IsZero() {
			t.Errorf("after Cancel, pass since %v, paused %v; want a paused pass", c.PassSince, c.Paused)
		}
	})
}

func TestSyncSampleCheck(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		src, dst := new(memory.Storage), new(memory.Storage)
		uploadBlobs(t, src, "a", "b", "c", "d")
		uploadBlobs(t, dst, "a")
		sh, mon := newHealthSyncHandler(t, src, dst)

		// Wherever the sample starts, it wraps around to see them all.
		c := runCheck(t, mon, "sync.sample[sync-test]")
		if c.State != health.StateWarning || !strings.Contains(c.Message, "1 of 4 source blobs") || !strings.Contains(c.Message, "(25.0%)") {
			t.Errorf("sample = %v %q; want warning that 1 of 4 (25.0%%) are there", c.State, c.Message)
		}
		sh.mu.Lock()
		queued := len(sh.needCopy)
		sh.mu.Unlock()
		if queued != 3 {
			t.Errorf("queued %d blobs to copy; want 3", queued)
		}

		uploadBlobs(t, dst, "b", "c", "d")
		if c := runCheck(t, mon, "sync.sample[sync-test]"); c.State != health.StateOK || !strings.Contains(c.Message, "all 4 source blobs") {
			t.Errorf("sample = %v %q; want ok, all 4 there", c.State, c.Message)
		}
	})
}

// corruptFetcher is a memory.Storage that serves the wrong contents of
// every blob.
type corruptFetcher struct{ *memory.Storage }

func (s corruptFetcher) Fetch(ctx context.Context, br blob.Ref) (io.ReadCloser, uint32, error) {
	rc, size, err := s.Storage.Fetch(ctx, br)
	if err != nil {
		return nil, 0, err
	}
	rc.Close()
	return io.NopCloser(strings.NewReader(strings.Repeat("x", int(size)))), size, nil
}

func TestSyncCompareCheck(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		src, dst := new(memory.Storage), new(memory.Storage)
		uploadBlobs(t, src, "a", "b")
		uploadBlobs(t, dst, "a", "b")
		_, mon := newHealthSyncHandler(t, src, dst)
		// Wherever the round starts, there may be no blobs after it, so
		// it just mustn't fail.
		if c := runCheck(t, mon, "sync.compare[sync-test]"); c.State != health.StateOK {
			t.Errorf("compare = %v %q; want ok", c.State, c.Message)
		}

		// Compare rounds start at a random point, so use enough blobs
		// that the round almost surely sees one.
		src, dst = new(memory.Storage), new(memory.Storage)
		for i := range 200 {
			s := strings.Repeat("blob ", i+1)
			uploadBlobs(t, src, s)
			uploadBlobs(t, dst, s)
		}
		_, mon = newHealthSyncHandler(t, src, corruptFetcher{dst})
		var c health.CheckStatus
		for range 10 {
			if c = runCheck(t, mon, "sync.compare[sync-test]"); c.State == health.StateFailed {
				break
			}
		}
		if c.State != health.StateFailed || !strings.Contains(c.Message, "expected sha") {
			t.Errorf("compare of corrupt copies = %v %q; want failed digest check", c.State, c.Message)
		}
	})
}

func TestSyncCopyCheck(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		src, dst := new(memory.Storage), new(memory.Storage)
		sh, mon := newHealthSyncHandler(t, src, dst)
		sb := (&test.Blob{Contents: "a"}).SizedRef()
		sh.addBlobToCopy(sb)

		cs := sh.newCopyStatus(sb)
		sh.mu.Lock()
		sh.copying[sb.Ref] = cs
		sh.mu.Unlock()
		cs.setError(errors.New("network down"))
		if c := checkStatus(t, mon, "sync.copy[sync-test]"); c.State != health.StateWarning || !strings.Contains(c.Message, "network down") {
			t.Errorf("after a failed copy, copy = %v %q; want warning", c.State, c.Message)
		}

		cs = sh.newCopyStatus(sb)
		sh.mu.Lock()
		sh.copying[sb.Ref] = cs
		sh.mu.Unlock()
		cs.setError(nil)
		if c := checkStatus(t, mon, "sync.copy[sync-test]"); c.State != health.StateOK {
			t.Errorf("after the copy succeeded, copy = %v %q; want ok", c.State, c.Message)
		}
	})
}

// pricedStorage is a memory.Storage with S3-like prices.
type pricedStorage struct{ *memory.Storage }

func (pricedStorage) Pricing() (blobserver.Pricing, bool) {
	return blobserver.Pricing{
		Desc:             "S3",
		ListPer1000:      0.005,
		ListPage:         1000,
		GetPer1000:       0.0004,
		EgressPerGB:      0.09,
		ListBytesPerBlob: 200,
	}, true
}

func TestSyncCostEstimates(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		src := new(memory.Storage)
		_, mon := newHealthSyncHandler(t, src, pricedStorage{new(memory.Storage)})
		if c := checkStatus(t, mon, "sync.validate[sync-test]"); !strings.Contains(c.Cost, "unknown until the storage census has counted the blobs in") {
			t.Errorf("validate cost before the census = %q; want unknown", c.Cost)
		}

		// 6.9M sha1 blobs, the source's only type, is 26953 per shard:
		// 27 listing requests for each of the 256 sha1 shards, or 6912
		// requests in all ($0.035), and 1.38 GB of listing responses
		// ($0.124).
		for _, pfx := range []string{"/src/", "/dst/"} {
			mon.Register(blobserver.CensusID(pfx), "census").SetDetails([]blobserver.HashEstimate{
				{Hash: "sha1", Blobs: 6_900_000, Bytes: 330 << 30},
			})
		}
		c := checkStatus(t, mon, "sync.validate[sync-test]")
		if want := "a full pass: ≈$0.159: 6.91K listing requests to S3"; !strings.HasPrefix(c.Cost, want) {
			t.Errorf("validate cost = %q; want it to start %q", c.Cost, want)
		}
		// The source is free, so the sample is of its most blobs, all
		// statted at the destination: $0.004.
		if c := checkStatus(t, mon, "sync.sample[sync-test]"); c.Cost != "≈$0.0040: 10.0K fetch or stat requests to S3 ($0.0040)" {
			t.Errorf("sample cost = %q", c.Cost)
		}
		// 16 MiB of blobs averaging about 51 KB is 326 fetches, and the
		// 16 MiB out is $0.0015.
		if c := checkStatus(t, mon, "sync.compare[sync-test]"); !strings.HasPrefix(c.Cost, "a round of 16.0 MiB: ≈$0.0016: 326 fetch or stat requests") {
			t.Errorf("compare cost = %q", c.Cost)
		}
	})
}

func TestWeightedHash(t *testing.T) {
	ests := []blobserver.HashEstimate{
		{Hash: "sha1", Blobs: 3},
		{Hash: "sha224", Blobs: 1},
		{Hash: "sha256", Blobs: 0},
	}
	for r, want := range []string{"sha1", "sha1", "sha1", "sha224", "sha1"} {
		if got := weightedHash(ests, uint64(r)); got != want {
			t.Errorf("weightedHash(%d) = %q; want %q", r, got, want)
		}
	}
	if got := weightedHash(nil, 7); got != blob.HashFuncs()[0] {
		t.Errorf("weightedHash with no blobs = %q; want %q", got, blob.HashFuncs()[0])
	}
}

// TestSyncSampleStartsAmongSourceBlobs checks that samples start among
// the hash types the source has blobs of.
func TestSyncSampleStartsAmongSourceBlobs(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		src, dst := new(memory.Storage), new(memory.Storage)
		for i := range 20 {
			data := fmt.Sprint("sha1 blob ", i)
			br, ok := blob.Parse(fmt.Sprintf("sha1-%x", sha1.Sum([]byte(data))))
			if !ok {
				t.Fatal("can't make a sha1 ref")
			}
			if _, err := blobserver.Receive(t.Context(), src, br, strings.NewReader(data)); err != nil {
				t.Fatal(err)
			}
		}
		_, mon := newHealthSyncHandler(t, src, dst)
		for range 10 {
			c := runCheck(t, mon, "sync.sample[sync-test]")
			if !strings.Contains(c.Message, "from sha1-") {
				t.Fatalf("sample = %q; want it to start among the source's sha1 blobs", c.Message)
			}
		}
	})
}
