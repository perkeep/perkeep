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

package sqlite

import (
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"perkeep.org/pkg/sorted"
	"perkeep.org/pkg/sorted/kvtest"
)

// newRowidDB creates a database at path with an ordinary rowid rows table,
// like the databases Perkeep versions from before October 2026 created,
// holding rows.
func newRowidDB(t *testing.T, path string, rows map[string]string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, q := range []string{
		`CREATE TABLE rows (k VARCHAR(767) NOT NULL PRIMARY KEY, v VARCHAR(63000))`,
		`CREATE TABLE meta (metakey VARCHAR(255) NOT NULL PRIMARY KEY, value VARCHAR(255) NOT NULL)`,
		`PRAGMA journal_mode = WAL`,
		`REPLACE INTO meta VALUES ('version', '1')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	for k, v := range rows {
		if _, err := db.Exec(`INSERT INTO rows VALUES (?, ?)`, k, v); err != nil {
			t.Fatal(err)
		}
	}
	if rowid, err := hasRowidRows(db); err != nil || !rowid {
		t.Fatalf("hasRowidRows of rowid test DB = %v, %v; want true", rowid, err)
	}
}

func openKV(t *testing.T, path string) *keyValue {
	t.Helper()
	kv, err := NewStorage(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { kv.Close() })
	return kv.(*keyValue)
}

func wantLayout(t *testing.T, kv *keyValue, wantRowid bool) {
	t.Helper()
	rowid, err := hasRowidRows(kv.db)
	if err != nil {
		t.Fatal(err)
	}
	if rowid != wantRowid {
		t.Errorf("hasRowidRows = %v; want %v", rowid, wantRowid)
	}
}

// wantRows checks that kv holds exactly want, via Get and Find, which use
// the store's prepared statements.
func wantRows(t *testing.T, kv sorted.KeyValue, want map[string]string) {
	t.Helper()
	got := map[string]string{}
	if err := sorted.Foreach(kv, func(k, v string) error {
		got[k] = v
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(got) != len(want) {
		t.Errorf("rows = %q; want %q", got, want)
	}
	for k, v := range want {
		if g, err := kv.Get(k); err != nil || g != v {
			t.Errorf("Get(%q) = %q, %v; want %q", k, g, err, v)
		}
	}
}

var oldRows = map[string]string{"a": "1", "b": "2", "c": "3"}

func TestNewDBIsWithoutRowid(t *testing.T) {
	wantLayout(t, openKV(t, filepath.Join(t.TempDir(), "new.sqlite")), false)
}

func TestRowidLayoutStillWorks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.sqlite")
	newRowidDB(t, path, nil)
	kv := openKV(t, path)
	wantLayout(t, kv, true)
	kvtest.TestSorted(t, kv)
}

func TestWipeUpgradesRowidLayout(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.sqlite")
	newRowidDB(t, path, oldRows)
	kv := openKV(t, path)
	wantRows(t, kv, oldRows)
	if err := kv.Wipe(); err != nil {
		t.Fatal(err)
	}
	wantLayout(t, kv, false)
	if _, err := kv.Get("a"); !errors.Is(err, sorted.ErrNotFound) {
		t.Errorf("Get(a) after Wipe = %v; want ErrNotFound", err)
	}
	// The prepared statements must still work on the recreated table.
	if err := kv.Set("x", "after wipe"); err != nil {
		t.Fatal(err)
	}
	wantRows(t, kv, map[string]string{"x": "after wipe"})
	kv.Close()

	kv = openKV(t, path)
	wantLayout(t, kv, false)
	wantRows(t, kv, map[string]string{"x": "after wipe"})
}

func TestBeginReindexUpgradesRowidLayout(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.sqlite")
	newRowidDB(t, path, oldRows)
	kv := openKV(t, path)
	s, err := kv.BeginReindex()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Set("x", "rebuilt"); err != nil {
		t.Fatal(err)
	}
	if err := s.Finish(true); err != nil {
		t.Fatal(err)
	}
	wantLayout(t, kv, false)
	wantRows(t, kv, map[string]string{"x": "rebuilt"})
	kv.Close()

	// A completed rebuild reopens normally, without the incomplete marker.
	kv = openKV(t, path)
	wantLayout(t, kv, false)
	wantRows(t, kv, map[string]string{"x": "rebuilt"})
}

func freelistCount(t *testing.T, kv *keyValue) int {
	t.Helper()
	var n int
	if err := kv.db.QueryRow("PRAGMA freelist_count").Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// TestReindexCompacts tests that a successful rebuild VACUUMs the database,
// and an aborted one doesn't.
func TestReindexCompacts(t *testing.T) {
	for _, success := range []bool{true, false} {
		t.Run(map[bool]string{true: "success", false: "abort"}[success], func(t *testing.T) {
			kv := openKV(t, filepath.Join(t.TempDir(), "index.sqlite"))
			b := kv.BeginBatch()
			for i := range 5000 {
				b.Set(fmt.Sprintf("key%05d", i), strings.Repeat("v", 200))
			}
			if err := kv.CommitBatch(b); err != nil {
				t.Fatal(err)
			}
			s, err := kv.BeginReindex()
			if err != nil {
				t.Fatal(err)
			}
			if err := s.Set("x", "rebuilt"); err != nil {
				t.Fatal(err)
			}
			if err := s.Finish(success); err != nil {
				t.Fatal(err)
			}
			// BeginReindex's delete freed the old rows' pages.
			got := freelistCount(t, kv)
			if success && got != 0 {
				t.Errorf("freelist_count after successful rebuild = %d; want 0 (compacted)", got)
			}
			if !success && got == 0 {
				t.Error("freelist_count after aborted rebuild = 0; want freed pages left (not compacted)")
			}
		})
	}
}
