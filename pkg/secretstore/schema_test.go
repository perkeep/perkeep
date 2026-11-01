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
	"errors"
	"testing"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// Exercise SQLite directly, bypassing the store's argument validation. CHECK
// constraints are enforced in ordinary tables too; STRICT controls datatypes.
func TestSchemaCheckConstraints(t *testing.T) {
	for _, tc := range []struct {
		name, column string
		index        int
		value        int64
	}{
		{"zero revision", "revision", 2, 0},
		{"negative revision", "revision", 2, -1},
		{"negative migration flag", "migrated", 3, -1},
		{"invalid migration flag", "migrated", 3, 2},
		{"negative deletion flag", "deleted", 5, -1},
		{"invalid deletion flag", "deleted", 5, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := memoryStore(t)
			meta := mustPut(t, s, "existing", "unchanged", PutOptions{})
			var ignored int
			if err := s.db.QueryRow("PRAGMA ignore_check_constraints").Scan(&ignored); err != nil {
				t.Fatal(err)
			}
			if ignored != 0 {
				t.Fatal("CHECK constraints are disabled")
			}
			check := func(t *testing.T, err error) {
				t.Helper()
				var sqlErr *sqlite.Error
				if !errors.As(err, &sqlErr) || sqlErr.Code() != sqlite3.SQLITE_CONSTRAINT_CHECK {
					t.Fatalf("SQL error = %v; want SQLITE_CONSTRAINT_CHECK", err)
				}
			}
			t.Run("insert", func(t *testing.T) {
				args := []any{"invalid", "value", int64(1), int64(0), int64(1), int64(0)}
				args[tc.index] = tc.value
				_, err := s.db.Exec(`INSERT INTO credentials
					(name, value, revision, migrated, updated, deleted)
					VALUES (?, ?, ?, ?, ?, ?)`, args...)
				check(t, err)
				if _, err := s.Get(background, "invalid"); !errors.Is(err, ErrNotFound) {
					t.Fatal("invalid insert changed the database")
				}
			})
			t.Run("update", func(t *testing.T) {
				// The column name comes only from the fixed cases above.
				_, err := s.db.Exec("UPDATE credentials SET "+tc.column+" = ? WHERE name = 'existing'", tc.value)
				check(t, err)
				entry := mustGet(t, s, "existing")
				if entry.Value != "unchanged" || entry.SecretMetadata != meta {
					t.Fatal("invalid update changed the database")
				}
			})
		})
	}
}
