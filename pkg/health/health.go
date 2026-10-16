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

// Package health tracks the state of a server's background checks, such
// as storage integrity checks, in one place.
//
// A server creates one Monitor and hands it to its handlers and storage,
// which each register the checks they run and report their results to
// them. The Monitor summarizes those results for the status page and for
// a readiness endpoint, and puts the server in read-only mode while any
// check has a fatal failure, so the server keeps serving the problem to
// its owner rather than exiting.
//
// All methods on a nil *Monitor or a nil *Check are no-ops, for code
// that runs outside a server, such as tests and command-line tools.
package health

import (
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"perkeep.org/pkg/sorted"
)

// State is the state of a check, or of a Monitor as a whole.
type State int

const (
	// StatePending means the check is registered but hasn't started.
	StatePending State = iota
	// StateUnchecked means the check has never run, and won't until
	// it's asked to: it has a runner, but its schedule is off.
	StateUnchecked
	// StateRunning means the check is running and hasn't found a problem
	// yet in this run.
	StateRunning
	// StateOK means the check's last run found no problem.
	StateOK
	// StateCanceled means the check's last run was canceled before it
	// finished.
	StateCanceled
	// StateWarning means the check found a problem that needs attention,
	// but that the server can keep working with.
	StateWarning
	// StateFailed means the check found a serious problem, or couldn't
	// run.
	StateFailed
)

func (s State) String() string {
	switch s {
	case StatePending:
		return "pending"
	case StateUnchecked:
		return "unchecked"
	case StateRunning:
		return "running"
	case StateOK:
		return "ok"
	case StateCanceled:
		return "canceled"
	case StateWarning:
		return "warning"
	case StateFailed:
		return "failed"
	}
	return fmt.Sprintf("State(%d)", int(s))
}

func (s State) MarshalText() ([]byte, error) { return []byte(s.String()), nil }

// maxRecentErrors is how many of its most recent problems a check keeps.
const maxRecentErrors = 10

// ErrReadOnly is wrapped by the error from Monitor.ReadOnlyError.
var ErrReadOnly = errors.New("server is read-only")

// Monitor tracks a server's checks. Create one with New.
//
// All methods are safe to call on a nil *Monitor. They do nothing, or
// report a healthy server with no checks, and Register returns a nil
// *Check.
type Monitor struct {
	startupDone chan struct{} // closed by EndStartup
	closed      chan struct{} // closed by Close
	closeOnce   sync.Once
	runners     sync.WaitGroup // of the checks' scheduling goroutines

	mu       sync.Mutex
	checks   map[string]*Check
	starting bool                  // until EndStartup
	store    sorted.KeyValue       // or nil, to not save state; see UseStore
	saved    map[string]savedCheck // by check ID, as loaded by UseStore
	// dirtyShards are the shards, by check, whose state is to be saved
	// when shardSaveTimer fires; see saveShardsLocked.
	dirtyShards    map[*Check]map[string]bool
	shardSaveTimer *time.Timer
}

// New returns a Monitor for a server that is starting.
func New() *Monitor {
	return &Monitor{
		startupDone: make(chan struct{}),
		closed:      make(chan struct{}),
		checks:      make(map[string]*Check),
		starting:    true,
		saved:       make(map[string]savedCheck),
	}
}

// EndStartup records that the server has finished starting. Checks with
// runners (see Check.SetRunner) don't run until then.
func (m *Monitor) EndStartup() {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.starting {
		m.starting = false
		close(m.startupDone)
	}
}

// Close cancels any running checks, and waits for them to stop.
func (m *Monitor) Close() error {
	if m == nil {
		return nil
	}
	m.closeOnce.Do(func() { close(m.closed) })
	m.mu.Lock()
	for _, c := range m.checks {
		if r := c.runner; r != nil && r.cancel != nil {
			r.cancel()
		}
	}
	m.mu.Unlock()
	m.runners.Wait()
	m.mu.Lock()
	if m.store != nil {
		m.flushShardsLocked()
	}
	m.mu.Unlock()
	return nil
}

// compareIDs orders check IDs (see ID) by their name's family, the part
// before its first ".", such as "sync"; then by the prefix they're for;
// then by the rest.
func compareIDs(a, b string) int {
	key := func(id string) (family, prefix string) {
		name, rest, _ := strings.Cut(id, "[")
		family, _, _ = strings.Cut(name, ".")
		return family, strings.TrimSuffix(rest, "]")
	}
	fa, pa := key(a)
	fb, pb := key(b)
	if c := strings.Compare(fa, fb); c != 0 {
		return c
	}
	if c := strings.Compare(pa, pb); c != 0 {
		return c
	}
	return strings.Compare(a, b)
}

// ID returns the conventional ID of a check: name, which says what code
// runs the check and which check it is, as a dotted path such as
// "storage.blobpacked.large-integrity", then in brackets the handler
// prefix it runs for, without slashes, such as "[bs]" for "/bs/".
func ID(name, prefix string) string {
	return name + "[" + strings.Trim(prefix, "/") + "]"
}

// Register adds a check with the given ID and returns it, for its owner
// to report to. The ID must be unique and stay the same across restarts;
// see ID for the convention. The title is a short human-readable
// description.
//
// Register panics if a check with the same ID is already registered.
func (m *Monitor) Register(id, title string) *Check {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, dup := m.checks[id]; dup {
		panic(fmt.Sprintf("health: check %q registered twice", id))
	}
	c := &Check{m: m, id: id, title: title}
	m.checks[id] = c
	return c
}

// Check is a check registered with a Monitor, for its owner to report
// its progress and results to.
//
// All methods are safe to call on a nil *Check, and do nothing.
type Check struct {
	m     *Monitor
	id    string
	title string

	// The rest is guarded by m.mu.
	state     State
	fatal     bool   // the last failure makes the server read-only
	message   string // describing the state, if not pending or ok
	started   time.Time
	updated   time.Time
	done      int64
	total     int64
	recentErr []Event  // most recent last, at most maxRecentErrors
	runner    *runner  // or nil, if the check has no runner
	sharded   *sharded // or nil, if the check isn't sharded; see SetShardedRunner
	hint      string   // see SetProgressHint
	details   any      // see SetDetails
}

// Event is a problem a check reported.
type Event struct {
	Time    time.Time `json:"time"`
	Message string    `json:"message"`
}

// Start records that the check started a run, and clears its progress.
// A fatal failure from an earlier run keeps the server read-only until
// the check reports again.
func (c *Check) Start() {
	if c == nil {
		return
	}
	c.m.mu.Lock()
	defer c.m.mu.Unlock()
	now := time.Now()
	c.state, c.message = StateRunning, ""
	c.started, c.updated = now, now
	c.done, c.total = 0, 0
}

// Progress records how far along the current run is. The units are up
// to the check, such as blobs or bytes. A total of 0 means unknown.
func (c *Check) Progress(done, total int64) {
	if c == nil {
		return
	}
	c.m.mu.Lock()
	defer c.m.mu.Unlock()
	c.done, c.total = done, total
	c.updated = time.Now()
}

// SetProgressHint sets a description of what the check's progress
// counts (see Progress), such as "768 shards: 256 digest prefixes ×
// {sha1, sha224, sha256}".
func (c *Check) SetProgressHint(hint string) {
	if c == nil {
		return
	}
	c.m.mu.Lock()
	defer c.m.mu.Unlock()
	c.hint = hint
}

// Details returns the details of the check with the given ID (see
// Check.SetDetails), or nil.
func (m *Monitor) Details(id string) any {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if c, ok := m.checks[id]; ok {
		return c.details
	}
	return nil
}

// SetDetails sets structured details of the check's latest result, for
// the status page and its JSON. v must not be changed afterward, and
// should be JSON-encodable.
func (c *Check) SetDetails(v any) {
	if c == nil {
		return
	}
	c.m.mu.Lock()
	defer c.m.mu.Unlock()
	c.details = v
}

// OK records that the check's run finished without finding a problem.
func (c *Check) OK() {
	if c == nil {
		return
	}
	c.m.mu.Lock()
	defer c.m.mu.Unlock()
	c.state, c.fatal, c.message = StateOK, false, ""
	c.updated = time.Now()
}

// OKMessage is like OK, with a message that describes the result.
func (c *Check) OKMessage(msg string) {
	if c == nil {
		return
	}
	c.m.mu.Lock()
	defer c.m.mu.Unlock()
	c.state, c.fatal, c.message = StateOK, false, msg
	c.updated = time.Now()
}

// Warn records that the check found a problem that needs attention, but
// that the server can keep working with.
func (c *Check) Warn(err error) { c.report(StateWarning, false, err) }

// Fail records that the check found a serious problem, or couldn't run.
func (c *Check) Fail(err error) { c.report(StateFailed, false, err) }

// Fatal is like Fail, but for a problem that makes it unsafe to keep
// writing to the server, which goes into read-only mode until the check
// reports OK, a warning, or a failure that isn't fatal.
func (c *Check) Fatal(err error) { c.report(StateFailed, true, err) }

func (c *Check) report(st State, fatal bool, err error) {
	if c == nil {
		return
	}
	c.m.mu.Lock()
	defer c.m.mu.Unlock()
	c.reportLocked(st, fatal, err)
}

func (c *Check) reportLocked(st State, fatal bool, err error) {
	msg := "unknown error"
	if err != nil {
		msg = err.Error()
	}
	now := time.Now()
	c.state, c.fatal, c.message = st, fatal, msg
	c.updated = now
	if len(c.recentErr) == maxRecentErrors {
		c.recentErr = slices.Delete(c.recentErr, 0, 1)
	}
	c.recentErr = append(c.recentErr, Event{Time: now, Message: msg})
}

// CheckStatus is a snapshot of a check's state.
type CheckStatus struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	State State  `json:"state"`
	// Fatal is whether the failure puts the server in read-only mode.
	Fatal   bool      `json:"fatal,omitempty"`
	Message string    `json:"message,omitempty"`
	Started time.Time `json:"started,omitzero"` // of the current or last run
	Updated time.Time `json:"updated,omitzero"`
	Done    int64     `json:"done,omitempty"`
	Total   int64     `json:"total,omitempty"`
	// ProgressHint describes what Done and Total count, if set.
	ProgressHint string `json:"progressHint,omitempty"`
	// Details are structured details of the check's latest result, if
	// any (see Check.SetDetails).
	Details any `json:"details,omitempty"`
	// RecentErrors are the check's most recent problems, most recent
	// first, including ones from earlier runs.
	RecentErrors []Event `json:"recentErrors,omitempty"`

	// The rest are only set for a check with a runner.
	Runnable bool `json:"runnable,omitempty"`
	// Schedule is how often it runs, and DefaultSchedule how often it
	// runs unless Schedule was changed (see Monitor.SetSchedule).
	Schedule        Schedule  `json:"schedule,omitempty"`
	DefaultSchedule Schedule  `json:"defaultSchedule,omitempty"`
	LastRun         time.Time `json:"lastRun,omitzero"` // when a run last finished
	// For a sharded check, PassSince is when its full pass in progress
	// started, if any, and Paused whether it was canceled (see
	// Monitor.Resume).
	Sharded   bool      `json:"sharded,omitempty"`
	PassSince time.Time `json:"passSince,omitzero"`
	Paused    bool      `json:"paused,omitempty"`
	NextRun   time.Time `json:"nextRun,omitzero"` // when it's next scheduled to start
}

// Status is a snapshot of a Monitor's state.
type Status struct {
	// State is the worst state of any check, or StateOK if none is
	// warning or failed.
	State    State `json:"state"`
	Starting bool  `json:"starting,omitempty"`
	ReadOnly bool  `json:"readOnly,omitempty"`
	// Checks are sorted by the family of their IDs (see ID), then by
	// what they're for, so that, for example, a handler's checks are
	// next to each other.
	Checks []CheckStatus `json:"checks"`
}

// Status returns a snapshot of the state of m and its checks.
func (m *Monitor) Status() Status {
	if m == nil {
		return Status{State: StateOK}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	st := Status{State: StateOK, Starting: m.starting}
	for _, c := range m.checks {
		cs := CheckStatus{
			ID:      c.id,
			Title:   c.title,
			State:   c.state,
			Fatal:   c.fatal,
			Message: c.message,
			Started: c.started,
			Updated: c.updated,
			Done:    c.done,
			Total:   c.total,

			ProgressHint: c.hint,
			Details:      c.details,
		}
		for _, ev := range slices.Backward(c.recentErr) {
			cs.RecentErrors = append(cs.RecentErrors, ev)
		}
		if r := c.runner; r != nil {
			cs.Runnable = true
			cs.Schedule, cs.DefaultSchedule = r.scheduleLocked(), r.defaultSchedule
			cs.LastRun, cs.NextRun = r.lastRun, r.nextRunLocked()
			if s := c.sharded; s != nil {
				cs.Sharded, cs.PassSince, cs.Paused = true, s.passSince, s.paused
			}
			if cs.State == StatePending && cs.NextRun.IsZero() && r.cancel == nil {
				cs.State = StateUnchecked
			}
		}
		st.Checks = append(st.Checks, cs)
		if c.fatal {
			st.ReadOnly = true
		}
		if c.state >= StateWarning && c.state > st.State {
			st.State = c.state
		}
	}
	slices.SortFunc(st.Checks, func(a, b CheckStatus) int { return compareIDs(a.ID, b.ID) })
	return st
}

// ReadOnlyError returns an error wrapping ErrReadOnly that says why, if
// any check has a fatal failure, and otherwise nil.
func (m *Monitor) ReadOnlyError() error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var why []string
	for _, c := range m.checks {
		if c.fatal {
			why = append(why, fmt.Sprintf("%s (%s): %s", c.title, c.id, c.message))
		}
	}
	if len(why) == 0 {
		return nil
	}
	slices.Sort(why)
	return fmt.Errorf("%w: %s", ErrReadOnly, strings.Join(why, "; "))
}

// ServeHealthz serves a summary of m's status for monitoring, without
// any details, so it can be served without authentication. The body is
// a single word: "starting", "ok", "warning", "failed", or "read-only".
// The status code is 200 for "ok" and "warning", and 503 otherwise.
func (m *Monitor) ServeHealthz(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" && r.Method != "HEAD" {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	st := m.Status()
	word, code := st.State.String(), http.StatusOK
	switch {
	case st.Starting:
		word, code = "starting", http.StatusServiceUnavailable
		w.Header().Set("Retry-After", "5")
	case st.ReadOnly:
		word, code = "read-only", http.StatusServiceUnavailable
	case st.State == StateFailed:
		code = http.StatusServiceUnavailable
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	fmt.Fprintln(w, word)
}
