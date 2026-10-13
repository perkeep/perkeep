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

package sorted_test

import (
	"errors"
	"fmt"
	"iter"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"

	"tailscale.com/types/result"

	"perkeep.org/pkg/sorted"
	"perkeep.org/pkg/sorted/sqlite"
)

// observedKV records how many rows are read before each iterator closes.
// Checking active before loop-body reentry makes a regression fail rather than
// hanging forever on SQLite's single-slot gate.
type observedKV struct {
	sorted.KeyValue
	active int
	starts []string
	ends   []string
	pages  []int
}

func (kv *observedKV) Find(start, end string) sorted.Iterator {
	kv.starts = append(kv.starts, start)
	kv.ends = append(kv.ends, end)
	it := kv.KeyValue.Find(start, end)
	kv.active++
	return &observedIterator{Iterator: it, kv: kv}
}

type observedIterator struct {
	sorted.Iterator
	kv     *observedKV
	rows   int
	closed bool
}

func (it *observedIterator) Next() bool {
	if !it.Iterator.Next() {
		return false
	}
	it.rows++
	return true
}

func (it *observedIterator) Close() error {
	err := it.Iterator.Close()
	if !it.closed {
		it.closed = true
		it.kv.active--
		it.kv.pages = append(it.kv.pages, it.rows)
	}
	return err
}

func setNumberedPairs(t *testing.T, kv sorted.KeyValue, n int) {
	t.Helper()
	b := kv.BeginBatch()
	for i := 0; i < n; i++ {
		key := fmt.Sprintf("k%04d", i)
		b.Set(key, "value:"+key)
	}
	if err := kv.CommitBatch(b); err != nil {
		t.Fatal(err)
	}
}

func TestScanPagedPages(t *testing.T) {
	for _, n := range []int{0, 1, 999, 1000, 1001, 2000, 2001, 2505} {
		t.Run(strconv.Itoa(n), func(t *testing.T) {
			kv := &observedKV{KeyValue: sorted.NewMemoryKeyValue()}
			defer kv.Close()
			setNumberedPairs(t, kv, n)
			visited := 0
			for r := range sorted.ScanPaged(kv, "", "") {
				if kv.active != 0 {
					t.Fatalf("loop body has %d open iterators", kv.active)
				}
				if err := r.Err(); err != nil {
					t.Fatal(err)
				}
				entry := r.MustValue()
				wantKey := fmt.Sprintf("k%04d", visited)
				if entry.Key != wantKey || entry.Value != "value:"+wantKey {
					t.Fatalf("entry %d = %+v, want (%q, %q)", visited, entry, wantKey, "value:"+wantKey)
				}
				visited++
			}
			if visited != n || kv.active != 0 {
				t.Fatalf("visited %d/%d pairs; %d open iterators", visited, n, kv.active)
			}
			// A completely full final page needs one more Find to discover
			// exhaustion. Subsequent pages also read the skipped cursor key.
			if len(kv.pages) != n/1000+1 {
				t.Fatalf("pages = %v, want %d pages", kv.pages, n/1000+1)
			}
			for p, rows := range kv.pages {
				wantRows := min(1000, n-p*1000)
				if p != 0 {
					wantRows++
				}
				if rows != wantRows {
					t.Errorf("page %d read %d rows, want %d", p, rows, wantRows)
				}
				wantStart := ""
				if p != 0 {
					wantStart = fmt.Sprintf("k%04d", p*1000-1)
				}
				if kv.starts[p] != wantStart || kv.ends[p] != "" {
					t.Errorf("page %d range = (%q, %q), want (%q, %q)", p, kv.starts[p], kv.ends[p], wantStart, "")
				}
			}
		})
	}
}

func TestScanPagedLazyAndReusable(t *testing.T) {
	kv := &observedKV{KeyValue: sorted.NewMemoryKeyValue()}
	defer kv.Close()
	setNumberedPairs(t, kv, 2005)
	start, end := "k0002", "k2005"
	// Assert the public API uses a single result value, not Seq2.
	var seq iter.Seq[result.Of[sorted.Entry]] = sorted.ScanPaged(kv, start, end)
	if len(kv.starts) != 0 || kv.active != 0 {
		t.Fatalf("creating sequence opened an iterator: starts=%q, active=%d", kv.starts, kv.active)
	}
	for run, limit := range []int{2003, 1000, 2003, 1, 2003} {
		// Mutations after sequence creation (and between iterations) are
		// visible. Each iteration must restart with a fresh cursor.
		firstValue := fmt.Sprintf("iteration:%d", run)
		if err := kv.Set(start, firstValue); err != nil {
			t.Fatal(err)
		}
		firstFind := len(kv.starts)
		visited := 0
		for r := range seq {
			if kv.active != 0 {
				t.Fatalf("iteration %d has %d open iterators", run, kv.active)
			}
			if err := r.Err(); err != nil {
				t.Fatal(err)
			}
			wantKey := fmt.Sprintf("k%04d", visited+2)
			wantValue := "value:" + wantKey
			if visited == 0 {
				wantValue = firstValue
			}
			if got := r.MustValue(); got != (sorted.Entry{Key: wantKey, Value: wantValue}) {
				t.Fatalf("iteration %d entry %d = %+v, want (%q, %q)", run, visited, got, wantKey, wantValue)
			}
			visited++
			if limit < 2003 && visited == limit {
				break
			}
		}
		wantPages := (limit + 999) / 1000
		if visited != limit || kv.active != 0 || len(kv.starts)-firstFind != wantPages {
			t.Fatalf("iteration %d: visited=%d, active=%d, pages=%d; want %d, 0, %d",
				run, visited, kv.active, len(kv.starts)-firstFind, limit, wantPages)
		}
		if kv.starts[firstFind] != start || kv.ends[firstFind] != end {
			t.Fatalf("iteration %d restarted at (%q, %q), want (%q, %q)",
				run, kv.starts[firstFind], kv.ends[firstFind], start, end)
		}
	}
}

func TestScanPagedRanges(t *testing.T) {
	keys := []string{"", "\x00", "\x00\x00", "\x00\xff", "a", "a\x00", "a\x00\x00", "a\xff", "b", "\x80", "\xff", "\xff\x00", "\xff\xff"}
	kv := sorted.NewMemoryKeyValue()
	defer kv.Close()
	for _, key := range keys {
		if err := kv.Set(key, "value:"+key); err != nil {
			t.Fatal(err)
		}
	}
	for _, bounds := range [][2]string{
		{"", ""}, {"", "\x00"}, {"\x00", "a"}, {"a", "b"},
		{"a\x00", "a\xff"}, {"\xff", ""}, {"\xff\x00", "\xff\xff"},
		{"missing", "missing"}, {"b", "a"}, {"\xff\xff\x00", ""},
	} {
		start, end := bounds[0], bounds[1]
		t.Run(fmt.Sprintf("%q-%q", start, end), func(t *testing.T) {
			var want, got []string
			for _, key := range keys {
				if key >= start && (end == "" || key < end) {
					want = append(want, key)
				}
			}
			for r := range sorted.ScanPaged(kv, start, end) {
				entry, err := r.Value()
				if err != nil {
					t.Fatal(err)
				}
				if entry.Value != "value:"+entry.Key {
					t.Errorf("value for %q = %q", entry.Key, entry.Value)
				}
				got = append(got, entry.Key)
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("keys = %q, want %q", got, want)
			}
		})
	}
}

func TestScanPagedBinaryPageBoundaries(t *testing.T) {
	for _, backend := range []string{"memory", "sqlite"} {
		t.Run(backend, func(t *testing.T) {
			var store sorted.KeyValue
			if backend == "memory" {
				store = sorted.NewMemoryKeyValue()
			} else {
				var err error
				store, err = sqlite.NewStorage(filepath.Join(t.TempDir(), "binary.sqlite"))
				if err != nil {
					t.Fatal(err)
				}
			}
			defer store.Close()
			kv := &observedKV{KeyValue: store}
			var keys []string
			for i := 0; i < 2505; i++ {
				// Include NUL and invalid UTF-8, including in the last key
				// of each page. Prefix extensions must not be skipped.
				key := fmt.Sprintf("k%04d\x00\xff", i)
				keys = append(keys, key, key+"\x00", key+"\x00\x00", key+"\xff")
			}
			keys = append(keys, "", "\x00", "\xff", "\xff\x00")
			sort.Strings(keys)
			b := kv.BeginBatch()
			for _, key := range keys {
				b.Set(key, key)
			}
			if err := kv.CommitBatch(b); err != nil {
				t.Fatal(err)
			}
			// Exercise non-empty bounds over many pages as well.
			start, end := keys[3], keys[len(keys)-2]
			want := keys[3 : len(keys)-2]
			var got []string
			for r := range sorted.ScanPaged(kv, start, end) {
				if err := r.Err(); err != nil {
					t.Fatal(err)
				}
				entry := r.MustValue()
				if entry.Key != entry.Value {
					t.Errorf("value for %q = %q", entry.Key, entry.Value)
				}
				got = append(got, entry.Key)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("visited %d keys, want %d in the same order", len(got), len(want))
			}
			for i, nextStart := range kv.starts {
				wantStart := start
				wantRows := min(1000, len(want)-i*1000)
				if i > 0 {
					wantStart = want[i*1000-1]
					wantRows++
				}
				if nextStart != wantStart || kv.ends[i] != end {
					t.Errorf("page %d range = (%q, %q), want (%q, %q)", i, nextStart, kv.ends[i], wantStart, end)
				}
				if kv.pages[i] != wantRows {
					t.Errorf("page %d read %d rows, want %d", i, kv.pages[i], wantRows)
				}
			}
		})
	}
}

// textBoundsKV models a backend that accepts ordinary string keys but rejects
// NULs in query bounds (as PostgreSQL does), or bounds beyond MaxKeySize.
type textBoundsKV struct {
	sorted.KeyValue
}

func (kv textBoundsKV) Find(start, end string) sorted.Iterator {
	for _, bound := range []string{start, end} {
		if strings.Contains(bound, "\x00") || len(bound) > sorted.MaxKeySize {
			return &failingIterator{
				Iterator: kv.KeyValue.Find("", ""),
				err:      fmt.Errorf("unsupported query bound %q", bound),
			}
		}
	}
	return kv.KeyValue.Find(start, end)
}

func TestScanPagedTextBounds(t *testing.T) {
	for _, keySize := range []int{5, sorted.MaxKeySize} {
		t.Run(strconv.Itoa(keySize), func(t *testing.T) {
			mem := sorted.NewMemoryKeyValue()
			defer mem.Close()
			kv := &observedKV{KeyValue: textBoundsKV{KeyValue: mem}}
			prefix := strings.Repeat("a", keySize-5)
			b := kv.BeginBatch()
			for i := 0; i < 2505; i++ {
				key := prefix + fmt.Sprintf("k%04d", i)
				b.Set(key, "value:"+key)
			}
			if err := kv.CommitBatch(b); err != nil {
				t.Fatal(err)
			}
			visited := 0
			start, end := prefix+"k0007", prefix+"k2010"
			for r := range sorted.ScanPaged(kv, start, end) {
				if err := r.Err(); err != nil {
					t.Fatal(err)
				}
				entry := r.MustValue()
				wantKey := prefix + fmt.Sprintf("k%04d", visited+7)
				if entry.Key != wantKey || entry.Value != "value:"+wantKey {
					t.Fatalf("entry %d = %+v, want (%q, %q)", visited, entry, wantKey, "value:"+wantKey)
				}
				visited++
			}
			if visited != 2003 || kv.active != 0 {
				t.Errorf("visited=%d, active=%d; want 2003, 0", visited, kv.active)
			}
			wantStarts := []string{start, prefix + "k1006", prefix + "k2006"}
			if !reflect.DeepEqual(kv.starts, wantStarts) || !reflect.DeepEqual(kv.pages, []int{1000, 1001, 4}) {
				t.Errorf("starts=%q, pages=%v; want %q, [1000 1001 4]", kv.starts, kv.pages, wantStarts)
			}
		})
	}
}

// failingKV simulates errors reported by Find/Next through Iterator.Close.
type failingKV struct {
	sorted.KeyValue
	finds     int
	failFind  int
	failAfter int
	err       error
}

func (kv *failingKV) Find(start, end string) sorted.Iterator {
	kv.finds++
	it := kv.KeyValue.Find(start, end)
	if kv.finds != kv.failFind {
		return it
	}
	return &failingIterator{Iterator: it, remaining: kv.failAfter, err: kv.err}
}

type failingIterator struct {
	sorted.Iterator
	remaining int
	err       error
}

func (it *failingIterator) Next() bool {
	if it.remaining == 0 {
		return false
	}
	it.remaining--
	return it.Iterator.Next()
}

func (it *failingIterator) Close() error {
	it.Iterator.Close()
	return it.err
}

func TestScanPagedIteratorErrors(t *testing.T) {
	iterErr := errors.New("iterator failed")
	for _, tt := range []struct {
		name        string
		failFind    int
		failAfter   int
		wantEntries int
	}{
		{"find", 1, 0, 0},
		{"partial-page", 1, 5, 5},
		{"full-page-close", 1, 1000, 1000},
		{"later-find", 2, 0, 1000},
		{"later-cursor-only", 2, 1, 1000},
		{"later-page", 2, 5, 1004},
		{"later-full-page-close", 2, 1001, 2000},
	} {
		t.Run(tt.name, func(t *testing.T) {
			mem := sorted.NewMemoryKeyValue()
			defer mem.Close()
			setNumberedPairs(t, mem, 2005)
			kv := &observedKV{KeyValue: &failingKV{
				KeyValue: mem, failFind: tt.failFind, failAfter: tt.failAfter, err: iterErr,
			}}
			entries, errorResults := 0, 0
			for r := range sorted.ScanPaged(kv, "", "") {
				if kv.active != 0 {
					t.Fatalf("loop body has %d open iterators", kv.active)
				}
				entry, err := r.Value()
				if r.Err() != err {
					t.Fatalf("Err() = %v, Value() error = %v", r.Err(), err)
				}
				if err != nil {
					errorResults++
					if err != iterErr {
						t.Errorf("error = %v, want original iterator error", err)
					}
					if entry != (sorted.Entry{}) {
						t.Errorf("error result has entry %+v, want zero Entry", entry)
					}
					if entries != tt.wantEntries {
						t.Errorf("error yielded after %d entries, want %d", entries, tt.wantEntries)
					}
					func() {
						defer func() {
							if got := recover(); got != iterErr {
								t.Errorf("MustValue panic = %v, want original iterator error", got)
							}
						}()
						r.MustValue()
					}()
					// Continue rather than break to detect duplicate errors
					// or successful entries after the terminal error.
					continue
				}
				if errorResults != 0 {
					t.Fatal("successful entry yielded after terminal error")
				}
				wantKey := fmt.Sprintf("k%04d", entries)
				want := sorted.Entry{Key: wantKey, Value: "value:" + wantKey}
				if entry != want || r.MustValue() != want {
					t.Fatalf("entry %d = %+v, want %+v", entries, entry, want)
				}
				entries++
			}
			if entries != tt.wantEntries || errorResults != 1 {
				t.Errorf("got %d entries and %d errors, want %d entries and one error", entries, errorResults, tt.wantEntries)
			}
			if kv.active != 0 || len(kv.starts) != tt.failFind || len(kv.pages) != tt.failFind {
				t.Errorf("iterator state: active=%d, starts=%q, pages=%v", kv.active, kv.starts, kv.pages)
			}
		})
	}
}

func TestScanPagedEarlyBreak(t *testing.T) {
	for _, stopAt := range []int{1, 999, 1000, 1001, 2000, 2005} {
		t.Run(strconv.Itoa(stopAt), func(t *testing.T) {
			kv := &observedKV{KeyValue: sorted.NewMemoryKeyValue()}
			defer kv.Close()
			setNumberedPairs(t, kv, 2005)
			visited := 0
			for r := range sorted.ScanPaged(kv, "", "") {
				if kv.active != 0 {
					t.Fatalf("loop body has %d open iterators", kv.active)
				}
				if err := r.Err(); err != nil {
					t.Fatal(err)
				}
				wantKey := fmt.Sprintf("k%04d", visited)
				if got := r.MustValue(); got != (sorted.Entry{Key: wantKey, Value: "value:" + wantKey}) {
					t.Fatalf("entry %d = %+v", visited, got)
				}
				visited++
				if visited == stopAt {
					break
				}
			}
			if visited != stopAt {
				t.Errorf("visited %d entries, want %d", visited, stopAt)
			}
			wantPages := (stopAt + 999) / 1000
			if kv.active != 0 || len(kv.starts) != wantPages || len(kv.pages) != wantPages {
				t.Errorf("iterator state: active=%d, starts=%q, pages=%v", kv.active, kv.starts, kv.pages)
			}
		})
	}
}

func TestScanPagedEarlyBreakError(t *testing.T) {
	iterErr := errors.New("iterator failed")
	for _, tt := range []struct {
		name      string
		failFind  int
		failAfter int
		stopAt    int // Zero means break at the terminal error.
	}{
		{"mid-partial-page", 1, 5, 1},
		{"end-partial-page", 1, 5, 5},
		{"end-full-page", 1, 1000, 1000},
		{"end-later-page", 2, 5, 1004},
		{"find-error", 1, 0, 0},
		{"partial-page-error", 1, 5, 0},
		{"later-page-error", 2, 5, 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			mem := sorted.NewMemoryKeyValue()
			defer mem.Close()
			setNumberedPairs(t, mem, 2005)
			kv := &observedKV{KeyValue: &failingKV{
				KeyValue: mem, failFind: tt.failFind, failAfter: tt.failAfter, err: iterErr,
			}}
			entries, errorResults := 0, 0
			for r := range sorted.ScanPaged(kv, "", "") {
				if kv.active != 0 {
					t.Fatalf("loop body has %d open iterators", kv.active)
				}
				if err := r.Err(); err != nil {
					if tt.stopAt != 0 {
						t.Fatalf("yielded pending error after break: %v", err)
					}
					if err != iterErr {
						t.Fatalf("error = %v, want original iterator error", err)
					}
					errorResults++
					break
				}
				wantKey := fmt.Sprintf("k%04d", entries)
				if got := r.MustValue(); got != (sorted.Entry{Key: wantKey, Value: "value:" + wantKey}) {
					t.Fatalf("entry %d = %+v", entries, got)
				}
				entries++
				if entries == tt.stopAt {
					break
				}
			}
			wantEntries, wantErrors := tt.stopAt, 0
			if tt.stopAt == 0 {
				wantEntries = (tt.failFind-1)*1000 + tt.failAfter
				if tt.failFind > 1 && tt.failAfter > 0 {
					wantEntries-- // Subsequent pages skip the cursor key.
				}
				wantErrors = 1
			}
			if entries != wantEntries || errorResults != wantErrors {
				t.Errorf("got %d entries and %d errors, want %d and %d", entries, errorResults, wantEntries, wantErrors)
			}
			if kv.active != 0 || len(kv.starts) != tt.failFind || len(kv.pages) != tt.failFind {
				t.Errorf("iterator state: active=%d, starts=%q, pages=%v", kv.active, kv.starts, kv.pages)
			}
		})
	}
}

func TestScanPagedPullStop(t *testing.T) {
	for _, tt := range []struct {
		name         string
		take         int
		pendingError bool
	}{
		{"before-first", 0, false},
		{"mid-page", 1, false},
		{"full-page", 1000, false},
		{"later-page", 1001, false},
		{"before-pending-error", 5, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			mem := sorted.NewMemoryKeyValue()
			defer mem.Close()
			setNumberedPairs(t, mem, 2005)
			var store sorted.KeyValue = mem
			if tt.pendingError {
				store = &failingKV{
					KeyValue: mem, failFind: 1, failAfter: 5, err: errors.New("pending error"),
				}
			}
			kv := &observedKV{KeyValue: store}
			next, stop := iter.Pull(sorted.ScanPaged(kv, "k0002", "k2005"))
			defer stop()
			if len(kv.starts) != 0 {
				t.Fatalf("creating pull iterator opened an iterator: starts=%q", kv.starts)
			}
			for i := 0; i < tt.take; i++ {
				r, ok := next()
				if !ok {
					t.Fatalf("next %d exhausted before stop", i)
				}
				if kv.active != 0 {
					t.Fatalf("next %d has %d open iterators", i, kv.active)
				}
				if err := r.Err(); err != nil {
					t.Fatal(err)
				}
				wantKey := fmt.Sprintf("k%04d", i+2)
				if got := r.MustValue(); got != (sorted.Entry{Key: wantKey, Value: "value:" + wantKey}) {
					t.Fatalf("entry %d = %+v", i, got)
				}
			}
			stop()
			stop() // Pull's stop is idempotent.
			if _, ok := next(); ok {
				t.Fatal("next yielded a result after stop")
			}
			wantPages := (tt.take + 999) / 1000
			if kv.active != 0 || len(kv.starts) != wantPages || len(kv.pages) != wantPages {
				t.Errorf("iterator state: active=%d, starts=%q, pages=%v; want %d closed pages",
					kv.active, kv.starts, kv.pages, wantPages)
			}
		})
	}
}

func TestScanPagedReuseAfterError(t *testing.T) {
	mem := sorted.NewMemoryKeyValue()
	defer mem.Close()
	setNumberedPairs(t, mem, 1001)
	iterErr := errors.New("iterator failed")
	kv := &observedKV{KeyValue: &failingKV{
		KeyValue: mem, failFind: 1, failAfter: 5, err: iterErr,
	}}
	seq := sorted.ScanPaged(kv, "", "")
	for run, wantEntries := range []int{5, 1001} {
		entries, errorResults := 0, 0
		for r := range seq {
			if kv.active != 0 {
				t.Fatalf("loop body has %d open iterators", kv.active)
			}
			if err := r.Err(); err != nil {
				if run != 0 || err != iterErr {
					t.Fatalf("iteration %d error = %v", run, err)
				}
				errorResults++
				continue
			}
			wantKey := fmt.Sprintf("k%04d", entries)
			if got := r.MustValue(); got != (sorted.Entry{Key: wantKey, Value: "value:" + wantKey}) {
				t.Fatalf("iteration %d entry %d = %+v", run, entries, got)
			}
			entries++
		}
		wantErrors := 1 - run
		if entries != wantEntries || errorResults != wantErrors {
			t.Errorf("iteration %d got %d entries and %d errors, want %d and %d",
				run, entries, errorResults, wantEntries, wantErrors)
		}
	}
	if !reflect.DeepEqual(kv.starts, []string{"", "", "k0999"}) || kv.active != 0 {
		t.Errorf("starts=%q, active=%d; want [\"\" \"\" \"k0999\"], 0", kv.starts, kv.active)
	}
}

func TestScanPagedSQLiteReentry(t *testing.T) {
	store, err := sqlite.NewStorage(filepath.Join(t.TempDir(), "paged.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	setNumberedPairs(t, store, 1001)
	kv := &observedKV{KeyValue: store}
	visited := 0
	for r := range sorted.ScanPaged(kv, "", "") {
		if kv.active != 0 {
			t.Fatalf("loop body has %d open iterators; reentry would deadlock", kv.active)
		}
		if err := r.Err(); err != nil {
			t.Fatal(err)
		}
		entry := r.MustValue()
		key, value := entry.Key, entry.Value
		got, err := kv.Get(key)
		if err != nil {
			t.Fatal(err)
		}
		if got != value {
			t.Fatalf("Get(%q) = %q, want %q", key, got, value)
		}
		// Another iterator must also be safe to acquire from the loop body.
		it := kv.Find(key, key+"\x00")
		found := it.Next() && it.Key() == key && it.Value() == value
		if err := it.Close(); err != nil {
			t.Fatal(err)
		}
		if !found {
			t.Fatalf("loop-body Find did not return %q", key)
		}
		// Exercise write reentry without inserting an endlessly growing tail.
		if err := kv.Set(key, value); err != nil {
			t.Fatal(err)
		}
		visited++
	}
	if visited != 1001 || kv.active != 0 {
		t.Errorf("visited=%d, active=%d; want 1001, 0", visited, kv.active)
	}
}

func TestScanPagedSQLiteErrorReentry(t *testing.T) {
	for _, failAfter := range []int{0, 5, 1000} {
		t.Run(strconv.Itoa(failAfter), func(t *testing.T) {
			store, err := sqlite.NewStorage(filepath.Join(t.TempDir(), "error.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			setNumberedPairs(t, store, 1001)
			iterErr := errors.New("iterator failed")
			kv := &observedKV{KeyValue: &failingKV{
				KeyValue: store, failFind: 1, failAfter: failAfter, err: iterErr,
			}}
			entries, errorResults := 0, 0
			for r := range sorted.ScanPaged(kv, "", "") {
				if kv.active != 0 {
					t.Fatalf("loop body has %d open iterators; reentry would deadlock", kv.active)
				}
				if err := r.Err(); err != nil {
					if err != iterErr {
						t.Fatalf("error = %v, want original iterator error", err)
					}
					errorResults++
					// Check actual SQLite reentry while consuming the error,
					// not just while consuming successful entries.
					got, err := kv.Get("k0000")
					if err != nil {
						t.Fatal(err)
					}
					if got != "value:k0000" {
						t.Fatalf("Get(k0000) = %q", got)
					}
					it := kv.Find("k0000", "k0001")
					found := it.Next() && it.Key() == "k0000"
					if err := it.Close(); err != nil {
						t.Fatal(err)
					}
					if !found {
						t.Fatal("Find from error loop body did not return k0000")
					}
					if err := kv.Set("k0000", got); err != nil {
						t.Fatal(err)
					}
					continue
				}
				wantKey := fmt.Sprintf("k%04d", entries)
				if got := r.MustValue(); got != (sorted.Entry{Key: wantKey, Value: "value:" + wantKey}) {
					t.Fatalf("entry %d = %+v", entries, got)
				}
				entries++
			}
			if entries != failAfter || errorResults != 1 || kv.active != 0 {
				t.Errorf("got %d entries, %d errors, %d open iterators; want %d, 1, 0",
					entries, errorResults, kv.active, failAfter)
			}
			if len(kv.starts) != 2 {
				t.Errorf("opened %d iterators, want one page and one error-loop-body reentry", len(kv.starts))
			}
		})
	}
}

func TestScanPagedNotSnapshot(t *testing.T) {
	kv := &observedKV{KeyValue: sorted.NewMemoryKeyValue()}
	defer kv.Close()
	setNumberedPairs(t, kv, 1001)
	var got []string
	for r := range sorted.ScanPaged(kv, "", "") {
		if kv.active != 0 {
			t.Fatal("iterator still open in loop body")
		}
		if err := r.Err(); err != nil {
			t.Fatal(err)
		}
		entry := r.MustValue()
		key, value := entry.Key, entry.Value
		if len(got) == 0 {
			// Current-page changes are not observed, but changes beyond the
			// page's last key are. Deleting that last key must not disturb
			// the resume cursor.
			for _, pair := range [][2]string{
				{"k0001", "changed in current page"},
				{"k1000", "changed in later page"},
				{"j", "inserted before cursor"},
				{"k0999\x00", "inserted after cursor"},
			} {
				if err := kv.Set(pair[0], pair[1]); err != nil {
					t.Fatal(err)
				}
			}
			if err := kv.Delete("k0999"); err != nil {
				t.Fatal(err)
			}
		}
		wantValue := "value:" + key
		switch key {
		case "k1000":
			wantValue = "changed in later page"
		case "k0999\x00":
			wantValue = "inserted after cursor"
		}
		if value != wantValue {
			t.Fatalf("value for %q = %q, want %q", key, value, wantValue)
		}
		got = append(got, key)
	}
	var want []string
	for i := 0; i < 1000; i++ {
		want = append(want, fmt.Sprintf("k%04d", i))
	}
	want = append(want, "k0999\x00", "k1000")
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("visited %d keys, want %d in the same order", len(got), len(want))
	}
	if !reflect.DeepEqual(kv.starts, []string{"", "k0999"}) || !reflect.DeepEqual(kv.pages, []int{1000, 2}) {
		t.Errorf("starts=%q, pages=%v; want [\"\" \"k0999\"], [1000 2]", kv.starts, kv.pages)
	}
}
