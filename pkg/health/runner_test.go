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
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"perkeep.org/pkg/sorted"
)

// counter returns a RunFunc that counts its runs.
func counter(n *atomic.Int32) RunFunc {
	return func(context.Context) error {
		n.Add(1)
		return nil
	}
}

func TestRunnerStartupSchedule(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m := New()
		defer m.Close()
		var n atomic.Int32
		c := m.Register("test.startup[x]", "x")
		c.SetRunner(counter(&n), ScheduleStartup)
		synctest.Wait()
		if got := n.Load(); got != 0 {
			t.Fatalf("ran %d times before EndStartup; want 0", got)
		}
		m.EndStartup()
		synctest.Wait()
		time.Sleep(48 * time.Hour)
		synctest.Wait()
		if got := n.Load(); got != 1 {
			t.Errorf("ran %d times; want once, at startup", got)
		}
		if st := m.Status().Checks[0]; st.State != StateOK || st.LastRun.IsZero() || !st.Runnable {
			t.Errorf("status = %+v; want ok, runnable, with a last run", st)
		}
	})
}

// TestRunnerUnchecked checks that a check that won't run until asked to
// is "unchecked", not "pending", until it runs.
func TestRunnerUnchecked(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m := New()
		defer m.Close()
		var n atomic.Int32
		m.Register("test.off[x]", "x").SetRunner(counter(&n), ScheduleOff)
		m.Register("test.hourly[x]", "x").SetRunner(func(ctx context.Context) error {
			<-ctx.Done() // still running when checked
			return ctx.Err()
		}, ScheduleHourly)
		m.Register("test.plain[x]", "x")
		states := func() map[string]State {
			got := map[string]State{}
			for _, c := range m.Status().Checks {
				got[c.ID] = c.State
			}
			return got
		}
		if got := states(); got["test.off[x]"] != StateUnchecked || got["test.hourly[x]"] != StatePending || got["test.plain[x]"] != StatePending {
			t.Errorf("before startup, states = %v; want off unchecked, hourly and plain pending", got)
		}
		m.EndStartup()
		synctest.Wait()
		if got := states(); got["test.off[x]"] != StateUnchecked || got["test.hourly[x]"] != StateRunning {
			t.Errorf("after startup, states = %v; want off unchecked, hourly running", got)
		}
		if err := m.RunNow("test.off[x]"); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		if got := states(); got["test.off[x]"] != StateOK {
			t.Errorf("after RunNow, off = %v; want ok", got["test.off[x]"])
		}
	})
}

func TestRunnerHourly(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m := New()
		defer m.Close()
		var n atomic.Int32
		m.Register("test.hourly[x]", "x").SetRunner(counter(&n), ScheduleHourly)
		m.EndStartup()
		synctest.Wait()
		if got := n.Load(); got != 1 {
			t.Fatalf("ran %d times right after startup, with no last run; want 1", got)
		}
		time.Sleep(3*time.Hour + time.Minute)
		synctest.Wait()
		if got := n.Load(); got != 4 {
			t.Errorf("ran %d times after 3 hours; want 4", got)
		}
	})
}

func TestRunnerRunNowAndCancel(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m := New()
		defer m.Close()
		started := make(chan bool)
		c := m.Register("test.manual[x]", "x")
		c.SetRunner(func(ctx context.Context) error {
			started <- true
			<-ctx.Done()
			return ctx.Err()
		}, ScheduleOff)
		m.EndStartup()
		synctest.Wait()
		if err := m.Cancel("test.manual[x]"); err == nil {
			t.Errorf("Cancel of a check that isn't running succeeded")
		}

		if err := m.RunNow("test.manual[x]"); err != nil {
			t.Fatal(err)
		}
		<-started
		if st := m.Status().Checks[0]; st.State != StateRunning {
			t.Errorf("state = %v; want running", st.State)
		}
		if err := m.RunNow("test.manual[x]"); err == nil {
			t.Errorf("RunNow of a running check succeeded")
		}
		if err := m.Cancel("test.manual[x]"); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		if st := m.Status().Checks[0]; st.State != StateCanceled || st.Message != "canceled" {
			t.Errorf("after Cancel, check = %v %q; want canceled", st.State, st.Message)
		}
		if st := m.Status(); st.State != StateOK {
			t.Errorf("overall state with a canceled check = %v; want ok", st.State)
		}

		for _, id := range []string{"test.nope[x]"} {
			if err := m.RunNow(id); err == nil {
				t.Errorf("RunNow(%q) succeeded", id)
			}
		}
		m.Register("test.plain[x]", "x")
		if err := m.RunNow("test.plain[x]"); err == nil {
			t.Errorf("RunNow of a check without a runner succeeded")
		}
	})
}

func TestRunnerResults(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m := New()
		defer m.Close()
		failing := m.Register("test.failing[x]", "x")
		failing.SetRunner(func(context.Context) error { return errors.New("broken") }, ScheduleStartup)
		warning := m.Register("test.warning[x]", "x")
		warning.SetRunner(func(context.Context) error {
			warning.Warn(errors.New("hmm"))
			return nil
		}, ScheduleStartup)
		m.EndStartup()
		synctest.Wait()
		got := map[string]State{}
		for _, c := range m.Status().Checks {
			got[c.ID] = c.State
		}
		if got["test.failing[x]"] != StateFailed || got["test.warning[x]"] != StateWarning {
			t.Errorf("states = %v; want failing failed, warning warning", got)
		}
	})
}

func TestRunnerSavedState(t *testing.T) {
	store := sorted.NewMemoryKeyValue()
	synctest.Test(t, func(t *testing.T) {
		m := New()
		if err := m.UseStore(store); err != nil {
			t.Fatal(err)
		}
		var n atomic.Int32
		m.Register("test.saved[x]", "x").SetRunner(counter(&n), ScheduleOff)
		if err := m.SetSchedule("test.saved[x]", "sometimes"); err == nil {
			t.Errorf("SetSchedule with an invalid schedule succeeded")
		}
		if err := m.SetSchedule("test.saved[x]", ScheduleDaily); err != nil {
			t.Fatal(err)
		}
		m.EndStartup()
		synctest.Wait()
		if got := n.Load(); got != 1 {
			t.Errorf("ran %d times after becoming daily with no last run; want 1", got)
		}
		m.Close()
		if v, err := store.Get("test.saved[x]/schedule"); err != nil || v != "daily" {
			t.Errorf("saved schedule = %q, %v; want daily", v, err)
		}
		if v, err := store.Get("test.saved[x]/lastRun"); err != nil || v == "" {
			t.Errorf("saved last run = %q, %v; want a time", v, err)
		}

		// A new Monitor, as after a restart, keeps the schedule, and
		// doesn't run the check again until a day after it last ran.
		time.Sleep(time.Hour)
		m = New()
		defer m.Close()
		if err := m.UseStore(store); err != nil {
			t.Fatal(err)
		}
		m.Register("test.saved[x]", "x").SetRunner(counter(&n), ScheduleOff)
		m.EndStartup()
		synctest.Wait()
		cs := m.Status().Checks[0]
		if cs.Schedule != ScheduleDaily || cs.DefaultSchedule != ScheduleOff {
			t.Errorf("after restart, schedule %q (default %q); want daily (default off)", cs.Schedule, cs.DefaultSchedule)
		}
		if got := n.Load(); got != 1 {
			t.Errorf("after restart, ran %d times in all; want still 1", got)
		}
		if want := cs.LastRun.Add(24 * time.Hour); !cs.NextRun.Equal(want) {
			t.Errorf("next run = %v; want %v, a day after the last", cs.NextRun, want)
		}
		time.Sleep(23 * time.Hour)
		synctest.Wait()
		if got := n.Load(); got != 2 {
			t.Errorf("a day after the last run, ran %d times in all; want 2", got)
		}
	})
}

// TestRunnerResultSurvivesRestart checks that a check's result, as of its
// last run, is restored after a restart.
func TestRunnerResultSurvivesRestart(t *testing.T) {
	store := sorted.NewMemoryKeyValue()
	synctest.Test(t, func(t *testing.T) {
		m := New()
		if err := m.UseStore(store); err != nil {
			t.Fatal(err)
		}
		c := m.Register("test.c[x]", "x")
		c.SetRunner(func(context.Context) error {
			c.Warn(errors.New("3 things are amiss"))
			return nil
		}, ScheduleOff)
		m.EndStartup()
		if err := m.RunNow("test.c[x]"); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		m.Close()

		m = New()
		defer m.Close()
		if err := m.UseStore(store); err != nil {
			t.Fatal(err)
		}
		m.Register("test.c[x]", "x").SetRunner(func(context.Context) error { return nil }, ScheduleOff)
		cs := m.Status().Checks[0]
		if cs.State != StateWarning || cs.Message != "3 things are amiss" || cs.Updated.IsZero() {
			t.Errorf("after a restart, check = %v %q, updated %v; want the warning from before", cs.State, cs.Message, cs.Updated)
		}
	})
}

// TestRunnerScheduleBackToDefault checks that setting a check's schedule
// back to its default removes it from the store.
func TestRunnerScheduleBackToDefault(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := sorted.NewMemoryKeyValue()
		m := New()
		defer m.Close()
		if err := m.UseStore(store); err != nil {
			t.Fatal(err)
		}
		m.Register("test.c[x]", "x").SetRunner(func(context.Context) error { return nil }, ScheduleOff)
		if err := m.SetSchedule("test.c[x]", ScheduleWeekly); err != nil {
			t.Fatal(err)
		}
		if err := m.SetSchedule("test.c[x]", ScheduleOff); err != nil {
			t.Fatal(err)
		}
		if _, err := store.Get("test.c[x]/schedule"); err != sorted.ErrNotFound {
			t.Errorf("after setting the default schedule, stored schedule err = %v; want not found", err)
		}
	})
}
