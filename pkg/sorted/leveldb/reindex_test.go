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

package leveldb

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"perkeep.org/pkg/sorted"
	"perkeep.org/pkg/sorted/kvtest"
)

func reindexOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func reindexStore(t *testing.T) *kvis {
	t.Helper()
	kv, err := NewStorage(filepath.Join(t.TempDir(), "db.leveldb"))
	reindexOK(t, err)
	t.Cleanup(func() { kv.Close() })
	return kv.(*kvis)
}

func reindexBegin(t *testing.T, kv *kvis) *reindexSession {
	t.Helper()
	s, err := kv.BeginReindex()
	reindexOK(t, err)
	return s.(*reindexSession)
}

func hasMarker(t *testing.T, dir string) bool {
	t.Helper()
	ok, err := hasIncompleteMarker(dir)
	reindexOK(t, err)
	return ok
}

// reopen closes kv and opens its database again, returning the error
// from opening it, as well as the new KeyValue, if any.
func reopen(t *testing.T, kv *kvis) (sorted.KeyValue, error) {
	t.Helper()
	reindexOK(t, kv.Close())
	kv.db = nil
	got, err := NewStorage(kv.path)
	if got != nil {
		t.Cleanup(func() { got.Close() })
	}
	return got, err
}

func TestWritesSyncByDefault(t *testing.T) {
	kv := reindexStore(t)
	if !kv.writeOpts.Sync {
		t.Fatal("ordinary writes aren't synced")
	}
	s := reindexBegin(t, kv)
	defer s.Close()
	if s.writeOpts.Sync {
		t.Fatal("reindex writes are synced")
	}
}

func TestReindexSessionSorted(t *testing.T) {
	kv := reindexStore(t)
	s := reindexBegin(t, kv)
	kvtest.TestSorted(t, s)
	reindexOK(t, s.Finish(true))
}

func TestReindexFinish(t *testing.T) {
	kv := reindexStore(t)
	reindexOK(t, kv.Set("old", "wipe me"))
	s := reindexBegin(t, kv)
	if !hasMarker(t, kv.path) {
		t.Fatal("no incomplete marker during reindex")
	}
	if _, err := s.Get("old"); !errors.Is(err, sorted.ErrNotFound) {
		t.Fatalf("Get of wiped key: %v", err)
	}
	reindexOK(t, s.Set("new", "value"))
	b := s.BeginBatch()
	b.Set("batched", "value")
	reindexOK(t, s.CommitBatch(b))
	reindexOK(t, s.Finish(true))
	reindexOK(t, s.Finish(true)) // idempotent
	if hasMarker(t, kv.path) {
		t.Fatal("incomplete marker remains after Finish")
	}
	if _, err := s.Get("new"); !errors.Is(err, errSessionDone) {
		t.Fatalf("Get after Finish: %v", err)
	}
	// Reopening the database moved the session's writes from the
	// unsynced journal into synced tables, and started an empty journal.
	ents, err := os.ReadDir(kv.path)
	reindexOK(t, err)
	for _, ent := range ents {
		if !strings.HasSuffix(ent.Name(), ".log") {
			continue
		}
		fi, err := ent.Info()
		reindexOK(t, err)
		if fi.Size() != 0 {
			t.Errorf("journal %s has %d bytes after Finish", ent.Name(), fi.Size())
		}
	}
	// The parent is usable, and syncs its writes again.
	reindexOK(t, kv.Set("after", "value"))

	got, err := reopen(t, kv)
	reindexOK(t, err)
	for _, k := range []string{"new", "batched", "after"} {
		if v, err := got.Get(k); err != nil || v != "value" {
			t.Errorf("reopened Get(%q) = %q, %v", k, v, err)
		}
	}
	if _, err := got.Get("old"); !errors.Is(err, sorted.ErrNotFound) {
		t.Errorf("reopened Get of wiped key: %v", err)
	}
}

func TestReindexAbort(t *testing.T) {
	kv := reindexStore(t)
	s := reindexBegin(t, kv)
	reindexOK(t, s.Set("partial", "value"))
	reindexOK(t, s.Close())
	if err := s.Finish(true); !errors.Is(err, errSessionDone) {
		t.Fatalf("Finish(true) after abort = %v", err)
	}
	if err := s.Set("k", "v"); !errors.Is(err, errSessionDone) {
		t.Fatalf("Set after abort = %v", err)
	}
	if !hasMarker(t, kv.path) {
		t.Fatal("no incomplete marker after abort")
	}
	got, err := reopen(t, kv)
	var need sorted.NeedWipeError
	if got == nil || !errors.As(err, &need) {
		t.Fatalf("reopen after abort = %v, %v; want NeedWipeError", got, err)
	}
	reindexOK(t, got.(sorted.Wiper).Wipe())
	if hasMarker(t, kv.path) {
		t.Fatal("incomplete marker remains after Wipe")
	}
	if _, err := got.Get("partial"); !errors.Is(err, sorted.ErrNotFound) {
		t.Fatalf("Get after Wipe: %v", err)
	}
	_, err = reopen(t, got.(*kvis))
	reindexOK(t, err)
}

func TestReindexWriteErrorIsSticky(t *testing.T) {
	kv := reindexStore(t)
	s := reindexBegin(t, kv)
	reindexOK(t, s.Set("k", "v"))
	// Ignore a failed write, as a caller might.
	failure := errors.New("disk on fire")
	s.fail(failure)
	if err := s.Finish(true); !errors.Is(err, failure) {
		t.Fatalf("Finish = %v, want %v", err, failure)
	}
	if !hasMarker(t, kv.path) {
		t.Fatal("no incomplete marker after failed reindex")
	}
}

func TestReindexConcurrentUse(t *testing.T) {
	kv := reindexStore(t)
	s := reindexBegin(t, kv)
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Go(func() {
			for j := range 100 {
				k := fmt.Sprintf("k%d-%d", i, j)
				if err := s.Set(k, "v"); err != nil {
					t.Error(err)
					return
				}
				if _, err := s.Get(k); err != nil {
					t.Error(err)
					return
				}
				b := s.BeginBatch()
				b.Set(k+"b", "v")
				if err := s.CommitBatch(b); err != nil {
					t.Error(err)
					return
				}
			}
		})
	}
	wg.Wait()
	reindexOK(t, s.Finish(true))
	n := 0
	it := kv.Find("", "")
	for it.Next() {
		n++
	}
	reindexOK(t, it.Close())
	if n != 8*100*2 {
		t.Fatalf("found %d rows, want %d", n, 8*100*2)
	}
}

// TestOpenCorruptIncomplete checks that a database left partly removed by
// an interrupted reindex or Wipe opens, empty, with a NeedWipeError.
func TestOpenCorruptIncomplete(t *testing.T) {
	kv := reindexStore(t)
	reindexOK(t, kv.Set("old", "value"))
	reindexOK(t, kv.Close())
	kv.db = nil
	reindexOK(t, writeIncompleteMarker(kv.path))
	ents, err := os.ReadDir(kv.path)
	reindexOK(t, err)
	for _, ent := range ents {
		if strings.HasPrefix(ent.Name(), "MANIFEST-") {
			reindexOK(t, os.Remove(filepath.Join(kv.path, ent.Name())))
		}
	}
	got, err := NewStorage(kv.path)
	var need sorted.NeedWipeError
	if got == nil || !errors.As(err, &need) {
		t.Fatalf("open = %v, %v; want NeedWipeError", got, err)
	}
	defer got.Close()
	if _, err := got.Get("old"); !errors.Is(err, sorted.ErrNotFound) {
		t.Fatalf("Get from cleared database: %v", err)
	}
}

func TestReindexCrashRecovery(t *testing.T) {
	const childEnv = "PERKEEP_LEVELDB_REINDEX_CRASH_FILE"
	if file := os.Getenv(childEnv); file != "" {
		kv, err := NewStorage(file)
		reindexOK(t, err)
		s := reindexBegin(t, kv.(*kvis))
		reindexOK(t, s.Set("partial", "value"))
		os.Exit(23) // No Finish, no Close, no testing cleanup.
	}
	kv := reindexStore(t)
	reindexOK(t, kv.Set("old", "wipe me"))
	reindexOK(t, kv.Close())
	kv.db = nil
	cmd := exec.Command(os.Args[0], "-test.run=^TestReindexCrashRecovery$")
	cmd.Env = append(os.Environ(), childEnv+"="+kv.path)
	out, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 23 {
		t.Fatalf("crash helper: %v\n%s", err, out)
	}
	got, err := NewStorage(kv.path)
	var need sorted.NeedWipeError
	if got == nil || !errors.As(err, &need) {
		t.Fatalf("reopen after crash = %v, %v; want NeedWipeError", got, err)
	}
	defer got.Close()
	if _, err := got.Get("old"); !errors.Is(err, sorted.ErrNotFound) {
		t.Fatalf("Get of wiped key after crash: %v", err)
	}
	s := reindexBegin(t, got.(*kvis))
	reindexOK(t, s.Set("fresh", "complete"))
	reindexOK(t, s.Finish(true))
	if hasMarker(t, kv.path) {
		t.Fatal("incomplete marker remains after a complete rebuild")
	}
}
