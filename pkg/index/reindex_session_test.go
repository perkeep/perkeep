/*
Copyright 2026 The Perkeep Authors.

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
	"io"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/sorted"
	"perkeep.org/pkg/test"
)

// recordingReindexKV implements the optional interface explicitly: embedding a
// sorted.KeyValue alone would hide BeginReindex and accidentally test Wipe.
// The private view uses another memory KV so using the original during a rebuild
// cannot accidentally succeed with the freshly indexed rows.
type recordingReindexKV struct {
	sorted.KeyValue
	view     *recordingReindexSession
	beginErr error
	begins   int
	wipes    int
}

func (s *recordingReindexKV) Wipe() error {
	s.wipes++
	return s.KeyValue.(sorted.Wiper).Wipe()
}

func (s *recordingReindexKV) BeginReindex() (sorted.ReindexSession, error) {
	s.begins++
	if s.beginErr != nil {
		return nil, s.beginErr
	}
	return s.view, nil
}

type recordingReindexSession struct {
	sorted.KeyValue
	mu          sync.Mutex
	afterCommit func(sorted.BatchMutation)

	setErr, getErr, cacheErr, missingErr, finishErr error
	sticky                                          error
	finishes                                        []bool
	cacheClosed                                     bool
	onMissingSet                                    func()
	onFinish                                        func(bool)
}

func (s *recordingReindexSession) Set(key, value string) error {
	if s.setErr != nil && key == keySchemaVersion.name {
		return s.setErr
	}
	err := s.KeyValue.Set(key, value)
	if err == nil && strings.HasPrefix(key, "missing|") && s.onMissingSet != nil {
		s.onMissingSet()
	}
	return err
}

func (s *recordingReindexSession) Get(key string) (string, error) {
	if s.getErr != nil && strings.HasPrefix(key, "have:") {
		s.mu.Lock()
		s.sticky = s.getErr
		s.mu.Unlock()
		return "", s.getErr
	}
	return s.KeyValue.Get(key)
}

func (s *recordingReindexSession) Find(start, end string) sorted.Iterator {
	it := s.KeyValue.Find(start, end)
	if start == keyDeleted.Prefix() {
		return &recordingReindexIterator{Iterator: it, session: s, deletionCache: true}
	}
	if strings.HasPrefix(start, "missing|") && s.missingErr != nil {
		return &recordingReindexIterator{Iterator: it, session: s}
	}
	return it
}

func (s *recordingReindexSession) CommitBatch(b sorted.BatchMutation) error {
	if err := s.KeyValue.CommitBatch(b); err != nil {
		return err
	}
	if s.afterCommit != nil {
		s.afterCommit(b)
	}
	return nil
}

func (s *recordingReindexSession) Finish(success bool) error {
	s.mu.Lock()
	s.finishes = append(s.finishes, success)
	err := errors.Join(s.sticky, s.finishErr)
	s.mu.Unlock()
	if s.onFinish != nil {
		s.onFinish(success)
	}
	return err
}

func (s *recordingReindexSession) Close() error { return s.Finish(false) }

type recordingReindexIterator struct {
	sorted.Iterator
	session       *recordingReindexSession
	deletionCache bool
}

func (it *recordingReindexIterator) Close() error {
	err := it.Iterator.Close()
	it.session.mu.Lock()
	defer it.session.mu.Unlock()
	if it.deletionCache {
		it.session.cacheClosed = true
		return errors.Join(err, it.session.cacheErr)
	}
	// Cleanup logs this Close error rather than returning it to ReceiveBlob.
	// Model the SQL session remembering the underlying failure regardless.
	it.session.sticky = it.session.missingErr
	return errors.Join(err, it.session.missingErr)
}

func newRecordingReindex(t *testing.T) (*Index, *recordingReindexKV) {
	t.Helper()
	s := &recordingReindexKV{
		KeyValue: sorted.NewMemoryKeyValue(),
		view: &recordingReindexSession{
			KeyValue: sorted.NewMemoryKeyValue(),
		},
	}
	ix, err := New(s)
	if err != nil {
		t.Fatal(err)
	}
	ix.Logger = test.NewLogger(t, "")
	t.Cleanup(func() {
		if err := ix.Close(); err != nil {
			t.Error(err)
		}
	})
	return ix, s
}

func TestReindexSessionFinalization(t *testing.T) {
	t.Setenv("CAMLI_REINDEX_START", "")
	t.Setenv("CAMLI_REDO_INDEX_ON_RECEIVE", "false")
	fault := errors.New("injected reindex session failure")
	for _, tc := range []struct {
		name    string
		fault   func(*recordingReindexKV)
		want    []bool
		wantErr bool
	}{
		{name: "success", want: []bool{true}},
		{name: "begin", fault: func(s *recordingReindexKV) { s.beginErr = fault }, wantErr: true},
		{name: "schema-set", fault: func(s *recordingReindexKV) { s.view.setErr = fault }, want: []bool{false}, wantErr: true},
		{name: "deletion-cache-close", fault: func(s *recordingReindexKV) { s.view.cacheErr = fault }, want: []bool{false}, wantErr: true},
		{name: "final-commit", fault: func(s *recordingReindexKV) { s.view.finishErr = fault }, want: []bool{true}, wantErr: true},
		// A batched reindex doesn't look up have: rows, so a failing
		// have:Get (which would make the session's error sticky) is
		// never reached.
		{name: "have-get-skipped", fault: func(s *recordingReindexKV) { s.view.getErr = fault }, want: []bool{true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ix, s := newRecordingReindex(t)
			src := new(test.Fetcher)
			raw := &test.Blob{Contents: "session finalization raw blob"}
			src.AddBlob(raw)
			ix.InitBlobSource(src)
			if tc.fault != nil {
				tc.fault(s)
			}
			s.view.onFinish = func(success bool) {
				if ix.s != s.view {
					t.Error("original storage restored before session Finish")
				}
				if success && !s.view.cacheClosed {
					t.Error("Finish(true) called before the deletion iterator closed")
				}
				ix.RLock()
				defer ix.RUnlock()
				if ix.inFlight.Len() != 0 || ix.readyWorkerDone != nil || ix.readyReindex.Len() != 0 {
					t.Error("session finalized with indexing workers still active")
				}
			}
			err := ix.Reindex()
			if tc.wantErr {
				if !errors.Is(err, ErrReindexIncomplete) || !errors.Is(err, fault) {
					t.Fatalf("Reindex = %v; want incomplete rebuild wrapping %v", err, fault)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if ix.Storage() != s {
				t.Error("Reindex did not restore original storage")
			}
			if s.begins != 1 || s.wipes != 0 {
				t.Errorf("BeginReindex/Wipe = %d/%d; want 1/0", s.begins, s.wipes)
			}
			if !slices.Equal(s.view.finishes, tc.want) {
				t.Errorf("Finish calls = %v; want %v", s.view.finishes, tc.want)
			}
			if tc.wantErr {
				// Failure is terminal for this Index; retry must not start a
				// second session using provisional dependency/cache state.
				if err := ix.Reindex(); !errors.Is(err, ErrReindexIncomplete) {
					t.Errorf("second Reindex = %v; want ErrReindexIncomplete", err)
				}
				if s.begins != 1 {
					t.Error("failed Index began another reindex session")
				}
			}
		})
	}
}

// failingFetchSource is a blob source whose Fetch of one of its blobs fails.
type failingFetchSource struct {
	*test.Fetcher
	bad blob.Ref
}

func (s *failingFetchSource) Fetch(ctx context.Context, br blob.Ref) (io.ReadCloser, uint32, error) {
	if br == s.bad {
		return nil, 0, errors.New("injected fetch failure")
	}
	return s.Fetcher.Fetch(ctx, br)
}

// TestReindexSessionBlobFailure tests that a blob failing to index aborts a
// batched rebuild, unless keepGoing is set, in which case the rebuild is
// committed without it. keepGoing doesn't excuse a failed commit.
func TestReindexSessionBlobFailure(t *testing.T) {
	t.Setenv("CAMLI_REINDEX_START", "")
	t.Setenv("CAMLI_REDO_INDEX_ON_RECEIVE", "false")
	fault := errors.New("injected final commit failure")
	for _, tc := range []struct {
		name           string
		keepGoing      bool
		finishErr      error
		wantFinish     []bool
		wantIncomplete bool
	}{
		{name: "stop", wantFinish: []bool{false}, wantIncomplete: true},
		{name: "keep-going", keepGoing: true, wantFinish: []bool{true}},
		{name: "keep-going-commit-fails", keepGoing: true, finishErr: fault, wantFinish: []bool{true}, wantIncomplete: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ix, s := newRecordingReindex(t)
			ix.keepGoing = tc.keepGoing
			s.view.finishErr = tc.finishErr
			src := &failingFetchSource{Fetcher: new(test.Fetcher)}
			good := &test.Blob{Contents: "blob that indexes fine"}
			bad := &test.Blob{Contents: "blob whose fetch fails"}
			src.AddBlob(good)
			src.AddBlob(bad)
			src.bad = bad.BlobRef()
			ix.InitBlobSource(src)

			err := ix.Reindex()
			if err == nil || !strings.Contains(err.Error(), "1 blobs failed to re-index") {
				t.Fatalf("Reindex = %v; want error about the failed blob", err)
			}
			if got := errors.Is(err, ErrReindexIncomplete); got != tc.wantIncomplete {
				t.Errorf("errors.Is(%v, ErrReindexIncomplete) = %v; want %v", err, got, tc.wantIncomplete)
			}
			if tc.finishErr != nil && !errors.Is(err, tc.finishErr) {
				t.Errorf("Reindex = %v; want it to wrap %v", err, tc.finishErr)
			}
			if !slices.Equal(s.view.finishes, tc.wantFinish) {
				t.Errorf("Finish calls = %v; want %v", s.view.finishes, tc.wantFinish)
			}
			if ix.reindexFailed != tc.wantIncomplete {
				t.Errorf("reindexFailed = %v; want %v", ix.reindexFailed, tc.wantIncomplete)
			}
			if _, err := s.view.KeyValue.Get("have:" + good.BlobRef().String()); err != nil {
				t.Errorf("good blob not indexed: %v", err)
			}
		})
	}
}

// sessionDrainSource makes a real file dependency missing once, then enumerates
// that dependency. Thus the queue is created after BeginReindex resets state,
// rather than depending on pre-wipe in-memory edges surviving the rebuild.
type sessionDrainSource struct {
	*test.Fetcher
	file, primary                  *test.Blob
	missingRecorded                <-chan struct{}
	primaryStarted, releasePrimary chan struct{}
	readyStarted, releaseReady     chan struct{}
	enumErr, readyErr              error
	mu                             sync.Mutex
	primaryFetches, fileFetches    int
}

func (s *sessionDrainSource) EnumerateBlobs(_ context.Context, dest chan<- blob.SizedRef, after string, _ int) error {
	defer close(dest)
	if after != "" {
		return nil
	}
	dest <- s.file.SizedRef()
	<-s.missingRecorded
	dest <- s.primary.SizedRef()
	<-s.primaryStarted
	return s.enumErr
}

func (s *sessionDrainSource) Fetch(ctx context.Context, br blob.Ref) (io.ReadCloser, uint32, error) {
	s.mu.Lock()
	var fetches int
	switch br {
	case s.primary.BlobRef():
		s.primaryFetches++
		fetches = s.primaryFetches
	case s.file.BlobRef():
		s.fileFetches++
		fetches = s.fileFetches
	}
	s.mu.Unlock()
	switch br {
	case s.primary.BlobRef():
		if fetches == 1 {
			return nil, 0, os.ErrNotExist
		}
		if fetches == 2 {
			close(s.primaryStarted)
			<-s.releasePrimary
		}
	case s.file.BlobRef():
		if fetches == 2 {
			close(s.readyStarted)
			<-s.releaseReady
			if s.readyErr != nil {
				return nil, 0, s.readyErr
			}
		}
	}
	return s.Fetcher.Fetch(ctx, br)
}

func TestReindexSessionWaitsForWorkersOnEnumerationError(t *testing.T) {
	t.Setenv("CAMLI_REINDEX_START", "")
	t.Setenv("CAMLI_REDO_INDEX_ON_RECEIVE", "false")
	testReindexSessionDrain(t, errors.New("enumeration failed during batched reindex"), nil, nil)
}

func TestReindexSessionReadyRetryFailureAborts(t *testing.T) {
	t.Setenv("CAMLI_REINDEX_START", "")
	t.Setenv("CAMLI_REDO_INDEX_ON_RECEIVE", "false")
	testReindexSessionDrain(t, nil, errors.New("ready retry fetch failed"), nil)
}

func TestReindexSessionStickyMissingIteratorError(t *testing.T) {
	t.Setenv("CAMLI_REINDEX_START", "")
	t.Setenv("CAMLI_REDO_INDEX_ON_RECEIVE", "false")
	testReindexSessionDrain(t, nil, nil, errors.New("ignored missing iterator close failure"))
}

func testReindexSessionDrain(t *testing.T, enumErr, readyErr, missingErr error) {
	t.Helper()
	synctest.Test(t, func(t *testing.T) {
		ix, s := newRecordingReindex(t)
		s.view.missingErr = missingErr
		primary, file := sessionDependencyBlobs(t)
		missingRecorded := make(chan struct{})
		var once sync.Once
		s.view.onMissingSet = func() { once.Do(func() { close(missingRecorded) }) }

		src := &sessionDrainSource{
			Fetcher: new(test.Fetcher), file: file, primary: primary,
			missingRecorded: missingRecorded,
			primaryStarted:  make(chan struct{}), releasePrimary: make(chan struct{}),
			readyStarted: make(chan struct{}), releaseReady: make(chan struct{}),
			enumErr: enumErr, readyErr: readyErr,
		}
		src.AddBlob(primary)
		src.AddBlob(file)
		ix.InitBlobSource(src)
		returned := false
		var reindexErr error
		go func() {
			reindexErr = ix.Reindex()
			returned = true
		}()
		synctest.Wait()
		select {
		case <-src.primaryStarted:
		default:
			t.Fatal("dependency's primary worker did not start")
		}
		if returned || len(s.view.finishes) != 0 || ix.s != s.view {
			t.Fatal("Reindex finalized while primary Fetch was blocked")
		}
		close(src.releasePrimary)
		synctest.Wait()
		select {
		case <-src.readyStarted:
		default:
			t.Fatal("dependency completion did not start the ready retry")
		}
		if returned || len(s.view.finishes) != 0 || ix.s != s.view {
			t.Fatal("Reindex finalized while ready Fetch was blocked")
		}
		close(src.releaseReady)
		synctest.Wait()
		if !returned || !errors.Is(reindexErr, ErrReindexIncomplete) {
			t.Errorf("returned=%v, Reindex=%v; want incomplete rebuild", returned, reindexErr)
		}
		for _, original := range []error{enumErr, missingErr} {
			if original != nil && !errors.Is(reindexErr, original) {
				t.Errorf("Reindex = %v; want original error %v", reindexErr, original)
			}
		}
		wantFinish := enumErr == nil && readyErr == nil
		if !slices.Equal(s.view.finishes, []bool{wantFinish}) || ix.s != s {
			t.Errorf("Finish=%v, storage=%T; want Finish(%v) and restored original", s.view.finishes, ix.s, wantFinish)
		}
		ix.RLock()
		defer ix.RUnlock()
		if ix.inFlight.Len() != 0 || ix.readyWorkerDone != nil || ix.readyReindex.Len() != 0 || len(ix.needs) != 0 {
			t.Error("Reindex returned before all worker/dependency state drained")
		}
		if got := ix.readyFailed.Len(); (got != 0) != (readyErr != nil) {
			t.Errorf("readyFailed = %d; want a failed retry only for ready-fetch errors", got)
		}
	})
}

func sessionDependencyBlobs(t *testing.T) (primary, file *test.Blob) {
	t.Helper()
	for n := range 100 {
		primary = &test.Blob{Contents: fmt.Sprintf("reindex session dependency %d", n)}
		file = &test.Blob{Contents: fmt.Sprintf(`{"camliVersion":1,"camliType":"file","fileName":"session.txt","parts":[{"blobRef":"%s","size":%d}]}`, primary.BlobRef(), len(primary.Contents))}
		if file.BlobRef().Less(primary.BlobRef()) {
			return primary, file
		}
	}
	t.Fatal("could not construct a file sorting before its dependency")
	return nil, nil
}

type sessionWakeupSource struct {
	*test.Fetcher
	file, primary   *test.Blob
	missed, indexed chan struct{}
	mu              sync.Mutex
	primaryFetches  int
}

func (s *sessionWakeupSource) EnumerateBlobs(_ context.Context, dest chan<- blob.SizedRef, after string, _ int) error {
	defer close(dest)
	if after != "" {
		return nil
	}
	dest <- s.file.SizedRef()
	<-s.missed
	dest <- s.primary.SizedRef()
	return nil
}

func (s *sessionWakeupSource) Fetch(ctx context.Context, br blob.Ref) (io.ReadCloser, uint32, error) {
	if br == s.primary.BlobRef() {
		s.mu.Lock()
		s.primaryFetches++
		first := s.primaryFetches == 1
		s.mu.Unlock()
		if first {
			close(s.missed)
			// Return the already-observed miss only after a second primary
			// worker commits the dependency. That commit still holds ix.mu,
			// so its completion notification precedes recording this edge.
			<-s.indexed
			return nil, 0, os.ErrNotExist
		}
	}
	return s.Fetcher.Fetch(ctx, br)
}

func TestReindexSessionDependencyArrivesBeforeMissingRecorded(t *testing.T) {
	t.Setenv("CAMLI_REINDEX_START", "")
	t.Setenv("CAMLI_REDO_INDEX_ON_RECEIVE", "false")
	oldProcs := ReindexMaxProcs()
	SetReindexMaxProcs(2)
	defer SetReindexMaxProcs(oldProcs)
	synctest.Test(t, func(t *testing.T) {
		ix, s := newRecordingReindex(t)
		primary, file := sessionDependencyBlobs(t)
		src := &sessionWakeupSource{
			Fetcher: new(test.Fetcher), file: file, primary: primary,
			missed: make(chan struct{}), indexed: make(chan struct{}),
		}
		src.AddBlob(primary)
		src.AddBlob(file)
		ix.InitBlobSource(src)
		var once sync.Once
		s.view.afterCommit = func(b sorted.BatchMutation) {
			for _, m := range b.(interface{ Mutations() []sorted.Mutation }).Mutations() {
				if m.Key() == "have:"+primary.BlobRef().String() {
					once.Do(func() { close(src.indexed) })
				}
			}
		}
		var reindexErr error
		returned := false
		go func() { reindexErr = ix.Reindex(); returned = true }()
		synctest.Wait()
		if !returned || reindexErr != nil {
			t.Fatalf("returned=%v, Reindex=%v; want dependency retry to finish successfully", returned, reindexErr)
		}
		if got, err := s.view.KeyValue.Get("have:" + file.BlobRef().String()); err != nil || !strings.HasSuffix(got, "|indexed") {
			t.Errorf("file have = %q, %v; want indexed after missing-wakeup retry", got, err)
		}
		it := s.view.KeyValue.Find("missing|", "missing}")
		if it.Next() {
			t.Errorf("stuck missing row %q after dependency completion", it.Key())
		}
		if err := it.Close(); err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(s.view.finishes, []bool{true}) || ix.s != s {
			t.Error("missing-wakeup recovery did not finalize and restore original storage")
		}
	})
}

func TestReindexSessionRejectsExistingCorpus(t *testing.T) {
	ix, s := newRecordingReindex(t)
	ix.InitBlobSource(new(test.Fetcher))
	if _, err := ix.KeepInMemory(); err != nil {
		t.Fatal(err)
	}
	if err := ix.Reindex(); err == nil || errors.Is(err, ErrReindexIncomplete) {
		t.Errorf("Reindex = %v; want preflight rejection, not a failed rebuild", err)
	}
	if s.begins != 0 || s.wipes != 0 || ix.Storage() != s {
		t.Error("corpus preflight rejection mutated storage")
	}
}

func TestReindexSessionRejectsActiveReceive(t *testing.T) {
	ix, s := newRecordingReindex(t)
	ix.InitBlobSource(new(test.Fetcher))
	op := ix.beginIndexOp()
	defer ix.endIndexOp(op)
	if err := ix.Reindex(); err == nil || errors.Is(err, ErrReindexIncomplete) {
		t.Errorf("Reindex = %v; want preflight rejection, not a failed rebuild", err)
	}
	if s.begins != 0 || s.wipes != 0 || ix.Storage() != s {
		t.Error("active-receive preflight rejection mutated storage")
	}
}
