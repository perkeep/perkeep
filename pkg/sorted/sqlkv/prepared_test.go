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

package sqlkv

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"go4.org/syncutil"
	_ "modernc.org/sqlite"
	"perkeep.org/pkg/sorted"
	"perkeep.org/pkg/sorted/kvtest"
)

func preparedSQLite(t *testing.T, path string) *KeyValue {
	t.Helper()
	// Public KV operations have no context. Catch regressions that wait for a
	// second pool connection (or forget to release the gate), including cleanup.
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	stop := context.AfterFunc(ctx, func() { panic("sqlkv integration test timed out: " + t.Name()) })
	t.Cleanup(func() { stop(); cancel() })
	db, err := sql.Open("sqlite", path)
	preparedCheck(t, err)
	db.SetMaxOpenConns(1)
	kv := &KeyValue{DB: db, Gate: syncutil.NewGate(1)}
	t.Cleanup(func() { preparedCheck(t, kv.Close()) })
	_, err = db.ExecContext(ctx, "CREATE TABLE IF NOT EXISTS rows (k TEXT PRIMARY KEY, v TEXT)")
	preparedCheck(t, err)
	return kv
}

func preparedCheck(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func preparedGet(t *testing.T, r interface{ Get(string) (string, error) }, key, want string) {
	t.Helper()
	got, err := r.Get(key)
	if want == "" {
		if !errors.Is(err, sorted.ErrNotFound) || got != "" {
			t.Fatalf("Get(%q) = %q, %v; want ErrNotFound", key, got, err)
		}
		return
	}
	if err != nil || got != want {
		t.Fatalf("Get(%q) = %q, %v; want %q", key, got, err, want)
	}
}

func preparedRows(t *testing.T, it sorted.Iterator, want ...string) {
	t.Helper()
	defer it.Close()
	var got []string
	for it.Next() {
		got = append(got, it.Key()+"="+it.Value())
	}
	preparedCheck(t, it.Close())
	if !slices.Equal(got, want) {
		t.Fatalf("Find = %v; want %v", got, want)
	}
}

func TestPreparedSQLiteSorted(t *testing.T) {
	kv := preparedSQLite(t, ":memory:")
	preparedCheck(t, kv.Prepare())
	kvtest.TestSorted(t, kv)
}

func TestPreparedSQLiteBatchesAndWipe(t *testing.T) {
	kv := preparedSQLite(t, ":memory:")
	preparedCheck(t, kv.Prepare())
	for range 5 {
		b := kv.BeginBatch()
		r := b.(*batchTx)
		b.Set("a", "old")
		b.Set("a", "av")
		b.Set("b", "bv")
		preparedGet(t, r, "a", "av")
		preparedRows(t, r.Find("a", "c"), "a=av", "b=bv")
		b.Delete("b")
		b.Delete("missing")
		preparedGet(t, r, "b", "")
		preparedCheck(t, kv.CommitBatch(b))
		preparedGet(t, kv, "a", "av")
		preparedRows(t, kv.Find("", ""), "a=av")
		preparedRows(t, kv.Find("a", "b"), "a=av")
		preparedCheck(t, kv.Wipe())
		preparedGet(t, kv, "a", "")
		preparedRows(t, kv.Find("", ""))
	}
	preparedCheck(t, kv.Set("a", "again"))
	preparedGet(t, kv, "a", "again")
	preparedCheck(t, kv.Delete("a"))
}

func TestPreparedSQLiteOverlappingReadIterators(t *testing.T) {
	for _, end := range []string{"", "d"} {
		t.Run("end="+end, func(t *testing.T) {
			kv := preparedSQLite(t, ":memory:")
			preparedCheck(t, kv.Prepare())
			for _, k := range []string{"a", "b", "c"} {
				preparedCheck(t, kv.Set(k, k+"v"))
			}
			tx := kv.BeginReadTx()
			defer tx.Close()
			a, b, c := tx.Find("a", end), tx.Find("a", end), tx.Find("a", end)
			defer a.Close()
			defer b.Close()
			defer c.Close()
			for _, it := range []sorted.Iterator{a, b, c} {
				if !it.Next() || it.Key() != "a" || it.Value() != "av" {
					t.Fatal("overlapping iterator lost its first row")
				}
				preparedGet(t, tx, "b", "bv")
			}
			// Closing one partially consumed cursor must not reset its peers.
			preparedCheck(t, b.Close())
			preparedRows(t, a, "b=bv", "c=cv")
			preparedGet(t, tx, "c", "cv")
			preparedRows(t, c, "b=bv", "c=cv")
		})
	}
}

func TestPreparedSQLiteCallbacksAndRollback(t *testing.T) {
	kv := preparedSQLite(t, ":memory:")
	setCalls, batchCalls, replaceQueries := 0, 0, 0
	var exactTx *sql.Tx
	injected := errors.New("injected batch set failure")
	kv.PlaceHolderFunc = func(q string) string {
		if strings.HasPrefix(q, "REPLACE") {
			replaceQueries++
			return "INVALID REPLACE"
		}
		return q
	}
	kv.SetFunc = func(db *sql.DB, k, v string) error {
		setCalls++
		if db != kv.DB {
			t.Fatal("SetFunc received a different DB")
		}
		_, err := db.Exec("INSERT OR REPLACE INTO rows VALUES (?, ?)", k, v)
		return err
	}
	kv.BatchSetFunc = func(tx *sql.Tx, k, v string) error {
		batchCalls++
		if tx != exactTx {
			t.Fatal("BatchSetFunc received a different transaction")
		}
		if k == "fail" {
			return injected
		}
		_, err := tx.Exec("INSERT OR REPLACE INTO rows VALUES (?, ?)", k, v)
		return err
	}
	preparedCheck(t, kv.Prepare())
	preparedCheck(t, kv.Set("a", "av"))
	b := kv.BeginBatch()
	exactTx = b.(*batchTx).tx
	b.Set("b", "bv")
	b.Set("c", "cv")
	b.Delete("a")
	preparedCheck(t, kv.CommitBatch(b))
	b = kv.BeginBatch()
	exactTx = b.(*batchTx).tx
	b.Set("rolled-back", "rv")
	b.Delete("b")
	b.Set("fail", "fv")
	b.Set("skipped", "sv")
	if err := kv.CommitBatch(b); !errors.Is(err, injected) {
		t.Fatalf("CommitBatch = %v; want injected error", err)
	}
	preparedGet(t, kv, "rolled-back", "")
	preparedGet(t, kv, "skipped", "")
	preparedRows(t, kv.Find("", ""), "b=bv", "c=cv")
	preparedCheck(t, kv.Set("a", "after rollback"))
	if setCalls != 2 || batchCalls != 4 || replaceQueries != 0 {
		t.Fatalf("callback/REPLACE counts = %d/%d/%d; want 2/4/0", setCalls, batchCalls, replaceQueries)
	}
}

func TestPreparedSQLiteRetryAndReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prepared.sqlite")
	kv := preparedSQLite(t, path)
	// Fail after other parents were prepared, not just on the first query.
	kv.PlaceHolderFunc = func(q string) string {
		if strings.Contains(q, "AND k <") {
			return strings.Replace(q, "/*TPRE*/rows", "missing", 1)
		}
		return q
	}
	if err := kv.Prepare(); err == nil {
		t.Fatal("Prepare succeeded with a missing table")
	}
	_, err := kv.DB.Exec("CREATE TABLE missing (k TEXT PRIMARY KEY, v TEXT)")
	preparedCheck(t, err)
	preparedCheck(t, kv.Prepare())
	preparedCheck(t, kv.Prepare()) // Successful opt-in is idempotent.
	preparedCheck(t, kv.Set("a", "av"))
	preparedGet(t, kv, "a", "av")
	preparedRows(t, kv.Find("", ""), "a=av")
	// The bounded query deliberately addresses the new, empty table.
	preparedRows(t, kv.Find("a", "b"))
	for range 3 {
		preparedCheck(t, kv.Close())
		if kv.DB.Stats().InUse != 0 {
			t.Fatal("connection still in use after Close")
		}
		kv = preparedSQLite(t, path)
		preparedCheck(t, kv.Prepare())
		preparedGet(t, kv, "a", "av")
		preparedCheck(t, kv.Set("b", "bv"))
		preparedRows(t, kv.Find("a", "c"), "a=av", "b=bv")
	}
}

func TestPreparedSQLiteConcurrentWAL(t *testing.T) {
	// WAL readers can briefly take the write lock to reread the WAL header.
	// Configure the busy timeout through the DSN so every pooled connection
	// gets it, rather than just the connection chosen by a PRAGMA Exec.
	kv := preparedSQLite(t, filepath.Join(t.TempDir(), "wal.sqlite")+
		"?_pragma=busy_timeout(5000)")
	kv.Gate = nil
	kv.DB.SetMaxOpenConns(16)
	_, err := kv.DB.Exec("PRAGMA journal_mode=WAL")
	preparedCheck(t, err)
	preparedCheck(t, kv.Prepare())
	for _, k := range []string{"a", "b", "c"} {
		preparedCheck(t, kv.Set(k, k+"v"))
	}
	tx := kv.BeginReadTx()
	defer tx.Close()
	preparedGet(t, tx, "a", "av") // Establish the WAL snapshot.
	preparedCheck(t, kv.Set("a", "new"))
	preparedGet(t, tx, "a", "av")
	preparedRows(t, tx.Find("a", "d"), "a=av", "b=bv", "c=cv")
	preparedCheck(t, tx.Close())
	preparedGet(t, kv, "a", "new")
	var wg sync.WaitGroup
	defer wg.Wait()
	for range 6 {
		wg.Go(func() {
			for range 20 {
				// Get while a parent Find cursor is active uses another connection.
				it := kv.Find("a", "d")
				v, err := kv.Get("b")
				var got []string
				for it.Next() {
					got = append(got, it.Key()+"="+it.Value())
				}
				closeErr := it.Close()
				if err != nil || v != "bv" || closeErr != nil ||
					!slices.Equal(got, []string{"a=new", "b=bv", "c=cv"}) {
					t.Errorf("concurrent Get/Find: %q, %v; %v, %v", v, err, got, closeErr)
					return
				}
			}
		})
	}
	// Only one application writer; readers never depend on this key, but WAL
	// internals can still cause transient lock contention.
	for range 20 {
		preparedCheck(t, kv.Set("z", "zv"))
		preparedCheck(t, kv.Delete("z"))
	}
	wg.Wait()
}
