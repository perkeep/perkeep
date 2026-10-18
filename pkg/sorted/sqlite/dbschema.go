/*
Copyright 2012 The Perkeep Authors.

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
	"strconv"

	"perkeep.org/pkg/sorted"
)

const requiredSchemaVersion = 1

func SchemaVersion() int {
	return requiredSchemaVersion
}

// rowsTableSQL creates the rows table as a WITHOUT ROWID table, which
// stores rows in key order in a single B-tree. A key range scan then reads
// the table's pages in order. In an ordinary rowid table, the text primary
// key is a separate index, so a scan looks up each row's value by rowid in
// insertion order, with a random read per row when the file isn't cached,
// and every key is stored twice.
func rowsTableSQL() string {
	// sqlite ignores n in VARCHAR(n), but setting it as such for consistency with
	// other sqls.
	return `CREATE TABLE rows (
 k VARCHAR(` + strconv.Itoa(sorted.MaxKeySize) + `) NOT NULL PRIMARY KEY,
 v VARCHAR(` + strconv.Itoa(sorted.MaxValueSize) + `)) WITHOUT ROWID`
}

// queryer is a *sql.DB or *sql.Tx.
type queryer interface {
	QueryRow(query string, args ...any) *sql.Row
	Exec(query string, args ...any) (sql.Result, error)
}

// hasRowidRows reports whether q's rows table is an ordinary rowid table
// rather than the WITHOUT ROWID table rowsTableSQL creates. Databases
// created by Perkeep versions from before October 2026 have one.
func hasRowidRows(q queryer) (bool, error) {
	var wr int
	if err := q.QueryRow("SELECT wr FROM pragma_table_list('rows') WHERE schema = 'main'").Scan(&wr); err != nil {
		return false, fmt.Errorf("checking SQLite rows table layout: %w", err)
	}
	return wr == 0, nil
}

// clearRows deletes all rows. A rowid rows table is dropped and recreated
// WITHOUT ROWID instead, upgrading its layout, since it's being emptied
// anyway.
func clearRows(q queryer) error {
	rowid, err := hasRowidRows(q)
	if err != nil {
		return err
	}
	if !rowid {
		_, err := q.Exec("DELETE FROM rows")
		return err
	}
	if _, err := q.Exec("DROP TABLE rows"); err != nil {
		return err
	}
	_, err = q.Exec(rowsTableSQL())
	return err
}

func SQLCreateTables() []string {
	return []string{
		rowsTableSQL(),

		`CREATE TABLE meta (
 metakey VARCHAR(255) NOT NULL PRIMARY KEY,
 value VARCHAR(255) NOT NULL)`,
	}
}

// InitDB creates a new sqlite database based on the file at path.
func InitDB(path string) error {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return err
	}
	defer db.Close()
	for _, tableSQL := range SQLCreateTables() {
		if _, err := db.Exec(tableSQL); err != nil {
			return err
		}
	}

	// Use Write Ahead Logging which improves SQLite concurrency.
	// Requires SQLite >= 3.7.0
	if _, err := db.Exec("PRAGMA journal_mode = WAL"); err != nil {
		return err
	}

	// Check if the WAL mode was set correctly
	var journalMode string
	if err = db.QueryRow("PRAGMA journal_mode").Scan(&journalMode); err != nil {
		return fmt.Errorf("Unable to determine sqlite3 journal_mode: %w", err)
	}
	if journalMode != "wal" {
		return errors.New("SQLite Write Ahead Logging (introducted in v3.7.0) is required. See http://perkeep.org/issue/114")
	}

	_, err = db.Exec(fmt.Sprintf(`REPLACE INTO meta VALUES ('version', '%d')`, SchemaVersion()))
	return err
}
