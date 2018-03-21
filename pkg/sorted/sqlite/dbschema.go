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
	"fmt"
	"log"
	"strconv"

	"perkeep.org/pkg/sorted"
)

const requiredSchemaVersion = 1

func SchemaVersion() int {
	return requiredSchemaVersion
}

func SQLCreateTables() []string {
	// sqlite ignores n in VARCHAR(n), but setting it as such for consistency with
	// other sqls.
	return []string{
		`CREATE TABLE rows (
 k VARCHAR(` + strconv.Itoa(sorted.MaxKeySize) + `) NOT NULL PRIMARY KEY,
 v VARCHAR(` + strconv.Itoa(sorted.MaxValueSize) + `))`,

		`CREATE TABLE meta (
 metakey VARCHAR(255) NOT NULL PRIMARY KEY,
 value VARCHAR(255) NOT NULL)`,
	}
}

// InitDB creates a new sqlite database based on the file at path.
func InitDB(path string) error {
	db, err := sql.Open("sqlite3", path)
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
		log.Fatalf("Unable to determine sqlite3 journal_mode: %v", err)
	}
	if journalMode != "wal" {
		log.Fatal("SQLite Write Ahead Logging (introducted in v3.7.0) is required. See http://perkeep.org/issue/114")
	}

	_, err = db.Exec(fmt.Sprintf(`REPLACE INTO meta VALUES ('version', '%d')`, SchemaVersion()))
	return err
}
