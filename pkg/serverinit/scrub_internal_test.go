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
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver"
	"perkeep.org/pkg/blobserver/memory"
	"perkeep.org/pkg/health"
)

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

// gatedStorage is a memory.Storage whose fetches with the numbers in
// stops, counting from 1, wait until their channels are closed.
type gatedStorage struct {
	*memory.Storage

	mu    sync.Mutex
	n     int
	stops map[int]chan struct{}
}

func (s *gatedStorage) Fetch(ctx context.Context, br blob.Ref) (io.ReadCloser, uint32, error) {
	s.mu.Lock()
	s.n++
	stop := s.stops[s.n]
	s.mu.Unlock()
	if stop != nil {
		select {
		case <-stop:
		case <-ctx.Done():
			return nil, 0, ctx.Err()
		}
	}
	return s.Storage.Fetch(ctx, br)
}

// TestScrubProgress checks that a scrub reports its first blob at once,
// with the census's estimate of the storage's blobs, and then, every
// scrubReportInterval, its own estimate, once it's checked more blobs
// than the census sampled, and how far through the shards it is; and
// that a resumed pass counts the blobs checked before it was paused.
func TestScrubProgress(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const blobs = 2000
		sto := &gatedStorage{Storage: new(memory.Storage), stops: make(map[int]chan struct{})}
		for _, n := range []int{2, 1502, 1503, 1505} {
			sto.stops[n] = make(chan struct{})
		}
		for i := range blobs {
			if _, err := blobserver.ReceiveString(t.Context(), sto, fmt.Sprintf("blob %04d", i)); err != nil {
				t.Fatal(err)
			}
		}
		hash := blob.RefFromString("").HashName()
		c := &census{results: map[string]censusResult{
			"/m/": {ests: []blobserver.HashEstimate{{Hash: hash, Blobs: 100, Bytes: 900, BlobsErr: 10, BytesErr: 90}}},
		}}
		mon := health.New()
		defer mon.Close()
		s := &scrub{c: c, prefix: "/m/", sto: sto}
		const id = "storage.scrub[m]"
		s.check = mon.Register(id, "scrub")
		s.check.SetShardedRunner(s.shards, s.scrubShard, 1, health.ScheduleOff)
		mon.EndStartup()
		status := func() health.CheckStatus {
			for _, c := range mon.Status().Checks {
				if c.ID == id {
					return c
				}
			}
			return health.CheckStatus{}
		}
		if err := mon.RunNow(id); err != nil {
			t.Fatal(err)
		}

		synctest.Wait()
		want := "≈0 of 100 blobs (≈900 ±90 B) checked: 0.01%, 0.00% checkpointed; 1 blobs (9 B) in this run"
		if got := status().Message; got != want {
			t.Errorf("after the first blob, message = %q; want %q", got, want)
		}
		close(sto.stops[2])
		synctest.Wait()
		if got := status().Message; got != want {
			t.Errorf("before %v, message = %q; want it unchanged, %q", scrubReportInterval, got, want)
		}

		time.Sleep(scrubReportInterval)
		close(sto.stops[1502])
		synctest.Wait()
		got := status().Message
		m := regexp.MustCompile(`^≈(\S+)K of ≈(\S+)K ±\S+ blobs \(≈\S+ ±\S+ KiB\) checked: (\S+)%, (\S+)% checkpointed; 1502 blobs \(13\.2 KiB\) in this run$`).FindStringSubmatch(got)
		if m == nil {
			t.Fatalf("after %v, message = %q; want the run's own estimate, and progress", scrubReportInterval, got)
		}
		checked, _ := strconv.ParseFloat(m[1], 64)
		est, _ := strconv.ParseFloat(m[2], 64)
		done, _ := strconv.ParseFloat(m[3], 64)
		checkpointed, _ := strconv.ParseFloat(m[4], 64)
		if checked < 1.4 || checked > 1.6 {
			t.Errorf("estimate of %vK blobs checked in %q; want about 1.5K", checked, got)
		}
		if est < 1.9 || est > 2.1 {
			t.Errorf("estimate of %vK blobs in %q; want about 2K", est, got)
		}
		if done < 70 || done > 80 || checkpointed > done || checkpointed < done-100.0/256 {
			t.Errorf("%v%% done, %v%% checkpointed in %q; want about 75%%, and checkpointed within a shard of it", done, checkpointed, got)
		}

		// Pausing the pass at blob 1503, and resuming it, the resumed
		// run's first blob is reported with the pass's shards checked
		// before it, and with the census's estimate again, but only its
		// own blob as in this run.
		if err := mon.Cancel(id); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		if err := mon.Resume(id); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		got = status().Message
		m = regexp.MustCompile(`^≈(\d+) of 100 blobs \(≈900 ±90 B\) checked: (\S+)%, (\S+)% checkpointed; 1 blobs \(9 B\) in this run$`).FindStringSubmatch(got)
		if m == nil {
			t.Fatalf("resumed, message = %q; want the pass's progress, and the run's one blob", got)
		}
		checked, _ = strconv.ParseFloat(m[1], 64)
		resumed, _ := strconv.ParseFloat(m[3], 64)
		if checked < 70 || checked > 80 || resumed > checkpointed || resumed < checkpointed-100.0/256 {
			t.Errorf("resumed, message = %q; want about 75 of 100 blobs checked, and %v%% checkpointed, less any shard that was being checked", got, checkpointed)
		}

		close(sto.stops[1505])
		synctest.Wait()
		if st := status(); st.State != health.StateOK {
			t.Errorf("after the pass, the scrub is %v %q; want ok", st.State, st.Message)
		}
	})
}

func TestScrubCost(t *testing.T) {
	c := &census{results: map[string]censusResult{}}
	s := &scrub{c: c, prefix: "/s3/", sto: pricedStorage{new(memory.Storage)}}
	if got := s.cost(); !strings.HasPrefix(got, "unknown until the storage census") {
		t.Errorf("cost before the census = %q; want unknown", got)
	}

	// 6.9M sha1 blobs of 330 GiB: 27 listing requests for each of 256
	// shards ($0.035) and 1.38 GB of their responses ($0.124), and 6.9M
	// fetches ($2.76) of 354 GB ($31.9).
	c.results["/s3/"] = censusResult{ests: []blobserver.HashEstimate{{Hash: "sha1", Blobs: 6_900_000, Bytes: 330 << 30}}}
	got := s.cost()
	for _, want := range []string{"a full pass: ≈$34.8", "6.91K listing requests", "6.90M fetch or stat requests", "330 GiB transferred out"} {
		if !strings.Contains(got, want) {
			t.Errorf("cost = %q; want it to contain %q", got, want)
		}
	}
}
