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
	"log"
	"slices"
	"sync"
	"time"
)

// ShardFunc checks one shard of a sharded check (see SetShardedRunner).
// It returns a description of any problem it found in the shard, or ""
// if none, or an error if it couldn't check it.
type ShardFunc func(ctx context.Context, shard string) (problem string, err error)

// sharded is the state of a check with a ShardFunc. Its fields are
// guarded by the Monitor's mu.
type sharded struct {
	list        func(context.Context) []string // returns the shards, at the start of each run
	run         ShardFunc
	concurrency int
	// problemState is the state of the check while any shard has a
	// problem: StateWarning, or StateFailed (see FailOnShardProblems).
	problemState State

	shards     []string             // as of the latest run, or from the saved state
	last       map[string]time.Time // when each shard was last checked
	problem    map[string]string    // the problem each shard last had, if any
	inProgress map[string]bool
	// passSince, if non-zero, is when a full pass of all the shards
	// (from RunNow, or at startup) started: until each shard has been
	// checked since then, it's due.
	passSince time.Time
	// paused is whether the full pass since passSince was canceled, so
	// that it doesn't continue until Monitor.Resume.
	paused bool
}

// inPassLocked reports whether a full pass is in progress, and not
// paused.
func (s *sharded) inPassLocked() bool {
	return !s.passSince.IsZero() && !s.paused
}

// dueLocked reports whether shard needs checking at now, with the
// schedule's interval iv: if it's never been checked, or not since the
// full pass in progress started, or not within iv.
func (s *sharded) dueLocked(shard string, now time.Time, iv time.Duration) bool {
	last := s.last[shard]
	switch {
	case last.IsZero():
		// Only due on a schedule, or in a full pass, so that a check
		// that's off doesn't start on its own.
		return iv > 0 || s.inPassLocked()
	case s.inPassLocked() && last.Before(s.passSince):
		return true
	case iv > 0 && now.Sub(last) >= iv:
		return true
	}
	return false
}

// nextDueLocked returns when a shard is next due, at the earliest, or
// the zero time if none will be on its own.
func (s *sharded) nextDueLocked(now time.Time, iv time.Duration) time.Time {
	// A full pass continues until a run finds that it's done, as its
	// shards may be more than are known until a run lists them.
	if s.inPassLocked() || len(s.shards) == 0 && iv > 0 {
		return now
	}
	var next time.Time
	for _, shard := range s.shards {
		if s.dueLocked(shard, now, iv) {
			return now
		}
		if iv > 0 {
			if t := s.last[shard].Add(iv); next.IsZero() || t.Before(next) {
				next = t
			}
		}
	}
	return next
}

// neediestLocked returns the due shard, not in progress, that was
// checked longest ago (never, first), or false if there's none.
func (s *sharded) neediestLocked(now time.Time, iv time.Duration) (string, bool) {
	best, found := "", false
	for _, shard := range s.shards {
		if s.inProgress[shard] || !s.dueLocked(shard, now, iv) {
			continue
		}
		if !found || s.last[shard].Before(s.last[best]) {
			best, found = shard, true
		}
	}
	return best, found
}

// SetShardedRunner gives the check a function that checks one of its
// shards, as returned by shards at the start of each run, which the
// Monitor runs on up to
// concurrency shards at once, always on the shards that most need it.
//
// A shard needs checking if it's never been checked, or not within the
// check's schedule's interval (by default, def's), or not since a full
// pass of all the shards started, which starting the check with RunNow,
// or at startup with ScheduleStartup, does. When each shard was last
// checked, and any problem it had, are saved after each one (see
// UseStore), so a check that's stopped by a restart continues where it
// stopped.
//
// The check warns (or fails; see FailOnShardProblems) while any shard's
// last check found a problem.
func (c *Check) SetShardedRunner(shards func(context.Context) []string, run ShardFunc, concurrency int, def Schedule) {
	if c == nil {
		return
	}
	if concurrency < 1 {
		concurrency = 1
	}
	s := &sharded{
		list:         shards,
		run:          run,
		concurrency:  concurrency,
		problemState: StateWarning,
		last:         make(map[string]time.Time),
		problem:      make(map[string]string),
		inProgress:   make(map[string]bool),
	}
	m := c.m
	m.mu.Lock()
	if sc, ok := m.saved[c.id]; ok {
		// Until a run lists them, the shards are those that were saved.
		for shard, t := range sc.ShardLast {
			s.last[shard] = t
			s.shards = append(s.shards, shard)
		}
		slices.Sort(s.shards)
		for shard, p := range sc.ShardProblem {
			s.problem[shard] = p
		}
		s.passSince = sc.PassSince
		s.paused = sc.Paused
	}
	c.sharded = s
	m.mu.Unlock()
	c.SetRunner(c.runShards, def)
}

// FailOnShardProblems makes the sharded check fail, rather than warn,
// while any of its shards has a problem, for problems that mean data is
// lost or damaged. It must be called after SetShardedRunner.
func (c *Check) FailOnShardProblems() {
	if c == nil {
		return
	}
	c.m.mu.Lock()
	defer c.m.mu.Unlock()
	if c.sharded != nil {
		c.sharded.problemState = StateFailed
	}
}

// startFullPassLocked starts a new full pass of a sharded check's shards,
// in which every shard checked before now is due.
func (c *Check) startFullPassLocked() {
	s := c.sharded
	if s == nil {
		return
	}
	s.paused = false
	// Start it after every check so far, even with a clock that hasn't
	// moved since, so that every shard is due.
	t := time.Now()
	for _, last := range s.last {
		if !last.Before(t) {
			t = last.Add(time.Nanosecond)
		}
	}
	s.passSince = t
	c.m.saveShardsLocked(c, nil)
}

// runShards checks the due shards of a sharded check, neediest first,
// until there are none, or ctx is canceled.
func (c *Check) runShards(ctx context.Context) error {
	m, s := c.m, c.sharded
	shards := s.list(ctx)
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	s.shards = slices.Clone(shards)
	iv := c.runner.scheduleLocked().interval()
	m.mu.Unlock()

	sem := make(chan struct{}, s.concurrency)
	var wg sync.WaitGroup
	for ctx.Err() == nil {
		sem <- struct{}{}
		m.mu.Lock()
		shard, ok := s.neediestLocked(time.Now(), iv)
		if ok {
			s.inProgress[shard] = true
		}
		c.reportShardProgressLocked(iv)
		m.mu.Unlock()
		if !ok {
			<-sem
			break
		}
		wg.Go(func() {
			defer func() { <-sem }()
			problem, err := s.run(ctx, shard)
			m.mu.Lock()
			defer m.mu.Unlock()
			delete(s.inProgress, shard)
			if ctx.Err() != nil {
				return
			}
			if err != nil {
				problem = "couldn't check it: " + err.Error()
			}
			now := time.Now()
			if now.Before(s.passSince) {
				now = s.passSince // so it's not due again in this pass
			}
			s.last[shard] = now
			if problem != "" {
				s.problem[shard] = problem
			} else {
				delete(s.problem, shard)
			}
			m.saveShardsLocked(c, []string{shard})
			c.reportShardProgressLocked(iv)
		})
	}
	wg.Wait()
	if err := ctx.Err(); err != nil {
		m.mu.Lock()
		m.flushShardsLocked()
		m.mu.Unlock()
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if s.inPassLocked() {
		s.passSince = time.Time{}
	}
	m.saveShardsLocked(c, nil)
	m.flushShardsLocked()
	var bad []string
	for _, shard := range s.shards {
		if s.problem[shard] != "" {
			bad = append(bad, shard)
		}
	}
	if len(bad) > 0 {
		c.reportLocked(s.problemState, false, fmt.Errorf("%d of %d shards had problems; %s: %s", len(bad), len(s.shards), bad[0], s.problem[bad[0]]))
		return nil
	}
	checked, oldest := 0, time.Time{}
	for _, shard := range s.shards {
		t := s.last[shard]
		if t.IsZero() {
			continue
		}
		checked++
		if oldest.IsZero() || t.Before(oldest) {
			oldest = t
		}
	}
	c.state, c.fatal = StateOK, false
	switch {
	case checked == 0:
		c.message = fmt.Sprintf("none of %d shards checked", len(s.shards))
	case checked < len(s.shards):
		c.message = fmt.Sprintf("%d of %d shards checked, the oldest %v ago", checked, len(s.shards), time.Since(oldest).Round(time.Second))
	default:
		c.message = fmt.Sprintf("all %d shards checked, the oldest %v ago", len(s.shards), time.Since(oldest).Round(time.Second))
	}
	if s.paused {
		c.message += "; the full pass is paused"
	}
	c.updated = time.Now()
	return nil
}

// reportShardProgressLocked reports how many of a sharded check's shards
// don't need checking.
func (c *Check) reportShardProgressLocked(iv time.Duration) {
	s := c.sharded
	now := time.Now()
	fresh := 0
	for _, shard := range s.shards {
		if !s.dueLocked(shard, now, iv) && !s.inProgress[shard] {
			fresh++
		}
	}
	c.done, c.total = int64(fresh), int64(len(s.shards))
	c.updated = now
}

// shardSaveDelay is how long the state of a sharded check's shards may
// wait to be saved, so that checking many shards quickly doesn't save
// (and with "jsonfile", rewrite) the store for each one. A restart in the
// meantime only means checking those shards again.
const shardSaveDelay = 5 * time.Second

// saveShardsLocked arranges to save the state of the given shards of c,
// and its full pass, within shardSaveDelay, if m has a store.
func (m *Monitor) saveShardsLocked(c *Check, shards []string) {
	if m.store == nil {
		return
	}
	if m.dirtyShards == nil {
		m.dirtyShards = make(map[*Check]map[string]bool)
	}
	dirty := m.dirtyShards[c]
	if dirty == nil {
		dirty = make(map[string]bool)
		m.dirtyShards[c] = dirty
	}
	for _, shard := range shards {
		dirty[shard] = true
	}
	if m.shardSaveTimer == nil {
		m.shardSaveTimer = time.AfterFunc(shardSaveDelay, func() {
			m.mu.Lock()
			defer m.mu.Unlock()
			m.flushShardsLocked()
		})
	}
}

// flushShardsLocked saves the state of the shards that saveShardsLocked
// arranged to save.
func (m *Monitor) flushShardsLocked() {
	if m.shardSaveTimer != nil {
		m.shardSaveTimer.Stop()
		m.shardSaveTimer = nil
	}
	if len(m.dirtyShards) == 0 {
		return
	}
	b := m.store.BeginBatch()
	for c, dirty := range m.dirtyShards {
		s := c.sharded
		for shard := range dirty {
			b.Set(c.id+"/shard/"+shard, formatShardTime(s.last[shard]))
			if p := s.problem[shard]; p != "" {
				b.Set(c.id+"/problem/"+shard, p)
			} else {
				b.Delete(c.id + "/problem/" + shard)
			}
		}
		if s.passSince.IsZero() {
			b.Delete(c.id + "/passSince")
		} else {
			b.Set(c.id+"/passSince", formatShardTime(s.passSince))
		}
		if s.paused {
			b.Set(c.id+"/paused", "true")
		} else {
			b.Delete(c.id + "/paused")
		}
	}
	m.dirtyShards = nil
	if err := m.store.CommitBatch(b); err != nil {
		log.Printf("health: saving the state of shards: %v", err)
	}
}

// formatShardTime formats t, the time a shard was last checked or a
// pass started, compactly: in UTC, to the second.
func formatShardTime(t time.Time) string {
	return t.UTC().Truncate(time.Second).Format(time.RFC3339)
}
