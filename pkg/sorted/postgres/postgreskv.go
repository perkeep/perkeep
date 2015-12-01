/*
Copyright 2012 The Camlistore Authors.

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
package postgres

import (
	"database/sql"
	"fmt"
	"regexp"

	"camlistore.org/pkg/env"
	"camlistore.org/pkg/sorted"
	"camlistore.org/pkg/sorted/sqlkv"
	"go4.org/jsonconfig"

	_ "camlistore.org/third_party/github.com/lib/pq"
)

func init() {
	sorted.RegisterKeyValue("postgres", newKeyValueFromJSONConfig)
}

func newKeyValueFromJSONConfig(cfg jsonconfig.Obj) (sorted.KeyValue, error) {
	conninfo := fmt.Sprintf("user=%s dbname=%s host=%s password=%s sslmode=%s",
		cfg.RequiredString("user"),
		cfg.RequiredString("database"),
		cfg.OptionalString("host", "localhost"),
		cfg.OptionalString("password", ""),
		cfg.OptionalString("sslmode", "require"),
	)
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	db, err := sql.Open("postgres", conninfo)
	if err != nil {
		return nil, err
	}
	for _, tableSql := range SQLCreateTables() {
		if _, err := db.Exec(tableSql); err != nil {
			return nil, fmt.Errorf("error creating table with %q: %v", tableSql, err)
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
		return []byte(fmt.Sprintf("$%d", i))
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
