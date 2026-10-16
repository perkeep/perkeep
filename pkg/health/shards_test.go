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

package health

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"perkeep.org/pkg/sorted"
)

// shardLog is a ShardFunc that records the shards it checks, in order,
// and returns the problems in problems, by shard.
type shardLog struct {
	mu       sync.Mutex
	checked  []string
	problems map[string]string
	block    string // a shard to block checking until canceled
}

func (l *shardLog) run(ctx context.Context, shard string) (string, error) {
	if shard == l.block {
		<-ctx.Done()
		return "", ctx.Err()
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.checked = append(l.checked, shard)
	return l.problems[shard], nil
}

func (l *shardLog) take() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	c := l.checked
	l.checked = nil
	return c
}

func abc(context.Context) []string { return []string{"a", "b", "c"} }

func TestShardedRunNow(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m := New()
		defer m.Close()
		l := &shardLog{}
		c := m.Register("test.sharded[x]", "x")
		c.SetShardedRunner(abc, l.run, 1, ScheduleOff)
		m.EndStartup()
		time.Sleep(48 * time.Hour)
		synctest.Wait()
		if got := l.take(); len(got) != 0 {
			t.Fatalf("checked %q with the schedule off; want nothing until asked", got)
		}

		if err := m.RunNow("test.sharded[x]"); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		if got := l.take(); !slices.Equal(got, abc(nil)) {
			t.Errorf("RunNow checked %q; want %q", got, abc(nil))
		}
		cs := m.Status().Checks[0]
		if cs.State != StateOK || !strings.Contains(cs.Message, "all 3 shards checked") || cs.Done != 3 || cs.Total != 3 {
			t.Errorf("after RunNow, check = %v %q, %d/%d; want ok, all 3 checked", cs.State, cs.Message, cs.Done, cs.Total)
		}
	})
}

func TestShardedResumesAfterRestart(t *testing.T) {
	store := sorted.NewMemoryKeyValue()
	synctest.Test(t, func(t *testing.T) {
		m := New()
		if err := m.UseStore(store); err != nil {
			t.Fatal(err)
		}
		l := &shardLog{block: "b"}
		m.Register("test.sharded[x]", "x").SetShardedRunner(abc, l.run, 1, ScheduleOff)
		m.EndStartup()
		if err := m.RunNow("test.sharded[x]"); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		if got := l.take(); !slices.Equal(got, []string{"a"}) {
			t.Fatalf("before the restart, checked %q; want [a], then stuck on b", got)
		}
		m.Close() // as at a restart, in the middle of b

		// The new Monitor finishes the pass on its own, without checking
		// a again.
		time.Sleep(time.Minute)
		m = New()
		defer m.Close()
		if err := m.UseStore(store); err != nil {
			t.Fatal(err)
		}
		l.block = ""
		m.Register("test.sharded[x]", "x").SetShardedRunner(abc, l.run, 1, ScheduleOff)
		m.EndStartup()
		synctest.Wait()
		if got := l.take(); !slices.Equal(got, []string{"b", "c"}) {
			t.Errorf("after the restart, checked %q; want [b c]", got)
		}
		if _, err := store.Get("test.sharded[x]/passSince"); err != sorted.ErrNotFound {
			t.Errorf("after the pass, passSince err = %v; want it deleted", err)
		}
		for _, shard := range abc(nil) {
			if _, err := store.Get("test.sharded[x]/shard/" + shard); err != nil {
				t.Errorf("shard %s's last check isn't saved: %v", shard, err)
			}
		}
	})
}

// TestShardedCancelPausesPass checks that canceling a sharded check
// pauses its full pass, which a restart doesn't continue, but Resume
// does, without checking its shards again, while RunNow starts over.
func TestShardedCancelPausesPass(t *testing.T) {
	store := sorted.NewMemoryKeyValue()
	synctest.Test(t, func(t *testing.T) {
		m := New()
		if err := m.UseStore(store); err != nil {
			t.Fatal(err)
		}
		l := &shardLog{block: "b"}
		m.Register("test.sharded[x]", "x").SetShardedRunner(abc, l.run, 1, ScheduleOff)
		m.EndStartup()
		if err := m.RunNow("test.sharded[x]"); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		if err := m.Cancel("test.sharded[x]"); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		cs := m.Status().Checks[0]
		if cs.State != StateCanceled || !cs.Paused || cs.PassSince.IsZero() {
			t.Errorf("after Cancel, check = %v, paused %v, pass since %v; want canceled and paused", cs.State, cs.Paused, cs.PassSince)
		}
		if got := l.take(); !slices.Equal(got, []string{"a"}) {
			t.Errorf("before Cancel, checked %q; want [a]", got)
		}
		m.Close()

		// After a restart, the paused pass doesn't continue on its own.
		time.Sleep(time.Hour)
		m = New()
		defer m.Close()
		if err := m.UseStore(store); err != nil {
			t.Fatal(err)
		}
		l.block = ""
		m.Register("test.sharded[x]", "x").SetShardedRunner(abc, l.run, 1, ScheduleOff)
		m.EndStartup()
		synctest.Wait()
		if got := l.take(); len(got) != 0 {
			t.Errorf("after a restart, checked %q; want nothing, as the pass is paused", got)
		}
		if cs := m.Status().Checks[0]; !cs.Paused {
			t.Errorf("after a restart, the pass isn't paused")
		}

		// Resume checks the rest.
		if err := m.Resume("test.sharded[x]"); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		if got := l.take(); !slices.Equal(got, []string{"b", "c"}) {
			t.Errorf("Resume checked %q; want [b c]", got)
		}
		if err := m.Resume("test.sharded[x]"); err == nil {
			t.Errorf("Resume with no pass to resume succeeded")
		}

		// RunNow starts over.
		time.Sleep(time.Hour)
		if err := m.RunNow("test.sharded[x]"); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		if got := l.take(); !slices.Equal(got, abc(nil)) {
			t.Errorf("RunNow checked %q; want all", got)
		}
	})
}

func TestShardedNeediestFirst(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		// Shards last checked at different times, all more than a day
		// ago, except c; and d, never checked.
		now := time.Now()
		store := sorted.NewMemoryKeyValue()
		for shard, ago := range map[string]time.Duration{
			"a": 30 * time.Hour,
			"b": 50 * time.Hour,
			"c": time.Hour,
			"e": 26 * time.Hour,
		} {
			if err := store.Set("test.sharded[x]/shard/"+shard, now.Add(-ago).Format(time.RFC3339Nano)); err != nil {
				t.Fatal(err)
			}
		}
		m := New()
		defer m.Close()
		if err := m.UseStore(store); err != nil {
			t.Fatal(err)
		}
		l := &shardLog{}
		shards := func(context.Context) []string { return []string{"a", "b", "c", "d", "e"} }
		m.Register("test.sharded[x]", "x").SetShardedRunner(shards, l.run, 1, ScheduleDaily)
		m.EndStartup()
		synctest.Wait()
		if got, want := l.take(), []string{"d", "b", "a", "e"}; !slices.Equal(got, want) {
			t.Errorf("checked %q; want %q: the never-checked one, then the stalest, and not c", got, want)
		}

		// c is due 23 hours later, and only c.
		time.Sleep(23*time.Hour + time.Minute)
		synctest.Wait()
		if got, want := l.take(), []string{"c"}; !slices.Equal(got, want) {
			t.Errorf("23 hours later, checked %q; want %q", got, want)
		}
	})
}

func TestShardedProblems(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m := New()
		defer m.Close()
		l := &shardLog{problems: map[string]string{"b": "2 blobs missing"}}
		m.Register("test.sharded[x]", "x").SetShardedRunner(abc, l.run, 2, ScheduleOff)
		m.EndStartup()
		if err := m.RunNow("test.sharded[x]"); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		cs := m.Status().Checks[0]
		if cs.State != StateWarning || !strings.Contains(cs.Message, "1 of 3 shards had problems; b: 2 blobs missing") {
			t.Errorf("check = %v %q; want a warning about b", cs.State, cs.Message)
		}

		l.mu.Lock()
		l.problems = nil
		l.mu.Unlock()
		if err := m.RunNow("test.sharded[x]"); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		if cs := m.Status().Checks[0]; cs.State != StateOK {
			t.Errorf("after b checked clean, check = %v %q; want ok", cs.State, cs.Message)
		}
	})
}

// countingKV is a sorted.KeyValue that counts its batch commits.
type countingKV struct {
	sorted.KeyValue
	commits atomic.Int32
}

func (kv *countingKV) CommitBatch(b sorted.BatchMutation) error {
	kv.commits.Add(1)
	return kv.KeyValue.CommitBatch(b)
}

// TestShardedSavesInBatches checks that checking many shards quickly
// saves their state in a few batches, not one each.
func TestShardedSavesInBatches(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		kv := &countingKV{KeyValue: sorted.NewMemoryKeyValue()}
		m := New()
		defer m.Close()
		if err := m.UseStore(kv); err != nil {
			t.Fatal(err)
		}
		var shards []string
		for i := range 50 {
			shards = append(shards, fmt.Sprintf("s%02d", i))
		}
		m.Register("test.sharded[x]", "x").SetShardedRunner(func(context.Context) []string { return shards }, func(context.Context, string) (string, error) {
			time.Sleep(10 * time.Millisecond)
			return "", nil
		}, 1, ScheduleOff)
		m.EndStartup()
		if err := m.RunNow("test.sharded[x]"); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		time.Sleep(time.Minute)
		synctest.Wait()
		if n := kv.commits.Load(); n > 5 {
			t.Errorf("saved %d times for 50 shards checked in half a second; want a few", n)
		}
		v, err := kv.Get("test.sharded[x]/shard/s49")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := time.Parse(time.RFC3339, v); err != nil || !strings.HasSuffix(v, "Z") || strings.Contains(v, ".") {
			t.Errorf("saved shard time %q; want UTC, to the second", v)
		}
	})
}

func TestShardedConcurrency(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m := New()
		defer m.Close()
		var mu sync.Mutex
		running, maxRunning := 0, 0
		var shards []string
		for i := range 20 {
			shards = append(shards, string(rune('a'+i)))
		}
		m.Register("test.sharded[x]", "x").SetShardedRunner(func(context.Context) []string { return shards }, func(ctx context.Context, shard string) (string, error) {
			mu.Lock()
			running++
			maxRunning = max(maxRunning, running)
			mu.Unlock()
			time.Sleep(time.Second)
			mu.Lock()
			running--
			mu.Unlock()
			return "", nil
		}, 4, ScheduleOff)
		m.EndStartup()
		if err := m.RunNow("test.sharded[x]"); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		time.Sleep(time.Minute)
		synctest.Wait()
		if maxRunning != 4 {
			t.Errorf("at most %d shards checked at once; want 4", maxRunning)
		}
		if cs := m.Status().Checks[0]; cs.State != StateOK || cs.Done != 20 {
			t.Errorf("check = %v %q, %d done; want ok, 20 done", cs.State, cs.Message, cs.Done)
		}
	})
}
