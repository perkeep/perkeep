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

package sqlite

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"perkeep.org/pkg/sorted"
	"perkeep.org/pkg/test"
)

func reindexStore(t *testing.T) (*keyValue, *sql.DB) {
	t.Helper()
	kv, err := NewStorage(filepath.Join(t.TempDir(), "index.sqlite"))
	reindexOK(t, err)
	k := kv.(*keyValue)
	k.db.SetMaxOpenConns(1)
	t.Cleanup(func() { k.Close() })
	observer, err := sql.Open("sqlite", k.file)
	reindexOK(t, err)
	observer.SetMaxOpenConns(1)
	t.Cleanup(func() { observer.Close() })
	return k, observer
}

func reindexOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func reindexBegin(t *testing.T, k *keyValue) *reindexSession {
	t.Helper()
	s, err := k.BeginReindex()
	reindexOK(t, err)
	t.Cleanup(func() { s.Close() })
	return s.(*reindexSession)
}

func reindexObserved(t *testing.T, db *sql.DB, keys string, marker int) {
	t.Helper()
	var got string
	reindexOK(t, db.QueryRow("SELECT coalesce(group_concat(k, ','), '') FROM (SELECT k FROM rows ORDER BY k)").Scan(&got))
	if got != keys {
		t.Fatalf("committed keys = %q, want %q", got, keys)
	}
	var n int
	reindexOK(t, db.QueryRow("SELECT count(*) FROM meta WHERE metakey='reindexing'").Scan(&n))
	if n != marker {
		t.Fatalf("reindex marker count = %d, want %d", n, marker)
	}
}

func reindexWait(t *testing.T, f func() error) {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- f() }()
	select {
	case err := <-done:
		reindexOK(t, err)
	case <-time.After(10 * time.Second): // Deadlock watchdog, never synchronization.
		t.Fatal("SQLite reindex operation deadlocked")
	}
}

func reindexSQL(t *testing.T, s *reindexSession, query string) {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	reindexOK(t, s.ensureTxLocked())
	_, err := s.currentTx.Exec(query)
	reindexOK(t, err)
}

// reindexPragmas checks that tx's connection is in WAL mode, with the
// synchronous setting wantSync (0 for OFF, 2 for FULL).
func reindexPragmas(t *testing.T, tx *sql.Tx, wantSync int) {
	t.Helper()
	var mode string
	var sync int
	reindexOK(t, tx.QueryRow("PRAGMA journal_mode").Scan(&mode))
	reindexOK(t, tx.QueryRow("PRAGMA synchronous").Scan(&sync))
	if mode != "wal" || sync != wantSync {
		t.Fatalf("durability settings = %q/%d, want wal/%d", mode, sync, wantSync)
	}
}

func TestReindexNormalDurability(t *testing.T) {
	test.TLog(t)
	k, db := reindexStore(t)
	reindexOK(t, k.Set("old", "value"))
	reindexObserved(t, db, "old", 0)
	reindexOK(t, k.Delete("old"))
	reindexObserved(t, db, "", 0)
	b := k.BeginBatch()
	b.Set("old", "value")
	reindexOK(t, k.CommitBatch(b))
	reindexObserved(t, db, "old", 0)
	tx, err := k.db.Begin()
	reindexOK(t, err)
	reindexPragmas(t, tx, 2)
	reindexOK(t, tx.Rollback())
	s := reindexBegin(t, k)
	if s.maxMutations != 1024 || s.maxBytes != 4<<20 {
		t.Fatalf("default group budgets = %d mutations/%d bytes", s.maxMutations, s.maxBytes)
	}
	reindexObserved(t, db, "", 1) // Wipe and marker have committed before return.
	if _, err := s.Get("old"); !errors.Is(err, sorted.ErrNotFound) {
		t.Fatalf("wiped Get: %v", err)
	}
	// The reserved transaction's connection commits without syncing
	// until Finish.
	reindexPragmas(t, s.currentTx, 0)
	reindexOK(t, s.Set("new", "value"))
	if v, err := s.Get("new"); err != nil || v != "value" {
		t.Fatalf("read own Set = %q, %v", v, err)
	}
	b = s.BeginBatch()
	b.Delete("new")
	b.Set("final", "value")
	reindexOK(t, s.CommitBatch(b))
	if _, err := s.Get("new"); !errors.Is(err, sorted.ErrNotFound) {
		t.Fatalf("read own batch Delete: %v", err)
	}
	reindexOK(t, s.Delete("final"))
	if _, err := s.Get("final"); !errors.Is(err, sorted.ErrNotFound) {
		t.Fatalf("read own Delete: %v", err)
	}
	reindexOK(t, s.Set("final", "value"))
	reindexObserved(t, db, "", 1)
	reindexOK(t, s.Finish(true))
	reindexObserved(t, db, "final", 0)
	reindexWait(t, func() error { _, err := k.Get("final"); return err })
	// Finish restored syncing on the connection it returned to the pool.
	tx, err = k.db.Begin()
	reindexOK(t, err)
	reindexPragmas(t, tx, 2)
	reindexOK(t, tx.Rollback())
	reopened, err := NewStorage(k.file)
	reindexOK(t, err)
	reindexOK(t, reopened.Close())
}

func TestReindexGroupBudgets(t *testing.T) {
	for _, budget := range []struct {
		name             string
		mutations, bytes int
	}{{"mutations", 3, 100}, {"bytes", 100, 6}} {
		t.Run(budget.name, func(t *testing.T) {
			k, db := reindexStore(t)
			s := reindexBegin(t, k)
			s.maxMutations, s.maxBytes = budget.mutations, budget.bytes
			reindexOK(t, s.Set("a", "1"))
			reindexObserved(t, db, "", 1)
			batch := func(keys string) {
				b := s.BeginBatch()
				for _, key := range keys {
					b.Set(string(key), "1")
				}
				reindexOK(t, s.CommitBatch(b))
			}
			batch("bc") // Exact boundary must produce a real SQL commit.
			reindexObserved(t, db, "a,b,c", 1)
			reindexOK(t, s.Set("d", "1"))
			reindexObserved(t, db, "a,b,c", 1)
			batch("efg") // Commit d before this indivisible logical batch.
			reindexObserved(t, db, "a,b,c,d,e,f,g", 1)
			batch("hijk") // An oversized batch gets its own atomic transaction.
			reindexObserved(t, db, "a,b,c,d,e,f,g,h,i,j,k", 1)
			reindexOK(t, s.Finish(false))
			reindexObserved(t, db, "a,b,c,d,e,f,g,h,i,j,k", 1)
			reindexWait(t, func() error { _, err := k.Get("a"); return err })
		})
	}
}

func TestReindexFindSnapshots(t *testing.T) {
	test.TLog(t)
	k, _ := reindexStore(t)
	s := reindexBegin(t, k)
	s.maxMutations = 3
	reindexOK(t, s.Set("a", "old-a"))
	reindexOK(t, s.Set("b", "old-b"))
	reindexWait(t, func() error {
		first := s.Find("", "")
		defer first.Close()
		if !first.Next() || first.Key() != "a" {
			return fmt.Errorf("first iterator missing a")
		}
		if v, err := s.Get("b"); err != nil || v != "old-b" {
			return fmt.Errorf("overlapping Get = %q, %v", v, err)
		}
		second := s.Find("b", "c")
		defer second.Close()
		// Rollover and finish with both iterators open.
		if err := errors.Join(s.Set("c", "new-c"), s.Delete("b"), s.Set("a", "new-a"), s.Finish(true)); err != nil {
			return err
		}
		if first.Value() != "old-a" || string(first.ValueBytes()) != "old-a" || string(first.KeyBytes()) != "a" {
			return fmt.Errorf("iterator data changed after rollover")
		}
		if !first.Next() || first.Key() != "b" || first.Value() != "old-b" || first.Next() {
			return fmt.Errorf("first snapshot changed")
		}
		if !second.Next() || second.Key() != "b" || second.Value() != "old-b" || second.Next() {
			return fmt.Errorf("bounded snapshot changed")
		}
		for _, it := range []sorted.Iterator{first, second} {
			if err := it.Close(); err != nil {
				return err
			}
			if err := it.Close(); err != nil {
				return err
			}
		}
		return nil
	})
}

func TestReindexConcurrentUse(t *testing.T) {
	test.TLog(t)
	k, _ := reindexStore(t)
	s := reindexBegin(t, k)
	s.maxMutations = 3
	start, done := make(chan struct{}), make(chan error, 8)
	for worker := 0; worker < cap(done); worker++ {
		go func() {
			<-start
			for n := 0; n < 8; n++ {
				key := fmt.Sprintf("%d/%d", worker, n)
				if err := s.Set(key, key); err != nil {
					done <- err
					return
				}
				b := s.BeginBatch()
				b.Set(key+"x", "temporary")
				b.Delete(key + "x")
				if err := s.CommitBatch(b); err != nil {
					done <- err
					return
				}
				if v, err := s.Get(key); err != nil || v != key {
					done <- fmt.Errorf("concurrent Get = %q, %v", v, err)
					return
				}
				it := s.Find(key, key+"x")
				ok := it.Next() && it.Key() == key && it.Value() == key && !it.Next()
				if err := it.Close(); err != nil || !ok {
					done <- fmt.Errorf("concurrent Find: %v, valid=%v", err, ok)
					return
				}
			}
			done <- nil
		}()
	}
	close(start)
	reindexWait(t, func() error {
		for range cap(done) {
			if err := <-done; err != nil {
				return err
			}
		}
		return nil
	})
	reindexOK(t, s.Finish(true))
}

func TestReindexBatchOwnership(t *testing.T) {
	test.TLog(t)
	k, _ := reindexStore(t)
	s := reindexBegin(t, k)
	other, _ := reindexStore(t)
	foreign := reindexBegin(t, other).BeginBatch()
	for _, b := range []sorted.BatchMutation{nil, sorted.NewBatchMutation(), foreign, (*reindexBatch)(nil)} {
		if err := s.CommitBatch(b); err == nil {
			t.Fatalf("accepted foreign batch %T", b)
		}
	}
	b := s.BeginBatch()
	b.Set("a", "1")
	reindexOK(t, s.CommitBatch(b))
	b.Set("a", "reused")
	if err := s.CommitBatch(b); !errors.Is(err, sql.ErrTxDone) {
		t.Fatalf("reused batch: %v", err)
	}
	if v, err := s.Get("a"); err != nil || v != "1" {
		t.Fatalf("reused batch changed a: %q, %v", v, err)
	}
	reindexOK(t, s.Finish(true)) // Rejected batches must not poison the session.
}

func TestReindexFailureIsSticky(t *testing.T) {
	test.TLog(t)
	for _, failure := range []string{"Get", "Find", "write", "batch", "commit", "marker"} {
		t.Run(failure, func(t *testing.T) {
			k, db := reindexStore(t)
			s := reindexBegin(t, k)
			s.maxMutations = 1
			reindexOK(t, s.Set("prior", "durable"))
			s.maxMutations = 2
			keys := "prior"
			switch failure {
			case "Get", "Find":
				reindexSQL(t, s, "DROP TABLE rows")
				if failure == "Get" {
					_, _ = s.Get("missing")
				} else {
					it := s.Find("", "")
					if it.Next() {
						t.Fatal("failed Find yielded rows")
					}
					_ = it.Close()
				}
			case "write", "batch":
				if failure == "batch" {
					reindexOK(t, s.Set("head", "pending before the batch"))
					keys = "head,prior"
				}
				reindexSQL(t, s, `CREATE TRIGGER fail_write BEFORE INSERT ON rows WHEN NEW.k='bad' BEGIN SELECT RAISE(ABORT, 'write failure'); END`)
				if failure == "write" {
					_ = s.Set("bad", "ignored")
				} else {
					b := s.BeginBatch()
					b.Set("partial-a", "1")
					b.Set("partial-b", "2")
					b.Set("bad", "3")
					_ = s.CommitBatch(b) // Oversized batch must not split and leak its first two rows.
				}
			case "commit":
				_, err := s.conn.ExecContext(t.Context(), "PRAGMA foreign_keys=ON")
				reindexOK(t, err)
				reindexSQL(t, s, "CREATE TABLE parent (id INTEGER PRIMARY KEY)")
				reindexSQL(t, s, "CREATE TABLE child (id INTEGER REFERENCES parent(id) DEFERRABLE INITIALLY DEFERRED)")
				reindexSQL(t, s, "INSERT INTO child VALUES (99)") // Only COMMIT, not Exec, fails.
				reindexOK(t, s.Set("tail", "pending"))
			case "marker":
				reindexSQL(t, s, `CREATE TRIGGER fail_marker BEFORE DELETE ON meta WHEN OLD.metakey='reindexing' BEGIN SELECT RAISE(ABORT, 'marker failure'); END`)
				reindexOK(t, s.Set("tail", "pending"))
				// Finish commits the last rows, then makes them
				// durable, before it removes the marker; they stay,
				// but the marker still says the index is incomplete.
				keys = "prior,tail"
			}
			err := s.Finish(true)
			if err == nil {
				t.Fatal("ignored SQL error was not returned by Finish")
			}
			if again := s.Finish(false); again != err {
				t.Fatalf("Finish lost sticky error: %v vs %v", again, err)
			}
			if again := s.Close(); again != err {
				t.Fatalf("Close lost sticky error: %v vs %v", again, err)
			}
			reindexObserved(t, db, keys, 1)
			// This uses the parent's gate and pool, detecting leaks and a poisoned native transaction.
			reindexWait(t, func() error { return k.Set("probe", "ok") })
			withProbe := strings.Split(keys+",probe", ",")
			slices.Sort(withProbe)
			reindexObserved(t, db, strings.Join(withProbe, ","), 1)
		})
	}
}

func TestReindexAbortAndClosedSession(t *testing.T) {
	k, db := reindexStore(t)
	s := reindexBegin(t, k)
	s.maxMutations = 1
	reindexOK(t, s.Set("prior", "durable"))
	s.maxMutations = 2
	reindexOK(t, s.Set("tail", "pending"))
	pending := s.BeginBatch()
	reindexOK(t, s.Close())
	reindexOK(t, s.Close())
	if err := s.Finish(true); !errors.Is(err, sql.ErrTxDone) {
		t.Fatalf("aborted session promoted to success: %v", err)
	}
	reindexObserved(t, db, "prior", 1)
	_, getErr := s.Get("prior")
	it := s.Find("", "")
	if it.Next() {
		t.Fatal("closed session iterator yielded rows")
	}
	for _, err := range []error{getErr, s.Set("x", "y"), s.Delete("prior"), s.CommitBatch(pending), s.CommitBatch(s.BeginBatch()), it.Close()} {
		if !errors.Is(err, sql.ErrTxDone) {
			t.Fatalf("closed session error = %v", err)
		}
	}
	reindexWait(t, func() error { _, err := k.Get("prior"); return err })
}

func TestReindexBeginFailureIsAtomic(t *testing.T) {
	k, db := reindexStore(t)
	reindexOK(t, k.Set("old", "retain"))
	_, err := k.db.Exec(`CREATE TRIGGER fail_begin BEFORE INSERT ON meta WHEN NEW.metakey='reindexing' BEGIN SELECT RAISE(ABORT, 'begin failure'); END`)
	reindexOK(t, err)
	if s, err := k.BeginReindex(); s != nil || err == nil {
		t.Fatalf("BeginReindex = %v, %v", s, err)
	}
	reindexObserved(t, db, "old", 0)
	reindexWait(t, func() error { return k.Set("probe", "ok") })
}

func TestReindexCrashRecovery(t *testing.T) {
	test.TLog(t)
	const childEnv = "PERKEEP_SQLITE_REINDEX_CRASH_FILE"
	if file := os.Getenv(childEnv); file != "" {
		kv, err := NewStorage(file)
		reindexOK(t, err)
		s := reindexBegin(t, kv.(*keyValue))
		s.maxMutations = 1
		reindexOK(t, s.Set("prior", "durable"))
		s.maxMutations = 2
		reindexOK(t, s.Set("tail", "pending"))
		os.Exit(23) // No Finish, no Close, no testing cleanup or deferred rollback.
	}
	k, db := reindexStore(t)
	reindexOK(t, k.Set("old", "wipe me"))
	reindexOK(t, k.Close())
	cmd := exec.Command(os.Args[0], "-test.run=^TestReindexCrashRecovery$")
	cmd.Env = append(os.Environ(), childEnv+"="+k.file)
	reindexWait(t, func() error {
		out, err := cmd.CombinedOutput()
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 23 {
			return fmt.Errorf("crash helper: %v\n%s", err, out)
		}
		return nil
	})
	reindexObserved(t, db, "prior", 1)
	live, err := NewStorage(k.file)
	var need sorted.NeedWipeError
	if live == nil || !errors.As(err, &need) {
		t.Fatalf("crashed reopen = %v, %v", live, err)
	}
	defer live.Close()
	reindexOK(t, live.(sorted.Wiper).Wipe())
	reindexObserved(t, db, "", 0)
	s := reindexBegin(t, live.(*keyValue))
	reindexOK(t, s.Set("fresh", "complete"))
	reindexOK(t, s.Finish(true))
	reindexOK(t, live.Close())
	reopened, err := NewStorage(k.file)
	reindexOK(t, err)
	defer reopened.Close()
	v, err := reopened.Get("fresh")
	if err != nil || v != "complete" {
		t.Fatalf("recovered Get = %q, %v", v, err)
	}
	reindexObserved(t, db, "fresh", 0)
}
