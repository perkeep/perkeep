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

package index

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/sorted"
)

// missingCleanupKV counts cleanup operations and can fail an iterator close,
// batch commit, or individual Set. The tests do not launch an indexing worker,
// so all access to its counters and faults is synchronous.
type missingCleanupKV struct {
	sorted.KeyValue

	missingFinds     []string
	missingCloses    int
	openMissing      int
	begins           int
	commits          [][]string // deleted keys in each attempted batch
	deletes          []string   // standalone Delete calls
	batchWhileOpen   bool
	iteratorCloseErr error
	commitErr        error
	setErr           error
	setErrKey        string
	persistFailedSet bool
	onSet            func(key string)
}

func (s *missingCleanupKV) Set(key, value string) error {
	if s.onSet != nil {
		s.onSet(key)
	}
	if s.setErr != nil && key == s.setErrKey {
		if s.persistFailedSet {
			if err := s.KeyValue.Set(key, value); err != nil {
				return err
			}
		}
		return s.setErr
	}
	return s.KeyValue.Set(key, value)
}

func (s *missingCleanupKV) Delete(key string) error {
	s.deletes = append(s.deletes, key)
	return s.KeyValue.Delete(key)
}

func (s *missingCleanupKV) Find(start, end string) sorted.Iterator {
	it := s.KeyValue.Find(start, end)
	if !strings.HasPrefix(start, "missing|") {
		return it
	}
	s.missingFinds = append(s.missingFinds, start)
	s.openMissing++
	return &missingCleanupIterator{
		Iterator: it,
		store:    s,
		closeErr: s.iteratorCloseErr,
	}
}

func (s *missingCleanupKV) BeginBatch() sorted.BatchMutation {
	s.begins++
	s.batchWhileOpen = s.batchWhileOpen || s.openMissing != 0
	return s.KeyValue.BeginBatch()
}

func (s *missingCleanupKV) CommitBatch(b sorted.BatchMutation) error {
	s.batchWhileOpen = s.batchWhileOpen || s.openMissing != 0
	// The in-memory backend exposes its batch mutations through this method.
	// Keep the original batch intact so its own CommitBatch can accept it.
	var deletes []string
	for _, m := range b.(interface{ Mutations() []sorted.Mutation }).Mutations() {
		if m.IsDelete() {
			deletes = append(deletes, m.Key())
		}
	}
	s.commits = append(s.commits, deletes)
	if s.commitErr != nil {
		return s.commitErr // fail before mutating storage
	}
	return s.KeyValue.CommitBatch(b)
}

func (s *missingCleanupKV) resetCounts() {
	s.missingFinds = nil
	s.missingCloses = 0
	s.begins = 0
	s.commits = nil
	s.deletes = nil
	s.batchWhileOpen = false
}

type missingCleanupIterator struct {
	sorted.Iterator
	store    *missingCleanupKV
	closeErr error
	closed   bool
}

func (it *missingCleanupIterator) Close() error {
	if it.closed {
		return it.closeErr
	}
	it.closed = true
	it.store.openMissing--
	it.store.missingCloses++
	if err := it.Iterator.Close(); err != nil {
		it.closeErr = err
	}
	return it.closeErr
}

func newMissingCleanupIndex(t *testing.T) (*Index, *missingCleanupKV) {
	t.Helper()
	s := &missingCleanupKV{KeyValue: sorted.NewMemoryKeyValue()}
	ix, err := New(s)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := ix.Close(); err != nil {
			t.Error(err)
		}
	})
	s.resetCounts() // exclude index initialization scans
	return ix, s
}

func noteMissingCleanupEdges(t *testing.T, ix *Index, have blob.Ref, deps ...blob.Ref) {
	t.Helper()
	ix.Lock()
	defer ix.Unlock()
	for _, dep := range deps {
		if err := ix.noteNeededLocked(have, dep); err != nil {
			t.Fatal(err)
		}
	}
}

func removeMissingCleanupEdges(ix *Index, have blob.Ref) {
	ix.Lock()
	defer ix.Unlock()
	ix.removeAllMissingEdges(have)
}

func assertMissingCleanupMarker(t *testing.T, ix *Index, have blob.Ref, want bool) {
	t.Helper()
	ix.RLock()
	defer ix.RUnlock()
	if got := ix.hasMissing.Contains(have); got != want {
		t.Errorf("hasMissing[%v] = %v; want %v", have, got, want)
	}
}

func assertMissingCleanupRow(t *testing.T, s sorted.KeyValue, key string, want bool) {
	t.Helper()
	value, err := s.Get(key)
	if want {
		if err != nil || value != "1" {
			t.Errorf("Get(%q) = %q, %v; want \"1\", nil", key, value, err)
		}
	} else if !errors.Is(err, sorted.ErrNotFound) {
		t.Errorf("Get(%q) = %q, %v; want ErrNotFound", key, value, err)
	}
}

func assertMissingCleanupOps(t *testing.T, s *missingCleanupKV, have blob.Ref, finds, batches int) {
	t.Helper()
	var wantFinds []string
	for range finds {
		wantFinds = append(wantFinds, keyMissing.Prefix(have))
	}
	if !slices.Equal(s.missingFinds, wantFinds) {
		t.Errorf("missing Find starts = %q; want %q", s.missingFinds, wantFinds)
	}
	if s.missingCloses != finds || s.openMissing != 0 {
		t.Errorf("missing iterators: closed %d, open %d; want closed %d, open 0",
			s.missingCloses, s.openMissing, finds)
	}
	if s.begins != batches || len(s.commits) != batches {
		t.Errorf("batch begins/commits = %d/%d; want %d/%d",
			s.begins, len(s.commits), batches, batches)
	}
	if len(s.deletes) != 0 {
		t.Errorf("standalone Delete calls = %q; want none", s.deletes)
	}
	if s.batchWhileOpen {
		t.Error("batch started or committed before missing iterator was closed")
	}
}

func TestMissingCleanupRawBlobsSkipFind(t *testing.T) {
	ix, s := newMissingCleanupIndex(t)
	for _, contents := range []string{"raw blob one", "raw blob two", "raw blob three"} {
		ref := blob.RefFromString(contents)
		got, err := ix.ReceiveBlob(context.Background(), ref, strings.NewReader(contents))
		if err != nil {
			t.Fatal(err)
		}
		if want := (blob.SizedRef{Ref: ref, Size: uint32(len(contents))}); got != want {
			t.Errorf("ReceiveBlob = %v; want %v", got, want)
		}
		assertMissingCleanupMarker(t, ix, ref, false)
	}
	assertMissingCleanupOps(t, s, blob.Ref{}, 0, 3) // only indexing batches
	if ix.hasMissing != nil {
		t.Error("raw blobs allocated hasMissing; want lazy initialization")
	}
}

func TestMissingCleanupStartupReload(t *testing.T) {
	_, s := newMissingCleanupIndex(t)
	have := blob.RefFromString("persisted needer")
	other := blob.RefFromString("another persisted needer")
	deps := []blob.Ref{blob.RefFromString("first dependency"), blob.RefFromString("second dependency")}
	for _, pair := range [][2]blob.Ref{{have, deps[0]}, {have, deps[1]}, {other, deps[0]}} {
		if err := s.KeyValue.Set(keyMissing.Key(pair[0], pair[1]), "1"); err != nil {
			t.Fatal(err)
		}
	}
	ix, err := New(s)
	if err != nil {
		t.Fatal(err)
	}
	assertMissingCleanupMarker(t, ix, have, true)
	assertMissingCleanupMarker(t, ix, other, true)
	if got := ix.needs[have]; len(got) != 2 || !slices.Contains(got, deps[0]) || !slices.Contains(got, deps[1]) {
		t.Fatalf("reloaded needs[%v] = %v; want %v", have, got, deps)
	}
	s.resetCounts()
	removeMissingCleanupEdges(ix, have)
	assertMissingCleanupOps(t, s, have, 1, 1)
	assertMissingCleanupMarker(t, ix, have, false)
	for _, dep := range deps {
		assertMissingCleanupRow(t, s, keyMissing.Key(have, dep), false)
	}
	assertMissingCleanupMarker(t, ix, other, true)
	assertMissingCleanupRow(t, s, keyMissing.Key(other, deps[0]), true)
}

func TestMissingCleanupReadyBlobKeepsEligibility(t *testing.T) {
	ix, s := newMissingCleanupIndex(t)
	have, dep := blob.RefFromString("ready needer"), blob.RefFromString("ready dependency")
	noteMissingCleanupEdges(t, ix, have, dep)

	func() {
		ix.Lock()
		defer ix.Unlock()
		// Suppress worker startup: exercise the real queueing transition without
		// a background goroutine, blob source, or timing-dependent assertions.
		ix.readyWorkerDone = make(chan struct{})
		defer func() { ix.readyWorkerDone = nil }()
		ix.noteBlobIndexedLocked(dep)
		if _, ok := ix.needs[have]; ok {
			t.Errorf("needs[%v] remains after its dependency was indexed", have)
		}
		if !ix.readyReindex.Contains(have) {
			t.Errorf("readyReindex does not contain %v", have)
		}
	}()
	assertMissingCleanupMarker(t, ix, have, true)
	removeMissingCleanupEdges(ix, have)
	assertMissingCleanupOps(t, s, have, 1, 1)
	assertMissingCleanupMarker(t, ix, have, false)
	assertMissingCleanupRow(t, s, keyMissing.Key(have, dep), false)
}

func TestMissingCleanupMultipleEdgesOneBatch(t *testing.T) {
	ix, s := newMissingCleanupIndex(t)
	have, other := blob.RefFromString("batch needer"), blob.RefFromString("unrelated needer")
	deps := []blob.Ref{blob.RefFromString("batch dep a"), blob.RefFromString("batch dep b"), blob.RefFromString("batch dep c")}
	noteMissingCleanupEdges(t, ix, have, deps...)
	noteMissingCleanupEdges(t, ix, other, deps[0])
	var keys []string
	for _, dep := range deps {
		keys = append(keys, keyMissing.Key(have, dep))
	}
	slices.Sort(keys)

	removeMissingCleanupEdges(ix, have)
	assertMissingCleanupOps(t, s, have, 1, 1)
	if len(s.commits) == 1 && !slices.Equal(s.commits[0], keys) {
		t.Errorf("batch deletes = %q; want %q", s.commits[0], keys)
	}
	for _, key := range keys {
		assertMissingCleanupRow(t, s, key, false)
	}
	assertMissingCleanupRow(t, s, keyMissing.Key(other, deps[0]), true)
	assertMissingCleanupMarker(t, ix, have, false)
	assertMissingCleanupMarker(t, ix, other, true)
	// Successful cleanup must make subsequent calls skip storage entirely.
	removeMissingCleanupEdges(ix, have)
	assertMissingCleanupOps(t, s, have, 1, 1)
}

func TestMissingCleanupIteratorCloseError(t *testing.T) {
	for _, empty := range []bool{false, true} {
		t.Run(fmt.Sprintf("empty=%v", empty), func(t *testing.T) {
			ix, s := newMissingCleanupIndex(t)
			have := blob.RefFromString("iterator error needer")
			deps := []blob.Ref{blob.RefFromString("iterator dep a"), blob.RefFromString("iterator dep b")}
			noteMissingCleanupEdges(t, ix, have, deps...)
			if empty {
				// Simulate stale conservative markers without changing needs.
				for _, dep := range deps {
					if err := s.KeyValue.Delete(keyMissing.Key(have, dep)); err != nil {
						t.Fatal(err)
					}
				}
			}
			s.iteratorCloseErr = errors.New("injected missing iterator error")
			removeMissingCleanupEdges(ix, have)
			assertMissingCleanupOps(t, s, have, 1, 0)
			assertMissingCleanupMarker(t, ix, have, true)
			for _, dep := range deps {
				assertMissingCleanupRow(t, s, keyMissing.Key(have, dep), !empty)
			}

			s.iteratorCloseErr = nil
			removeMissingCleanupEdges(ix, have)
			batches := 1
			if empty {
				batches = 0
			}
			assertMissingCleanupOps(t, s, have, 2, batches)
			assertMissingCleanupMarker(t, ix, have, false)
			for _, dep := range deps {
				assertMissingCleanupRow(t, s, keyMissing.Key(have, dep), false)
			}
			removeMissingCleanupEdges(ix, have)
			assertMissingCleanupOps(t, s, have, 2, batches)
		})
	}
}

func TestMissingCleanupCommitFailureRetry(t *testing.T) {
	ix, s := newMissingCleanupIndex(t)
	have := blob.RefFromString("commit error needer")
	deps := []blob.Ref{blob.RefFromString("commit dep a"), blob.RefFromString("commit dep b")}
	noteMissingCleanupEdges(t, ix, have, deps...)
	s.commitErr = errors.New("injected cleanup batch failure")

	removeMissingCleanupEdges(ix, have)
	assertMissingCleanupOps(t, s, have, 1, 1)
	assertMissingCleanupMarker(t, ix, have, true)
	for _, dep := range deps {
		assertMissingCleanupRow(t, s, keyMissing.Key(have, dep), true)
	}

	s.commitErr = nil
	removeMissingCleanupEdges(ix, have)
	assertMissingCleanupOps(t, s, have, 2, 2)
	assertMissingCleanupMarker(t, ix, have, false)
	if len(s.commits) == 2 && !slices.Equal(s.commits[0], s.commits[1]) {
		t.Errorf("retry deletes = %q; want original deletes %q", s.commits[1], s.commits[0])
	}
	for _, dep := range deps {
		assertMissingCleanupRow(t, s, keyMissing.Key(have, dep), false)
	}
	removeMissingCleanupEdges(ix, have)
	assertMissingCleanupOps(t, s, have, 2, 2)
}

func TestMissingCleanupAmbiguousSetFailure(t *testing.T) {
	for _, persisted := range []bool{false, true} {
		t.Run(fmt.Sprintf("persisted=%v", persisted), func(t *testing.T) {
			ix, s := newMissingCleanupIndex(t)
			contents := "raw blob successfully indexed after a failed dependency write"
			have, dep := blob.RefFromString(contents), blob.RefFromString("ambiguous dependency")
			key := keyMissing.Key(have, dep)
			s.setErr = errors.New("injected dependency Set failure")
			s.setErrKey = key
			s.persistFailedSet = persisted
			var markedBeforeSet bool
			s.onSet = func(got string) {
				if got == key {
					// noteNeededLocked holds ix.mu here.
					markedBeforeSet = ix.hasMissing.Contains(have)
				}
			}
			ix.Lock()
			err := ix.noteNeededLocked(have, dep)
			ix.Unlock()
			if !errors.Is(err, s.setErr) {
				t.Fatalf("noteNeededLocked = %v; want %v", err, s.setErr)
			}
			if !markedBeforeSet {
				t.Error("hasMissing was not marked before attempting Set")
			}
			assertMissingCleanupMarker(t, ix, have, true)
			if _, ok := ix.needs[have]; ok {
				t.Error("failed Set updated needs; want no dependency recorded in memory")
			}
			assertMissingCleanupRow(t, s, key, persisted)
			s.setErr, s.onSet = nil, nil

			if _, err := ix.ReceiveBlob(context.Background(), have, strings.NewReader(contents)); err != nil {
				t.Fatal(err)
			}
			batches := 1 // indexing commit; an empty cleanup must not start a batch
			if persisted {
				batches++
			}
			assertMissingCleanupOps(t, s, have, 1, batches)
			if persisted && len(s.commits) == 2 && !slices.Equal(s.commits[1], []string{key}) {
				t.Errorf("cleanup batch deletes = %q; want [%q]", s.commits[1], key)
			}
			assertMissingCleanupMarker(t, ix, have, false)
			assertMissingCleanupRow(t, s, key, false)
			value, err := s.Get("have:" + have.String())
			if err != nil || !strings.HasSuffix(value, "|indexed") {
				t.Errorf("have row = %q, %v; want successfully indexed", value, err)
			}
			removeMissingCleanupEdges(ix, have)
			assertMissingCleanupOps(t, s, have, 1, batches)
		})
	}
}
