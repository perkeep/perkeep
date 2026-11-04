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

package progress

import (
	"testing"
	"testing/synctest"
	"time"
)

func TestBar(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var s Set
		b := s.Bar("upload", "Uploading")
		if s.Bar("upload", "other") != b {
			t.Fatal("Bar of the same name made another")
		}
		b.SetUnit(Bytes)
		b.SetTotal(1000)
		// 10 bytes a second for a minute.
		for range 60 {
			time.Sleep(time.Second)
			b.Add(10)
		}
		st := b.Status()
		if st.Done != 600 || st.Rate != 10 || st.Remaining != 40*time.Second || !st.ETA.Equal(time.Now().Add(40*time.Second)) {
			t.Errorf("after a minute: %+v", st)
		}
		if got, want := st.Detail(), "600 B of 1000 B (60%), 10 B/s, about 40s left"; got != want {
			t.Errorf("Detail = %q; want %q", got, want)
		}
		// Then 20 bytes a second: the rate follows, over the window.
		for range 15 {
			time.Sleep(time.Second)
			b.Add(20)
		}
		if st := b.Status(); st.Rate != 15 {
			t.Errorf("rate over the window of half 10/s and half 20/s = %v; want 15", st.Rate)
		}
		b.Finish()
		st = b.Status()
		if !st.Finished || st.Done != 1000 || st.Remaining != 0 || st.Elapsed != 75*time.Second {
			t.Errorf("finished: %+v", st)
		}
	})
}

// TestSkip checks that work skipped, such as items done by an earlier run,
// counts toward a bar's done, but not its rate or estimate of the time
// left.
func TestSkip(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var s Set
		b := s.Bar("activities", "Importing activities")
		b.SetTotal(1000)
		b.Skip(400) // at once, as a resumed run skips what's done
		for range 30 {
			time.Sleep(time.Second)
			b.Add(10)
		}
		st := b.Status()
		if st.Done != 700 || st.Skipped != 400 || st.Rate != 10 || st.Remaining != 30*time.Second {
			t.Errorf("status = %+v; want 700 done, 400 skipped, at 10/s, 30s left", st)
		}
		if got, want := st.Detail(), "700 of 1000 (70%), 400 skipped, 10.0/s, about 30s left"; got != want {
			t.Errorf("Detail = %q; want %q", got, want)
		}
	})
}

// TestStop checks that a bar stopped short of finishing, as when its run
// is paused, keeps its count, without an estimate, and isn't finished.
func TestStop(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var s Set
		a := s.Bar("a", "Importing")
		a.SetTotal(10)
		done := s.Bar("b", "Checking")
		done.SetTotal(1)
		time.Sleep(time.Second)
		a.Set(4)
		done.Finish()
		s.StopAll()
		st := a.Status()
		if st.Finished || !st.Stopped || st.Done != 4 || st.Remaining != 0 || st.Elapsed != time.Second {
			t.Errorf("stopped status = %+v", st)
		}
		if got, want := st.Detail(), "4 of 10 (40%), 4.0/s, stopped after 1s"; got != want {
			t.Errorf("Detail = %q; want %q", got, want)
		}
		if st := done.Status(); !st.Finished || st.Stopped {
			t.Errorf("StopAll changed a finished bar: %+v", st)
		}
	})
}

func TestUnknownTotal(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var s Set
		b := s.Bar("scan", "Scanning")
		time.Sleep(2 * time.Second)
		b.Set(5)
		st := s.Snapshot()[0]
		if st.Fraction() != -1 || st.Remaining != 0 || st.Rate != 2.5 {
			t.Errorf("status = %+v", st)
		}
		if got, want := st.Detail(), "5, 2.5/s"; got != want {
			t.Errorf("Detail = %q; want %q", got, want)
		}
	})
}

func TestFormatDuration(t *testing.T) {
	for _, tt := range []struct {
		d    time.Duration
		want string
	}{
		{1400 * time.Millisecond, "1s"},
		{59 * time.Second, "59s"},
		{90 * time.Second, "2m"},
		{2*time.Hour + 5*time.Minute + 20*time.Second, "2h5m"},
	} {
		if got := FormatDuration(tt.d); got != tt.want {
			t.Errorf("FormatDuration(%v) = %q; want %q", tt.d, got, tt.want)
		}
	}
}
