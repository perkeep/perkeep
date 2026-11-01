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

package secretstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var background = context.Background()

func privateTempDir(t *testing.T) string {
	t.Helper()
	// In particular, Windows TempDir can inherit a readable/public ACL. Create
	// a private child atomically instead of changing that arbitrary directory.
	dir := filepath.Join(t.TempDir(), "private")
	if err := preparePrivateDirectory(dir); err != nil {
		t.Fatal(err)
	}
	return dir
}

func memoryStore(t *testing.T) *Store {
	t.Helper()
	s, err := NewMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	return s
}

func diskStore(t *testing.T, file string) *Store {
	t.Helper()
	s, err := Open(file)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	return s
}

func mustGet(t *testing.T, s *Store, name string) Entry {
	t.Helper()
	e, err := s.Get(background, name)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func mustPut(t *testing.T, s *Store, name, value string, opts PutOptions) Metadata {
	t.Helper()
	m, err := s.Put(background, name, value, opts)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestPersistenceAndMetadata(t *testing.T) {
	file := filepath.Join(privateTempDir(t), "private", "credentials.sqlite")
	s := diskStore(t, file)
	value := "distinctive-plaintext-value\x00with\nspecial-characters"
	before := time.Now()
	m := mustPut(t, s, "importer/account/accessToken", value, PutOptions{})
	if m.Revision != 1 || m.MigratedFromPermanode || m.Updated.Before(before) {
		t.Fatalf("unexpected initial metadata: %+v", m)
	}
	mustPut(t, s, "aaa", "another-private-value", PutOptions{})
	if err := s.Migrate(background, []LegacyEntry{{Name: m.Name, Value: value}}); err != nil {
		t.Fatal(err)
	}
	e := mustGet(t, s, m.Name)
	if e.Value != value || !e.MigratedFromPermanode || e.Revision != 2 {
		t.Fatal("migration did not preserve value and mark provenance")
	}
	list, err := s.List(background)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].Name != "aaa" || list[1].Name != m.Name {
		t.Fatalf("List = %+v; want sorted metadata", list)
	}
	encoded, err := json.Marshal(list)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "private-value") || strings.Contains(string(encoded), `"value"`) {
		t.Fatal("List metadata disclosed a value")
	}
	if !strings.Contains(string(encoded), `"migratedFromPermanode":true`) {
		t.Fatal("missing migration warning in metadata JSON")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s = diskStore(t, file)
	got := mustGet(t, s, m.Name)
	if got.Value != value || got.Revision != e.Revision ||
		!got.Updated.Equal(e.Updated) || !got.MigratedFromPermanode {
		t.Fatal("reopened credential differs")
	}
	if runtime.GOOS != "windows" {
		for path, want := range map[string]os.FileMode{
			file: 0600, filepath.Dir(file): 0700,
		} {
			fi, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if fi.Mode().Perm() != want {
				t.Errorf("mode = %o, want %o", fi.Mode().Perm(), want)
			}
		}
	}
}

func TestRevisionAndTombstonePersistence(t *testing.T) {
	file := filepath.Join(privateTempDir(t), "credentials.sqlite")
	s := diskStore(t, file)
	m := mustPut(t, s, "token", "old", PutOptions{})
	for _, expected := range []int64{0, m.Revision + 1} {
		if _, err := s.Put(background, "token", "bad", PutOptions{ExpectedRevision: expected}); !errors.Is(err, ErrConflict) {
			t.Fatalf("Put with revision %d: %v; want conflict", expected, err)
		}
	}
	if err := s.Delete(background, "token", m.Revision+1); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale Delete = %v; want conflict", err)
	}
	if err := s.Migrate(background, []LegacyEntry{{Name: "token", Value: "old"}}); err != nil {
		t.Fatal(err)
	}
	m = mustGet(t, s, "token").SecretMetadata
	deletions, err := s.Apply(background, []Mutation{{
		Name: "token", Delete: true, ExpectedRevision: m.Revision,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(deletions) != 1 || !deletions[0].MigratedFromPermanode || deletions[0].Revision != m.Revision+1 {
		t.Fatal("deletion did not retain provenance and revision")
	}
	if err := s.Delete(background, "never-existed", 0); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s = diskStore(t, file)
	if err := s.Migrate(background, []LegacyEntry{
		{Name: "token", Value: "old"},
		{Name: "never-existed", Value: "historical"},
	}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"token", "never-existed"} {
		if _, err := s.Get(background, name); !errors.Is(err, ErrNotFound) {
			t.Fatalf("deleted name %q was resurrected: %v", name, err)
		}
	}
	list, err := s.List(background)
	if err != nil || len(list) != 0 {
		t.Fatalf("List deleted entries = %+v, %v", list, err)
	}
	// Explicit recreation is allowed; it never resets the monotonic revision.
	recreated := mustPut(t, s, "token", "replacement", PutOptions{})
	if recreated.Revision != deletions[0].Revision+1 || !recreated.MigratedFromPermanode {
		t.Fatal("recreation reset revision/provenance")
	}
	if _, err := s.Put(background, "token", "stale", PutOptions{ExpectedRevision: m.Revision}); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale pre-delete revision accepted: %v", err)
	}
}

func TestMigrationAndStickyWarning(t *testing.T) {
	s := memoryStore(t)
	mustPut(t, s, "same", "same-secret", PutOptions{})
	mustPut(t, s, "different", "local-replacement", PutOptions{})
	input := []LegacyEntry{
		{Name: "new", Value: "legacy-secret"},
		{Name: "same", Value: "same-secret"},
		{Name: "different", Value: "old-legacy-secret"},
	}
	if err := s.Migrate(background, input); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"new", "same"} {
		e := mustGet(t, s, name)
		if !e.MigratedFromPermanode {
			t.Fatal("missing migration provenance")
		}
		if err := s.Migrate(background, input); err != nil {
			t.Fatal(err)
		}
		again := mustGet(t, s, name)
		if again != e {
			t.Fatal("idempotent migration changed metadata")
		}
		m := mustPut(t, s, name, "refreshed", PutOptions{ExpectedRevision: e.Revision})
		if !m.MigratedFromPermanode {
			t.Fatal("ordinary refresh cleared migration warning")
		}
		m = mustPut(t, s, name, "rotated", PutOptions{
			ExpectedRevision: m.Revision, ClearMigration: true,
		})
		if m.MigratedFromPermanode {
			t.Fatal("explicit rotation acknowledgement failed")
		}
	}
	e := mustGet(t, s, "different")
	if e.Value != "local-replacement" || e.MigratedFromPermanode || e.Revision != 1 {
		t.Fatal("migration overwrote/marked distinct local credential")
	}
}

func TestStrictStateSnapshotDoesNotUndoDelete(t *testing.T) {
	file := filepath.Join(privateTempDir(t), "credentials.sqlite")
	a, admin := diskStore(t, file), diskStore(t, file)
	mustPut(t, a, "access", "initial-access", PutOptions{})
	access, deleted, err := a.GetState(background, "access")
	if err != nil || deleted || access.Value != "initial-access" || access.Revision != 1 || access.Updated.IsZero() {
		t.Fatalf("live snapshot state unexpected: deleted=%v, err=%v", deleted, err)
	}
	refresh, deleted, err := a.GetState(background, "refresh")
	if err != nil || deleted || refresh.Name != "refresh" || refresh.Revision != 0 || refresh.Value != "" || !refresh.Updated.IsZero() {
		t.Fatalf("never-present snapshot state unexpected: deleted=%v, err=%v", deleted, err)
	}
	// A refresh is in flight using this never-present credential snapshot.
	// An explicit administrator delete of that absent key must invalidate it.
	if err := admin.Delete(background, "refresh", AnyRevision); err != nil {
		t.Fatal(err)
	}
	_, err = a.Apply(background, []Mutation{
		{Name: "access", Value: "stale-access", ExpectedRevision: access.Revision, CompareRecord: true},
		{Name: "refresh", Value: "stale-refresh", ExpectedRevision: refresh.Revision, CompareRecord: true},
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("stale absence snapshot Apply = %v; want conflict", err)
	}
	if e := mustGet(t, a, "access"); e.Value != "initial-access" || e.Revision != access.Revision {
		t.Fatal("strict snapshot conflict partially committed batch")
	}
	state, deleted, err := a.GetState(background, "refresh")
	if err != nil || !deleted || state.Revision != 1 || state.Value != "" || state.Updated.IsZero() {
		t.Fatalf("deleted snapshot state unexpected: deleted=%v, err=%v", deleted, err)
	}
	if _, err := a.Get(background, "refresh"); !errors.Is(err, ErrNotFound) {
		t.Fatal("ordinary Get disclosed tombstone")
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	a = diskStore(t, file)
	reopened, deleted, err := a.GetState(background, "refresh")
	if err != nil || !deleted || reopened != state {
		t.Fatal("tombstone snapshot did not survive reopen")
	}
	// A fresh operation which observed this exact tombstone may create a
	// value. That is different from a stale never-present snapshot.
	results, err := a.Apply(background, []Mutation{{
		Name: "refresh", Value: "fresh-refresh", ExpectedRevision: state.Revision, CompareRecord: true,
	}})
	if err != nil || len(results) != 1 || results[0].Revision != state.Revision+1 {
		t.Fatalf("fresh tombstone snapshot Apply = %v", err)
	}
	if e := mustGet(t, a, "refresh"); e.Value != "fresh-refresh" {
		t.Fatal("strict tombstone snapshot did not create credential")
	}
	// Ordinary owner API creation retains its deliberately broader semantics:
	// expected zero permits explicit recreation after a tombstone.
	if err := admin.Delete(background, "refresh", AnyRevision); err != nil {
		t.Fatal(err)
	}
	explicit := mustPut(t, admin, "refresh", "owner-replacement", PutOptions{})
	if explicit.Revision != state.Revision+3 {
		t.Fatal("ordinary explicit recreation reset revision")
	}
	_, err = a.Apply(background, []Mutation{{
		Name: "refresh", Value: "stale-tombstone-refresh", ExpectedRevision: state.Revision, CompareRecord: true,
	}})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("stale tombstone snapshot Apply = %v; want conflict", err)
	}
}

func TestStrictStateSnapshotCreateUpdateAndDelete(t *testing.T) {
	s := memoryStore(t)
	state, deleted, err := s.GetState(background, "token")
	if err != nil || deleted {
		t.Fatal("failed to capture never-present state")
	}
	for _, mutation := range []Mutation{
		{Name: "token", Value: "created", ExpectedRevision: state.Revision, CompareRecord: true},
		{Name: "token", Value: "updated", ExpectedRevision: 1, CompareRecord: true},
		{Name: "token", Delete: true, ExpectedRevision: 2, CompareRecord: true},
	} {
		if _, err := s.Apply(background, []Mutation{mutation}); err != nil {
			t.Fatal(err)
		}
	}
	state, deleted, err = s.GetState(background, "token")
	if err != nil || !deleted || state.Revision != 3 || state.Value != "" {
		t.Fatal("strict snapshot delete did not produce empty tombstone")
	}
	var raw string
	if err := s.db.QueryRow("SELECT value FROM credentials WHERE name = 'token'").Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if raw != "" {
		t.Fatal("tombstone retained deleted plaintext")
	}
	if _, _, err := s.GetState(background, "../invalid"); !errors.Is(err, ErrInvalidName) {
		t.Fatalf("invalid GetState = %v", err)
	}
}

func TestInvalidUTF8NeverCommits(t *testing.T) {
	s := memoryStore(t)
	secret := "sensitive-value\xffinvalid"
	mustPut(t, s, "existing", "original", PutOptions{})
	_, err := s.Apply(background, []Mutation{
		{Name: "existing", Value: "changed", ExpectedRevision: AnyRevision},
		{Name: "invalid", Value: secret},
	})
	if !errors.Is(err, ErrInvalidValue) || strings.Contains(err.Error(), "sensitive-value") {
		t.Fatal("invalid UTF-8 was accepted or disclosed in error")
	}
	if e := mustGet(t, s, "existing"); e.Value != "original" || e.Revision != 1 {
		t.Fatal("invalid-value batch partially committed")
	}
	if _, err := s.Get(background, "invalid"); !errors.Is(err, ErrNotFound) {
		t.Fatal("invalid UTF-8 was stored")
	}
	err = s.Migrate(background, []LegacyEntry{
		{Name: "existing", Value: "original"},
		{Name: "invalid", Value: secret},
	})
	if !errors.Is(err, ErrInvalidValue) || strings.Contains(err.Error(), "sensitive-value") {
		t.Fatal("invalid UTF-8 migration was accepted or disclosed in error")
	}
	if e := mustGet(t, s, "existing"); e.MigratedFromPermanode || e.Revision != 1 {
		t.Fatal("invalid-value migration partially committed")
	}
	value := "valid UTF-8: 日本語 😀\x00\n"
	mustPut(t, s, "valid", value, PutOptions{})
	wire, err := json.Marshal(mustGet(t, s, "valid"))
	if err != nil {
		t.Fatal(err)
	}
	var decoded Entry
	if err := json.Unmarshal(wire, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Value != value {
		t.Fatal("valid credential did not round-trip JSON exactly")
	}
}

func TestAtomicBatch(t *testing.T) {
	s := memoryStore(t)
	m := mustPut(t, s, "access", "initial-access", PutOptions{})
	_, err := s.Apply(background, []Mutation{
		{Name: "refresh", Value: "new-refresh", ExpectedRevision: 0},
		{Name: "access", Value: "new-access", ExpectedRevision: m.Revision + 1},
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("Apply = %v; want conflict", err)
	}
	if _, err := s.Get(background, "refresh"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("partially committed batch: %v", err)
	}
	if e := mustGet(t, s, "access"); e.Value != "initial-access" || e.Revision != m.Revision {
		t.Fatal("conflicting batch changed existing entry")
	}
	results, err := s.Apply(background, []Mutation{
		{Name: "refresh", Value: "new-refresh"},
		{Name: "access", Value: "new-access", ExpectedRevision: m.Revision},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 || results[0].Name != "refresh" ||
		results[1].Name != "access" || !results[0].Updated.Equal(results[1].Updated) {
		t.Fatal("batch results do not correspond to input")
	}
	_, err = s.Apply(background, []Mutation{
		{Name: "refresh", Delete: true, ExpectedRevision: results[0].Revision},
		{Name: "absent", Delete: true, ExpectedRevision: 9},
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatal("expected delete batch conflict")
	}
	if e := mustGet(t, s, "refresh"); e.Value != "new-refresh" {
		t.Fatal("partially committed delete batch")
	}
	// Invalid later migration entries cannot partially migrate earlier ones.
	err = s.Migrate(background, []LegacyEntry{{Name: "valid", Value: "secret"}, {Name: "../invalid", Value: "secret"}})
	if !errors.Is(err, ErrInvalidName) {
		t.Fatal("expected invalid migration name")
	}
	if _, err := s.Get(background, "valid"); !errors.Is(err, ErrNotFound) {
		t.Fatal("partially committed migration")
	}
}

func TestValidationAndBoundaries(t *testing.T) {
	for _, name := range []string{
		"abc", "account/sha224-123/accessToken", "scope:oauth/client_id",
		"scope:oauth/.token-v2_ABC",
		strings.Repeat("a", MaxNameSize),
	} {
		if err := ValidateName(name); err != nil {
			t.Errorf("ValidateName(valid) = %v", err)
		}
	}
	for _, name := range []string{
		"", "/", ".", "..", "./a", "a/..", "a//b", "/absolute", "trailing/",
		"spaces here", "a\nb", "a\x00b", `a\b`, "é", strings.Repeat("a", MaxNameSize+1),
	} {
		if err := ValidateName(name); !errors.Is(err, ErrInvalidName) {
			t.Errorf("ValidateName(%q) = %v", name, err)
		}
	}
	s := memoryStore(t)
	value := strings.Repeat("v", MaxValueSize)
	mustPut(t, s, "largest", value, PutOptions{})
	if got := mustGet(t, s, "largest").Value; got != value {
		t.Fatal("maximum-size value did not round trip")
	}
	mustPut(t, s, "empty", "", PutOptions{})
	for _, tc := range []struct {
		name string
		m    []Mutation
		want error
	}{
		{"size", []Mutation{{Name: "oversize", Value: value + "x"}}, ErrTooLarge},
		{"revision", []Mutation{{Name: "token", ExpectedRevision: -2}}, ErrInvalidRevision},
		{"strict any revision", []Mutation{{Name: "token", ExpectedRevision: AnyRevision, CompareRecord: true}}, ErrInvalidRevision},
		{"duplicate", []Mutation{{Name: "token"}, {Name: "token"}}, ErrInvalidMutation},
		{"delete value", []Mutation{{Name: "token", Delete: true, Value: "secret"}}, ErrInvalidMutation},
		{"delete clear", []Mutation{{Name: "token", Delete: true, ClearMigration: true}}, ErrInvalidMutation},
		{"batch size", make([]Mutation, MaxBatchSize+1), ErrTooLarge},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := s.Apply(background, tc.m); !errors.Is(err, tc.want) {
				t.Errorf("Apply = %v; want %v", err, tc.want)
			}
		})
	}
	if err := s.Migrate(background, []LegacyEntry{{Name: "dup"}, {Name: "dup"}}); !errors.Is(err, ErrInvalidMutation) {
		t.Fatalf("duplicate migration = %v", err)
	}
	if err := s.Migrate(background, make([]LegacyEntry, MaxBatchSize+1)); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("oversized migration = %v", err)
	}
}

func TestCanceledAndClosed(t *testing.T) {
	s := memoryStore(t)
	ctx, cancel := context.WithCancel(background)
	cancel()
	if _, err := s.Put(ctx, "token", "not-committed", PutOptions{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled Put = %v", err)
	}
	if _, err := s.Get(background, "token"); !errors.Is(err, ErrNotFound) {
		t.Fatal("canceled mutation committed")
	}
	if _, err := s.Get(ctx, "token"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled Get = %v", err)
	}
	if _, _, err := s.GetState(ctx, "token"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled GetState = %v", err)
	}
	if _, err := s.List(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled List = %v", err)
	}
	if err := s.Migrate(ctx, []LegacyEntry{{Name: "token", Value: "not-committed"}}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled migration = %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	for _, run := range []func() error{
		func() error { _, err := s.Get(background, "token"); return err },
		func() error { _, _, err := s.GetState(background, "token"); return err },
		func() error { _, err := s.List(background); return err },
		func() error { _, err := s.Put(background, "token", "secret", PutOptions{}); return err },
		func() error { return s.Delete(background, "token", AnyRevision) },
		func() error { return s.Migrate(background, nil) },
	} {
		if err := run(); !errors.Is(err, ErrClosed) {
			t.Errorf("closed operation = %v", err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal("Close is not idempotent")
	}
}

func TestIndependentOpenConcurrency(t *testing.T) {
	file := filepath.Join(privateTempDir(t), "credentials.sqlite")
	stores := []*Store{diskStore(t, file), diskStore(t, file)}
	const workers = 12
	const iterations = 10
	var wg sync.WaitGroup
	var won atomic.Int64
	errs := make(chan error, workers)
	for i := range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := stores[i%len(stores)].Put(background, "counter", "0", PutOptions{})
			if err == nil {
				won.Add(1)
			} else if !errors.Is(err, ErrConflict) {
				errs <- err
			}
		}()
	}
	wg.Wait()
	if won.Load() != 1 {
		t.Errorf("create-if-absent winners = %d; want 1", won.Load())
	}
	for i := range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s := stores[i%len(stores)]
			for range iterations {
				for {
					e, err := s.Get(background, "counter")
					if err != nil {
						errs <- err
						return
					}
					n, err := strconv.Atoi(e.Value)
					if err != nil {
						errs <- errors.New("non-integer counter value")
						return
					}
					_, err = s.Put(background, "counter", strconv.Itoa(n+1),
						PutOptions{ExpectedRevision: e.Revision})
					if errors.Is(err, ErrConflict) {
						continue
					}
					if err != nil {
						errs <- err
						return
					}
					break
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	e := mustGet(t, stores[0], "counter")
	if e.Value != strconv.Itoa(workers*iterations) || e.Revision != workers*iterations+1 {
		t.Fatal("independent-open CAS lost updates")
	}
}

func TestIndependentMigrationAndBatch(t *testing.T) {
	file := filepath.Join(privateTempDir(t), "credentials.sqlite")
	a, b := diskStore(t, file), diskStore(t, file)
	legacy := []LegacyEntry{{Name: "access", Value: "old-access"}, {Name: "refresh", Value: "old-refresh"}}
	for range 20 {
		var wg sync.WaitGroup
		errs := make(chan error, 2)
		wg.Add(2)
		go func() {
			defer wg.Done()
			errs <- a.Migrate(background, legacy)
		}()
		go func() {
			defer wg.Done()
			_, err := b.Apply(background, []Mutation{
				{Name: "access", Value: "new-access", ExpectedRevision: AnyRevision},
				{Name: "refresh", Value: "new-refresh", ExpectedRevision: AnyRevision},
			})
			errs <- err
		}()
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatal(err)
			}
		}
		if mustGet(t, a, "access").Value != "new-access" ||
			mustGet(t, a, "refresh").Value != "new-refresh" {
			t.Fatal("migration overwrote concurrent atomic replacement")
		}
	}
}

func TestSafePathsAndModes(t *testing.T) {
	dir := privateTempDir(t)
	if _, err := Open(":memory:"); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("Open silently accepted ephemeral path: %v", err)
	}
	if _, err := Open(""); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("Open empty path = %v", err)
	}
	if runtime.GOOS != "windows" {
		// A URI-like filename is treated as a filename, not interpreted by
		// SQLite. These characters are not valid Windows filenames.
		file := filepath.Join(dir, "file:credential?mode=memory&cache=shared")
		s := diskStore(t, file)
		mustPut(t, s, "token", "persisted", PutOptions{})
		s.Close()
		s = diskStore(t, file)
		if mustGet(t, s, "token").Value != "persisted" {
			t.Fatal("filename was interpreted as SQLite options")
		}
	}
	if runtime.GOOS == "windows" {
		t.Skip("Unix permission/symlink semantics")
	}
	existing := filepath.Join(dir, "existing.sqlite")
	if err := os.WriteFile(existing, nil, 0644); err != nil {
		t.Fatal(err)
	}
	diskStore(t, existing)
	fi, err := os.Stat(existing)
	if err != nil || fi.Mode().Perm() != 0600 {
		t.Fatalf("existing DB not tightened to 0600: %v", err)
	}
	existingDir, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	public := filepath.Join(dir, "public")
	if err := os.Mkdir(public, 0755); err != nil {
		t.Fatal(err)
	}
	diskStore(t, filepath.Join(public, "credentials.sqlite"))
	fi, err = os.Stat(public)
	if err != nil || fi.Mode().Perm() != 0755 {
		t.Fatal("Open changed existing directory permissions")
	}
	afterDir, err := os.Stat(dir)
	if err != nil || afterDir.Mode() != existingDir.Mode() {
		t.Fatal("Open changed ancestor directory permissions")
	}
	unsafe := filepath.Join(dir, "unsafe")
	if err := os.Mkdir(unsafe, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(unsafe, 0777); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(filepath.Join(unsafe, "credentials.sqlite")); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("accepted writable parent directory: %v", err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(existing, link); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(link); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("accepted symlink file: %v", err)
	}
	dirLink := filepath.Join(dir, "dirlink")
	if err := os.Symlink(public, dirLink); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(filepath.Join(dirLink, "credentials.sqlite")); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("accepted symlink immediate parent: %v", err)
	}
	sidecar := filepath.Join(dir, "sidecar.sqlite")
	if err := os.Symlink(existing, sidecar+"-journal"); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(sidecar); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("accepted symlink journal: %v", err)
	}
	if _, err := Open(public); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("accepted directory as DB file: %v", err)
	}
}

func TestErrorsDoNotDiscloseValues(t *testing.T) {
	s := memoryStore(t)
	secret := "sensitive-credential-that-must-not-appear"
	mustPut(t, s, "token", secret, PutOptions{})
	_, err := s.Put(background, "token", secret, PutOptions{})
	if !errors.Is(err, ErrConflict) || strings.Contains(err.Error(), secret) {
		t.Fatal("conflict error disclosed plaintext")
	}
	// Trigger a driver error which contains private SQL/data. No driver text
	// must escape through any of the store's errors.
	_, err = s.db.Exec(`CREATE TRIGGER reject_private BEFORE INSERT ON credentials
		BEGIN SELECT RAISE(ABORT, 'sensitive-credential-that-must-not-appear'); END`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Put(background, "other", secret, PutOptions{})
	if !errors.Is(err, ErrStorage) || strings.Contains(err.Error(), secret) {
		t.Fatal("backend error disclosed plaintext")
	}
	err = s.Migrate(background, []LegacyEntry{{Name: "legacy", Value: secret}})
	if !errors.Is(err, ErrStorage) || strings.Contains(err.Error(), secret) {
		t.Fatal("migration error disclosed plaintext")
	}
	file := filepath.Join(privateTempDir(t), "bad.sqlite")
	if err := os.WriteFile(file, []byte(secret), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(file); err == nil || strings.Contains(err.Error(), secret) {
		t.Fatal("corrupt database error disclosed content")
	}
}

func TestJournalPermissionsAndDurabilitySettings(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix permissions")
	}
	file := filepath.Join(privateTempDir(t), "credentials.sqlite")
	s := diskStore(t, file)
	for pragma, want := range map[string]int{
		"synchronous": 3, "secure_delete": 1,
	} {
		var got int
		if err := s.db.QueryRow("PRAGMA " + pragma).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("PRAGMA %s = %d, want %d", pragma, got, want)
		}
	}
	tx, err := s.db.BeginTx(background, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := writeRecord(background, tx, record{Entry: Entry{
		SecretMetadata: Metadata{Name: "token", Revision: 1, Updated: time.Now()},
		Value:          "private",
	}}); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(file + "-journal")
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0600 {
		t.Errorf("journal mode = %o, want 0600", fi.Mode().Perm())
	}
}

func TestRejectForeignDatabase(t *testing.T) {
	file := filepath.Join(privateTempDir(t), "index.sqlite")
	db, err := sql.Open("sqlite", file)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec("CREATE TABLE existing_index (key TEXT, value TEXT)"); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(file); !errors.Is(err, ErrStorage) {
		t.Fatalf("opened foreign SQLite database: %v", err)
	}
	var n int
	if err := db.QueryRow("SELECT count(*) FROM sqlite_schema WHERE name = 'credentials'").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatal("modified foreign database schema")
	}
}

func TestTotalValueLimitRollsBack(t *testing.T) {
	s := memoryStore(t)
	value := strings.Repeat("v", MaxValueSize)
	var mutations []Mutation
	for i := range MaxTotalValueSize / MaxValueSize {
		mutations = append(mutations, Mutation{Name: fmt.Sprintf("value/%03d", i), Value: value})
	}
	if _, err := s.Apply(background, mutations); err != nil {
		t.Fatal(err)
	}
	// Raw byte length, not Unicode rune count, determines the limit.
	if _, err := s.Put(background, "one-more", "é", PutOptions{}); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("over-limit value = %v", err)
	}
	if _, err := s.Get(background, "one-more"); !errors.Is(err, ErrNotFound) {
		t.Fatal("over-limit value committed")
	}
	// A batch can release space and use it again atomically.
	if _, err := s.Apply(background, []Mutation{
		{Name: "value/000", Delete: true, ExpectedRevision: AnyRevision},
		{Name: "one-more", Value: value},
	}); err != nil {
		t.Fatal(err)
	}
	// Input larger than the total bound is rejected without touching SQLite.
	mutations = append(mutations, Mutation{Name: "extra", Value: "x"})
	if _, err := s.Apply(background, mutations); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("oversized batch input = %v", err)
	}
	legacy := make([]LegacyEntry, 0, len(mutations))
	for _, m := range mutations {
		legacy = append(legacy, LegacyEntry{Name: m.Name, Value: m.Value})
	}
	if err := s.Migrate(background, legacy); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("oversized migration input = %v", err)
	}
}

func TestCountLimitRollsBackBatchAndMigration(t *testing.T) {
	s := memoryStore(t)
	// Populate without 10,000 individually synced transactions.
	for start := 0; start < MaxEntries; start += MaxBatchSize {
		var mutations []Mutation
		for i := start; i < min(start+MaxBatchSize, MaxEntries); i++ {
			mutations = append(mutations, Mutation{Name: fmt.Sprintf("entry/%05d", i)})
		}
		if _, err := s.Apply(background, mutations); err != nil {
			t.Fatal(err)
		}
	}
	_, err := s.Apply(background, []Mutation{
		{Name: "entry/00000", Value: "changed", ExpectedRevision: AnyRevision},
		{Name: "over-limit", Value: "secret"},
	})
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("over-limit Apply = %v", err)
	}
	if e := mustGet(t, s, "entry/00000"); e.Value != "" || e.Revision != 1 {
		t.Fatal("size failure partially committed batch")
	}
	err = s.Migrate(background, []LegacyEntry{
		{Name: "entry/00000"},
		{Name: "over-limit", Value: "secret"},
	})
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("over-limit migration = %v", err)
	}
	if mustGet(t, s, "entry/00000").MigratedFromPermanode {
		t.Fatal("size failure partially committed migration")
	}
	// Tombstones count toward the limit and are never discarded.
	if err := s.Delete(background, "entry/00000", AnyRevision); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Put(background, "over-limit", "secret", PutOptions{}); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("tombstone lost its slot: %v", err)
	}
	mustPut(t, s, "entry/00000", "explicitly-recreated", PutOptions{})
}
