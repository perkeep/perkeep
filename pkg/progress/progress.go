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

// Package progress tracks the progress of long-running work, such as an
// importer's run, as named bars: each a count of the work done, of items
// or bytes, and of all the work, if that's known, from which it estimates
// how fast the work is going and how long the rest will take.
//
// A Set's bars may be updated from many goroutines, as the work is done,
// and read, as a Snapshot, by another, to show them.
package progress // import "perkeep.org/pkg/progress"

import (
	"fmt"
	"strconv"
	"sync"
	"time"
)

// A Unit is what a Bar counts.
type Unit int

const (
	Items Unit = iota // things, such as files
	Bytes
)

// window is how far back a Bar's rate is measured, so that its estimate
// of the time left follows changes in the rate, rather than the rate
// since the bar started.
const window = 30 * time.Second

// sampleEvery is how often a Bar keeps a sample of its count, for its
// rate.
const sampleEvery = time.Second

// A Set is a set of named progress bars, in the order they were made.
// The zero value is an empty Set.
type Set struct {
	mu   sync.Mutex
	bars []*Bar
}

// Bar returns the bar of s named name, making it, with the title title,
// if s doesn't have it. A title describes the bar's work, such as
// "Uploading photos".
func (s *Set) Bar(name, title string) *Bar {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, b := range s.bars {
		if b.name == name {
			return b
		}
	}
	b := &Bar{s: s, name: name, title: title, start: time.Now()}
	s.bars = append(s.bars, b)
	return b
}

// Snapshot returns the states of s's bars, in the order they were made.
func (s *Set) Snapshot() []Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	sts := make([]Status, len(s.bars))
	for i, b := range s.bars {
		sts[i] = b.statusLocked(now)
	}
	return sts
}

// A Bar is one bar of a Set: a count of work done.
type Bar struct {
	s *Set // whose mu guards the fields below

	name, title, desc string
	unit              Unit
	done, total       int64 // total is zero if unknown
	start, finished   time.Time
	samples           []sample // in time order, within window of the last
}

type sample struct {
	t    time.Time
	done int64
}

// SetDescription sets the bar's description: more about its work, such
// as how many of the files of the bytes it counts are done.
func (b *Bar) SetDescription(desc string) {
	b.s.mu.Lock()
	defer b.s.mu.Unlock()
	b.desc = desc
}

// SetUnit sets what the bar counts. It's Items unless set.
func (b *Bar) SetUnit(u Unit) {
	b.s.mu.Lock()
	defer b.s.mu.Unlock()
	b.unit = u
}

// SetTotal sets the count of all of the bar's work, or zero if that's
// unknown, as it is unless set.
func (b *Bar) SetTotal(total int64) {
	b.s.mu.Lock()
	defer b.s.mu.Unlock()
	b.total = total
}

// Set sets the count of the bar's work done.
func (b *Bar) Set(done int64) {
	b.s.mu.Lock()
	defer b.s.mu.Unlock()
	b.setLocked(done)
}

// Add adds n to the count of the bar's work done.
func (b *Bar) Add(n int64) {
	b.s.mu.Lock()
	defer b.s.mu.Unlock()
	b.setLocked(b.done + n)
}

func (b *Bar) setLocked(done int64) {
	b.done = done
	now := time.Now()
	if n := len(b.samples); n > 0 && now.Sub(b.samples[n-1].t) < sampleEvery {
		return
	}
	b.samples = append(b.samples, sample{now, done})
	// Keep one sample from before the window, to measure across all of it.
	i := 0
	for i+1 < len(b.samples) && now.Sub(b.samples[i+1].t) >= window {
		i++
	}
	b.samples = b.samples[i:]
}

// Finish marks the bar's work finished. If its total is known, its count
// done is set to it.
func (b *Bar) Finish() {
	b.s.mu.Lock()
	defer b.s.mu.Unlock()
	if b.total > 0 {
		b.done = b.total
	}
	b.finished = time.Now()
}

// Status returns the bar's state.
func (b *Bar) Status() Status {
	b.s.mu.Lock()
	defer b.s.mu.Unlock()
	return b.statusLocked(time.Now())
}

func (b *Bar) statusLocked(now time.Time) Status {
	st := Status{
		Name:        b.name,
		Title:       b.title,
		Description: b.desc,
		Unit:        b.unit,
		Done:        b.done,
		Total:       b.total,
		Finished:    !b.finished.IsZero(),
	}
	end := now
	if st.Finished {
		end = b.finished
	}
	st.Elapsed = end.Sub(b.start)
	if st.Finished {
		if s := st.Elapsed.Seconds(); s > 0 {
			st.Rate = float64(b.done) / s
		}
		return st
	}
	// The rate over the window, if there's a second of it, or else since
	// the bar started.
	from := sample{b.start, 0}
	if len(b.samples) > 0 && now.Sub(b.samples[0].t) >= sampleEvery {
		from = b.samples[0]
	}
	if d := now.Sub(from.t).Seconds(); d > 0 {
		st.Rate = float64(b.done-from.done) / d
	}
	if b.total > 0 && st.Rate > 0 && b.done < b.total {
		st.Remaining = time.Duration(float64(b.total-b.done) / st.Rate * float64(time.Second))
		st.ETA = now.Add(st.Remaining)
	}
	return st
}

// Status is the state of a Bar.
type Status struct {
	Name        string
	Title       string
	Description string
	Unit        Unit
	Done        int64
	Total       int64 // or zero, if unknown
	Finished    bool

	Elapsed time.Duration // since the bar was made, until it finished
	Rate    float64       // per second, recently, or overall once finished

	// Remaining is the estimated time left, and ETA when it's estimated
	// to finish, if the total's known and the work is going; or else
	// they're zero.
	Remaining time.Duration
	ETA       time.Time
}

// Fraction returns how much of the work is done, from 0 to 1, or -1 if
// the total's unknown.
func (st Status) Fraction() float64 {
	if st.Total <= 0 {
		return -1
	}
	return min(float64(st.Done)/float64(st.Total), 1)
}

// Detail describes the counts and the estimates of st, such as
// "1.2 GiB of 4.0 GiB (30%), 12.0 MiB/s, about 4m left".
func (st Status) Detail() string {
	s := st.format(st.Done)
	if st.Total > 0 {
		s += " of " + st.format(st.Total) + fmt.Sprintf(" (%.0f%%)", 100*st.Fraction())
	}
	if st.Rate > 0 {
		if st.Unit == Bytes {
			s += ", " + FormatBytes(int64(st.Rate)) + "/s"
		} else {
			s += fmt.Sprintf(", %.1f/s", st.Rate)
		}
	}
	switch {
	case st.Finished:
		s += ", in " + FormatDuration(st.Elapsed)
	case st.Remaining > 0:
		s += ", about " + FormatDuration(st.Remaining) + " left"
	}
	return s
}

func (st Status) format(n int64) string {
	if st.Unit == Bytes {
		return FormatBytes(n)
	}
	return strconv.FormatInt(n, 10)
}

// FormatBytes formats n bytes in binary units, such as "1.5 GiB".
func FormatBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

// FormatDuration formats d roughly, to the second under a minute, then
// to the minute, then to hours and minutes, such as "2h5m".
func FormatDuration(d time.Duration) string {
	switch {
	case d < time.Minute:
		return d.Round(time.Second).String()
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Round(time.Minute)/time.Minute))
	}
	d = d.Round(time.Minute)
	return fmt.Sprintf("%dh%dm", int(d/time.Hour), int(d%time.Hour/time.Minute))
}
