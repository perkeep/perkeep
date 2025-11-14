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

// Package postgres provides an implementation of sorted.KeyValue
// on top of PostgreSQL.
package postgres // import "perkeep.org/pkg/sorted/postgres"

import (
	"database/sql"
	"errors"
	"fmt"
	"regexp"

	"go4.org/jsonconfig"
	"perkeep.org/pkg/env"
	"perkeep.org/pkg/sorted"
	"perkeep.org/pkg/sorted/sqlkv"

	_ "github.com/lib/pq"
)

func init() {
	sorted.RegisterKeyValue("postgres", newKeyValueFromJSONConfig)
}

func newKeyValueFromJSONConfig(cfg jsonconfig.Obj) (sorted.KeyValue, error) {
	var (
		user     = cfg.RequiredString("user")
		database = cfg.RequiredString("database")
		host     = cfg.OptionalString("host", "localhost")
		password = cfg.OptionalString("password", "")
		sslmode  = cfg.OptionalString("sslmode", "require")
	)
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	// connect without a database, it may not exist yet
	conninfo := fmt.Sprintf("user=%s host=%s sslmode=%s", user, host, sslmode)
	if password != "" {
		conninfo += fmt.Sprintf(" password=%s", password)
	}
	db, err := sql.Open("postgres", conninfo)
	if err != nil {
		return nil, err
	}
	err = createDB(db, database)
	db.Close() // ignoring error, if createDB failed db.Close() will likely also fail
	if err != nil {
		return nil, err
	}

	// reconnect after database is created
	conninfo += fmt.Sprintf(" dbname=%s", database)
	db, err = sql.Open("postgres", conninfo)
	if err != nil {
		return nil, err
	}

	for _, tableSQL := range SQLCreateTables() {
		if _, err := db.Exec(tableSQL); err != nil {
			return nil, fmt.Errorf("error creating table with %q: %v", tableSQL, err)
		}
	}
	for _, statement := range SQLDefineReplace() {
		if _, err := db.Exec(statement); err != nil {
			return nil, fmt.Errorf("error setting up replace statement with %q: %v", statement, err)
		}
	}
	r, err := db.Query(fmt.Sprintf(`SELECT replaceintometa('version', '%d')`, SchemaVersion()))
	if err != nil {
		return nil, fmt.Errorf("error setting schema version: %v", err)
	}
	r.Close()

	kv := &keyValue{
		db: db,
		KeyValue: &sqlkv.KeyValue{
			DB:              db,
			SetFunc:         altSet,
			BatchSetFunc:    altBatchSet,
			PlaceHolderFunc: replacePlaceHolders,
		},
	}
	if err := kv.ping(); err != nil {
		return nil, fmt.Errorf("PostgreSQL db unreachable: %v", err)
	}
	version, err := kv.SchemaVersion()
	if err != nil {
		return nil, fmt.Errorf("error getting schema version (need to init database?): %v", err)
	}
	if version != requiredSchemaVersion {
		if env.IsDev() {
			// Good signal that we're using the devcam server, so help out
			// the user with a more useful tip:
			return nil, fmt.Errorf("database schema version is %d; expect %d (run \"devcam server --wipe\" to wipe both your blobs and re-populate the database schema)", version, requiredSchemaVersion)
		}
		return nil, fmt.Errorf("database schema version is %d; expect %d (need to re-init/upgrade database?)",
			version, requiredSchemaVersion)
	}

	return kv, nil
}

type keyValue struct {
	*sqlkv.KeyValue
	db *sql.DB
}

// postgres does not have REPLACE INTO (upsert), so we use that custom
// one for Set operations instead
func altSet(db *sql.DB, key, value string) error {
	r, err := db.Query("SELECT replaceinto($1, $2)", key, value)
	if err != nil {
		return err
	}
	return r.Close()
}

// postgres does not have REPLACE INTO (upsert), so we use that custom
// one for Set operations in batch instead
func altBatchSet(tx *sql.Tx, key, value string) error {
	r, err := tx.Query("SELECT replaceinto($1, $2)", key, value)
	if err != nil {
		return err
	}
	return r.Close()
}

var qmark = regexp.MustCompile(`\?`)

// replace all ? placeholders into the corresponding $n in queries
var replacePlaceHolders = func(query string) string {
	i := 0
	dollarInc := func(b []byte) []byte {
		i++
		return fmt.Appendf(nil, "$%d", i)
	}
	return string(qmark.ReplaceAllFunc([]byte(query), dollarInc))
}

func (kv *keyValue) ping() error {
	_, err := kv.SchemaVersion()
	return err
}

func (kv *keyValue) SchemaVersion() (version int, err error) {
	err = kv.db.QueryRow("SELECT value FROM meta WHERE metakey='version'").Scan(&version)
	return
}

var validDatabaseRegex = regexp.MustCompile(`^[a-zA-Z0-9\-_]+$`)

func validDatabaseName(database string) bool {
	return validDatabaseRegex.MatchString(database)
}

func createDB(db *sql.DB, database string) error {
	if database == "" {
		return errors.New("database name can't be empty")
	}

	rows, err := db.Query(`SELECT 1 FROM pg_database WHERE datname = $1`, database)
	if err != nil {
		return err
	}
	defer rows.Close()
	if rows.Next() {
		return nil // database is already created
	}

	// Verify database only has runes we expect
	if !validDatabaseName(database) {
		return fmt.Errorf("Invalid postgres database name: %q", database)
	}
	_, err = db.Exec(fmt.Sprintf("CREATE DATABASE %s", database))
	if err != nil {
		return err
	}
	return err
}
