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

package importer

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"perkeep.org/pkg/secretstore"
)

func TestCredentialSnapshotCompareAndSwap(t *testing.T) {
	ctx := context.Background()
	const access, refresh = "oauthAccessToken", "stravaRefreshToken"
	for _, operation := range []string{"rotate", "replace-refresh", "delete-refresh", "replace-access", "delete-access"} {
		t.Run(operation, func(t *testing.T) {
			f := newObjectClaimFixture(t, nil)
			store := f.o.h.secrets
			if err := store.Migrate(ctx, []secretstore.LegacyEntry{
				{Name: f.o.secretName(access), Value: "old-access"},
				{Name: f.o.secretName(refresh), Value: "old-refresh"},
			}); err != nil {
				t.Fatal(err)
			}
			snapshot, err := f.o.CredentialSnapshot(ctx, access, refresh)
			if err != nil {
				t.Fatal(err)
			}
			if snapshot.Value(refresh) != "old-refresh" {
				t.Fatal("snapshot did not capture refresh credential")
			}
			key := refresh
			if operation == "replace-access" || operation == "delete-access" {
				key = access
			}
			switch operation {
			case "replace-refresh", "replace-access":
				if _, err := store.Put(ctx, f.o.secretName(key), "administrator-replacement",
					secretstore.PutOptions{ExpectedRevision: secretstore.AnyRevision}); err != nil {
					t.Fatal(err)
				}
			case "delete-refresh", "delete-access":
				if err := store.Delete(ctx, f.o.secretName(key), secretstore.AnyRevision); err != nil {
					t.Fatal(err)
				}
			}
			err = f.o.SetSecretAttrsIfRevision(ctx, snapshot, access, "new-access", refresh, "new-refresh")
			if operation == "rotate" {
				if err != nil {
					t.Fatal(err)
				}
				for _, key := range []string{access, refresh} {
					entry, err := store.Get(ctx, f.o.secretName(key))
					if err != nil || !entry.MigratedFromPermanode {
						t.Fatal("refresh lost sticky migration warning")
					}
				}
				if f.o.Attr(access) != "new-access" || f.o.Attr(refresh) != "new-refresh" {
					t.Fatal("refresh was not saved")
				}
			} else {
				if !errors.Is(err, secretstore.ErrConflict) {
					t.Fatalf("stale update = %v; want revision conflict", err)
				}
				want := "administrator-replacement"
				if operation == "delete-refresh" || operation == "delete-access" {
					want = ""
				}
				if f.o.Attr(key) != want {
					t.Fatal("stale refresh overwrote administrator change")
				}
				other, wantOther := access, "old-access"
				if key == access {
					other, wantOther = refresh, "old-refresh"
				}
				if f.o.Attr(other) != wantOther {
					t.Fatal("conflicting transaction partially persisted")
				}
			}
			if len(f.target.uploads()) != 0 {
				t.Fatal("credential snapshot update uploaded signed claims")
			}
		})
	}
}

func TestCredentialSnapshotAbsentAndValidation(t *testing.T) {
	ctx := context.Background()
	const key = "oauthAccessToken"
	f := newObjectClaimFixture(t, map[string][]string{key: {"historical-do-not-use"}})
	snapshot, err := f.o.CredentialSnapshot(ctx, key)
	if err != nil || snapshot.Value(key) != "" {
		t.Fatal("absent secret fell back to historical signed value")
	}
	for _, args := range [][]string{nil, {key}, {"title", "must-not-sign"}, {key, "a", key, "b"}} {
		if err := f.o.SetSecretAttrsIfRevision(ctx, snapshot, args...); err == nil {
			t.Fatal("invalid snapshot update accepted")
		}
	}
	if _, err := f.o.CredentialSnapshot(ctx, "title"); err == nil {
		t.Fatal("non-secret snapshot accepted")
	}
	other := newObjectClaimFixture(t, nil)
	if err := other.o.SetSecretAttrsIfRevision(ctx, snapshot, key, "wrong-object"); err == nil {
		t.Fatal("snapshot used for a different object")
	}
	if err := f.o.SetSecretAttrsIfRevision(ctx, snapshot, key, "new-private-value"); err != nil {
		t.Fatal(err)
	}
	if _, retained := f.o.attr[key]; retained {
		t.Fatal("credential retained in claim attribute map")
	}
	if err := f.o.SetSecretAttrsIfRevision(ctx, snapshot, key, "stale"); !errors.Is(err, secretstore.ErrConflict) {
		t.Fatalf("reused absent snapshot = %v; want conflict", err)
	}
	if len(f.target.uploads()) != 0 || len(other.target.uploads()) != 0 {
		t.Fatal("snapshot update signed credentials")
	}
}

func TestCredentialSnapshotUnavailableStoreFailsClosed(t *testing.T) {
	ctx := context.Background()
	const key = "oauthAccessToken"
	for _, state := range []string{"missing", "closed"} {
		t.Run(state, func(t *testing.T) {
			f := newObjectClaimFixture(t, map[string][]string{key: {"historical-do-not-use"}})
			snapshot, err := f.o.CredentialSnapshot(ctx, key)
			if err != nil {
				t.Fatal(err)
			}
			if state == "missing" {
				store := f.o.h.secrets
				defer func() { f.o.h.secrets = store }()
				f.o.h.secrets = nil
			} else if err := f.o.h.secrets.Close(); err != nil {
				t.Fatal(err)
			}
			if _, err := f.o.CredentialSnapshot(ctx, key); err == nil {
				t.Fatal("unavailable store snapshot did not fail closed")
			}
			if err := f.o.SetSecretAttrsIfRevision(ctx, snapshot, key, "must-not-sign"); err == nil {
				t.Fatal("unavailable store update did not fail closed")
			}
			if len(f.target.uploads()) != 0 {
				t.Fatal("unavailable store signed credential claims")
			}
		})
	}
}

func TestCredentialSnapshotAbsentDeletionConflict(t *testing.T) {
	ctx := context.Background()
	const key = "oauthAccessToken"
	for _, tombstone := range []bool{false, true} {
		for _, intervene := range []bool{false, true} {
			name := fmt.Sprintf("tombstone=%t/intervening-delete=%t", tombstone, intervene)
			t.Run(name, func(t *testing.T) {
				f := newObjectClaimFixture(t, nil)
				store := f.o.h.secrets
				if tombstone {
					if err := store.Delete(ctx, f.o.secretName(key), 0); err != nil {
						t.Fatal(err)
					}
				}
				snapshot, err := f.o.CredentialSnapshot(ctx, key)
				if err != nil || snapshot.Value(key) != "" {
					t.Fatal("absent credential snapshot failed")
				}
				if intervene {
					if err := store.Delete(ctx, f.o.secretName(key), secretstore.AnyRevision); err != nil {
						t.Fatal(err)
					}
				}
				err = f.o.SetSecretAttrsIfRevision(ctx, snapshot, key, "new-access")
				if intervene {
					if !errors.Is(err, secretstore.ErrConflict) {
						t.Fatalf("absent credential deleted during operation: %v; want conflict", err)
					}
					if _, err := store.Get(ctx, f.o.secretName(key)); !errors.Is(err, secretstore.ErrNotFound) {
						t.Fatal("intervening deletion was resurrected")
					}
				} else if err != nil || f.o.Attr(key) != "new-access" {
					t.Fatalf("unchanged absent state could not be replaced: %v", err)
				}
			})
		}
	}
}

func TestCredentialSnapshotPendingObjectPersistence(t *testing.T) {
	ctx := context.Background()
	const key = "oauthAccessToken"
	f := newObjectClaimFixture(t, nil)
	pending, err := f.o.h.newPendingObject()
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := pending.CredentialSnapshot(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	f.target.err = errors.New("injected pending permanode failure")
	if err := pending.SetSecretAttrsIfRevision(ctx, snapshot, key, "new-private-value"); err == nil {
		t.Fatal("pending permanode failure ignored")
	}
	if !pending.isPending() {
		t.Fatal("failed pending persistence was not retryable")
	}
	if _, err := f.o.h.secrets.Get(ctx, pending.secretName(key)); !errors.Is(err, secretstore.ErrNotFound) {
		t.Fatal("credential saved before pending permanode persisted")
	}
	f.target.err = nil
	if err := pending.SetSecretAttrsIfRevision(ctx, snapshot, key, "new-private-value"); err != nil {
		t.Fatal(err)
	}
	if pending.isPending() || pending.Attr(key) != "new-private-value" {
		t.Fatal("pending credential update did not persist")
	}
	for _, body := range f.target.uploads() {
		if strings.Contains(body, "new-private-value") {
			t.Fatal("pending permanode contained credential")
		}
	}
}
