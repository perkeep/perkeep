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
	"errors"
	"fmt"
	"log"
	"slices"
	"strings"
	"time"

	"perkeep.org/pkg/sorted"
)

// Schedule is how often a check with a runner runs on its own.
type Schedule string

const (
	ScheduleOff     Schedule = "off"     // only when asked to (see Monitor.RunNow)
	ScheduleStartup Schedule = "startup" // once each time the server starts
	ScheduleHourly  Schedule = "hourly"
	ScheduleDaily   Schedule = "daily"
	ScheduleWeekly  Schedule = "weekly"
	ScheduleMonthly Schedule = "monthly"
)

// Schedules are the valid schedules, from least to most often.
var Schedules = []Schedule{ScheduleOff, ScheduleMonthly, ScheduleWeekly, ScheduleDaily, ScheduleHourly, ScheduleStartup}

// interval returns how long s waits between the end of one run and the
// start of the next, or 0 if it doesn't run periodically.
func (s Schedule) interval() time.Duration {
	switch s {
	case ScheduleHourly:
		return time.Hour
	case ScheduleDaily:
		return 24 * time.Hour
	case ScheduleWeekly:
		return 7 * 24 * time.Hour
	case ScheduleMonthly:
		return 30 * 24 * time.Hour
	}
	return 0
}

// RunFunc runs one pass of a check. It reports its progress and any
// problems it finds to the check, and returns when it's done or ctx is
// canceled. If it returns an error, the check fails with it; if it
// returns nil without reporting a result, the check is OK.
type RunFunc func(ctx context.Context) error

// runner is the state of a check with a RunFunc. Its fields are guarded
// by the Monitor's mu.
type runner struct {
	check           *Check
	run             RunFunc
	defaultSchedule Schedule
	schedule        Schedule // if set by SetSchedule, instead of defaultSchedule
	lastRun         time.Time
	cancel          context.CancelFunc // of the run in progress, or nil
	runNow          chan struct{}      // buffered; asks the scheduler to run
	changed         chan struct{}      // buffered; tells the scheduler the schedule changed
}

func (r *runner) scheduleLocked() Schedule {
	if r.schedule != "" {
		return r.schedule
	}
	return r.defaultSchedule
}

// nextRunLocked returns when the check is next scheduled to run, or the
// zero time if it only runs when asked to, or is running.
func (r *runner) nextRunLocked() time.Time {
	iv := r.scheduleLocked().interval()
	if r.cancel != nil {
		return time.Time{}
	}
	if iv == 0 {
		return time.Time{}
	}
	if r.lastRun.IsZero() {
		return time.Now()
	}
	return r.lastRun.Add(iv)
}

// savedCheck is the state of a check that UseStore loads and the Monitor
// saves.
type savedCheck struct {
	Schedule Schedule // if changed from the default
	LastRun  time.Time

	// The check's result, as of its last run.
	State   State
	Message string
	Updated time.Time
}

// SetRunner gives the check a function that runs it, which the Monitor
// calls on the check's schedule (by default, def), or when asked to by
// RunNow, but never more than one at a time, and not before EndStartup.
func (c *Check) SetRunner(run RunFunc, def Schedule) {
	if c == nil {
		return
	}
	if !slices.Contains(Schedules, def) {
		panic(fmt.Sprintf("health: invalid schedule %q", def))
	}
	m := c.m
	m.mu.Lock()
	if c.runner != nil {
		m.mu.Unlock()
		panic(fmt.Sprintf("health: check %q given a runner twice", c.id))
	}
	r := &runner{
		check:           c,
		run:             run,
		defaultSchedule: def,
		runNow:          make(chan struct{}, 1),
		changed:         make(chan struct{}, 1),
	}
	if sc, ok := m.saved[c.id]; ok {
		if slices.Contains(Schedules, sc.Schedule) {
			r.schedule = sc.Schedule
		}
		r.lastRun = sc.LastRun
		if savedResultState(sc.State) {
			c.state, c.message, c.updated = sc.State, sc.Message, sc.Updated
		}
	}
	c.runner = r
	m.mu.Unlock()

	m.runners.Add(1)
	go c.schedule(r)
}

// schedule runs the check on its schedule, or when asked to, until the
// Monitor is closed.
func (c *Check) schedule(r *runner) {
	m := c.m
	defer m.runners.Done()
	select {
	case <-m.startupDone:
	case <-m.closed:
		return
	}
	m.mu.Lock()
	atStartup := r.scheduleLocked() == ScheduleStartup
	m.mu.Unlock()
	if atStartup {
		c.runOnce(r)
	}
	for {
		m.mu.Lock()
		next := r.nextRunLocked()
		m.mu.Unlock()
		var timerC <-chan time.Time
		var t *time.Timer
		if !next.IsZero() {
			t = time.NewTimer(time.Until(next))
			timerC = t.C
		}
		run, closed := false, false
		select {
		case <-m.closed:
			closed = true
		case <-r.changed:
		case <-r.runNow:
			run = true
		case <-timerC:
			run = true
		}
		if t != nil {
			t.Stop()
		}
		if closed {
			return
		}
		if run {
			c.runOnce(r)
		}
	}
}

// runOnce runs one pass of the check, unless the Monitor is closed.
func (c *Check) runOnce(r *runner) {
	m := c.m
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.mu.Lock()
	select {
	case <-m.closed:
		m.mu.Unlock()
		return
	default:
	}
	r.cancel = cancel
	// This run is what any RunNow before it asked for.
	select {
	case <-r.runNow:
	default:
	}
	m.mu.Unlock()

	c.Start()
	err := r.run(ctx)

	m.mu.Lock()
	defer m.mu.Unlock()
	r.cancel = nil
	r.lastRun = time.Now()
	switch {
	case ctx.Err() != nil:
		c.reportLocked(StateCanceled, false, errors.New("canceled"))
	case err != nil:
		c.reportLocked(StateFailed, false, err)
	case c.state == StateRunning:
		c.state, c.fatal, c.message = StateOK, false, ""
		c.updated = time.Now()
	}
	m.saveLocked()
}

// savedResultState reports whether st is the state of a finished run,
// which the Monitor saves (see UseStore) and restores after a restart.
func savedResultState(st State) bool {
	switch st {
	case StateOK, StateCanceled, StateWarning, StateFailed:
		return true
	}
	return false
}

// parseState returns the State whose String is s, or StatePending.
func parseState(s string) State {
	for st := StatePending; st <= StateFailed; st++ {
		if st.String() == s {
			return st
		}
	}
	return StatePending
}

// RunNow asks the check with the given ID to run as soon as possible.
func (m *Monitor) RunNow(id string) error {
	r, err := m.runnerFor(id)
	if err != nil {
		return err
	}
	m.mu.Lock()
	running := r.cancel != nil
	m.mu.Unlock()
	if running {
		return fmt.Errorf("check %q is already running", id)
	}
	select {
	case r.runNow <- struct{}{}:
	default: // already asked
	}
	return nil
}

// Cancel cancels the run of the check with the given ID, if it's
// running.
func (m *Monitor) Cancel(id string) error {
	r, err := m.runnerFor(id)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if r.cancel == nil {
		return fmt.Errorf("check %q isn't running", id)
	}
	r.cancel()
	return nil
}

// SetSchedule changes the schedule of the check with the given ID, and
// saves it (see UseStore).
func (m *Monitor) SetSchedule(id string, s Schedule) error {
	if !slices.Contains(Schedules, s) {
		return fmt.Errorf("invalid schedule %q", s)
	}
	r, err := m.runnerFor(id)
	if err != nil {
		return err
	}
	m.mu.Lock()
	r.schedule = s
	if s == r.defaultSchedule {
		r.schedule = ""
	}
	m.saveLocked()
	m.mu.Unlock()
	select {
	case r.changed <- struct{}{}:
	default:
	}
	return nil
}

func (m *Monitor) runnerFor(id string) (*runner, error) {
	if m == nil {
		return nil, errors.New("no health monitor")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.checks[id]
	if !ok {
		return nil, fmt.Errorf("no check %q", id)
	}
	if c.runner == nil {
		return nil, fmt.Errorf("check %q can't be run", id)
	}
	return c.runner, nil
}

// UseStore loads the checks' saved state (their schedules, if changed
// from their defaults, and when they last ran) from kv, and saves it
// there from then on. It must be called before the checks are given
// runners.
//
// The rows' keys are a check's ID, "/", and the name of a field:
// "schedule"; "lastRun" (in RFC 3339 format); and the state, message,
// and time ("updated") of the result of its last run.
func (m *Monitor) UseStore(kv sorted.KeyValue) error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.store = kv
	return sorted.Foreach(kv, func(key, value string) error {
		i := strings.LastIndex(key, "/")
		if i < 0 {
			return nil
		}
		id, field := key[:i], key[i+1:]
		sc := m.saved[id]
		switch field {
		case "schedule":
			sc.Schedule = Schedule(value)
		case "state":
			sc.State = parseState(value)
		case "message":
			sc.Message = value
		case "updated":
			t, err := time.Parse(time.RFC3339Nano, value)
			if err != nil {
				return fmt.Errorf("health: row %q: %w", key, err)
			}
			sc.Updated = t
		case "lastRun":
			t, err := time.Parse(time.RFC3339Nano, value)
			if err != nil {
				return fmt.Errorf("health: row %q: %w", key, err)
			}
			sc.LastRun = t
		default:
			return nil
		}
		m.saved[id] = sc
		return nil
	})
}

// saveLocked saves the state of the checks with runners to m.store, if
// set. It leaves the saved state of checks that aren't registered, such
// as those of handlers that are disabled for now.
func (m *Monitor) saveLocked() {
	if m.store == nil {
		return
	}
	b := m.store.BeginBatch()
	for id, c := range m.checks {
		r := c.runner
		if r == nil {
			continue
		}
		if r.schedule != "" {
			b.Set(id+"/schedule", string(r.schedule))
		} else {
			b.Delete(id + "/schedule")
		}
		if !r.lastRun.IsZero() {
			b.Set(id+"/lastRun", r.lastRun.Format(time.RFC3339Nano))
		}
		if savedResultState(c.state) {
			b.Set(id+"/state", c.state.String())
			b.Set(id+"/updated", c.updated.Format(time.RFC3339Nano))
			if c.message != "" {
				b.Set(id+"/message", c.message)
			} else {
				b.Delete(id + "/message")
			}
		}
	}
	if err := m.store.CommitBatch(b); err != nil {
		log.Printf("health: saving the state of checks: %v", err)
	}
}
