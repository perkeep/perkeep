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

// Package sqlite provides an implementation of sorted.KeyValue
// using an SQLite database file.
package sqlite // import "perkeep.org/pkg/sorted/sqlite"

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"runtime"

	_ "modernc.org/sqlite"

	"go4.org/jsonconfig"
	"go4.org/syncutil"
	"perkeep.org/pkg/env"
	"perkeep.org/pkg/sorted"
	"perkeep.org/pkg/sorted/sqlkv"
)

func init() {
	sorted.RegisterKeyValue("sqlite", newKeyValueFromConfig)
}

// NewStorage is a convenience that calls newKeyValueFromConfig
// with file as the sqlite storage file.
func NewStorage(file string) (sorted.KeyValue, error) {
	return newKeyValueFromConfig(jsonconfig.Obj{"file": file})
}

func newKeyValueFromConfig(cfg jsonconfig.Obj) (sorted.KeyValue, error) {
	file := cfg.RequiredString("file")
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	fi, err := os.Stat(file)
	if os.IsNotExist(err) || (err == nil && fi.Size() == 0) {
		if err := InitDB(file); err != nil {
			return nil, fmt.Errorf("could not initialize sqlite DB at %s: %w", file, err)
		}
	}
	db, err := sql.Open("sqlite", file)
	if err != nil {
		return nil, err
	}
	// Reads use their own connections, which in WAL mode don't wait
	// for the writer (or make it wait), so concurrent reads, such as
	// the corpus loading its prefixes or blobpacked's meta lookups
	// during a reindex, can overlap each other and writes. query_only
	// keeps them from writing; the busy timeout covers the brief
	// times a WAL reader can still be told the database is busy.
	readDB, err := sql.Open("sqlite", file+"?_pragma=query_only(1)&_pragma=busy_timeout(10000)")
	if err != nil {
		db.Close()
		return nil, err
	}
	// The pool isn't capped: Find holds a connection until its
	// iterator is closed, so a cap could deadlock callers that read
	// while iterating. But keep enough idle connections that busy
	// readers don't keep reopening them.
	readDB.SetMaxIdleConns(runtime.NumCPU())
	success := false
	defer func() {
		if !success {
			db.Close()
			readDB.Close()
		}
	}()
	kv := &keyValue{
		file: file,
		db:   db,
		KeyValue: &sqlkv.KeyValue{
			DB:     db,
			ReadDB: readDB,
			Gate:   syncutil.NewGate(1),
		},
	}

	version, err := kv.SchemaVersion()
	if err != nil {
		return nil, fmt.Errorf("error getting schema version (need to init database with 'perkeepd admin dbinit --dbtype=sqlite --dbname=%q'?): %w", file, err)
	}

	if err := kv.ping(); err != nil {
		return nil, err
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

	if err := kv.Prepare(); err != nil {
		return nil, fmt.Errorf("preparing SQLite key/value statements: %w", err)
	}
	rowid, err := hasRowidRows(db)
	if err != nil {
		return nil, err
	}
	if rowid {
		log.Printf("sqlite: %s uses an older table layout that makes range scans slow, especially when the file isn't cached. A reindex upgrades an index; to upgrade any SQLite file, stop the server and copy it into a new one with perkeep.org/dev/kvcopy.", file)
	}
	var marker int
	err = db.QueryRow("SELECT 1 FROM meta WHERE metakey='reindexing'").Scan(&marker)
	if err != nil && err != sql.ErrNoRows {
		return nil, fmt.Errorf("checking SQLite reindex marker: %w", err)
	}
	success = true
	if err == nil {
		return kv, sorted.NeedWipeError{Msg: "SQLite database contains an incomplete reindex; a fresh rebuild is required"}
	}
	return kv, nil

}

type keyValue struct {
	*sqlkv.KeyValue

	file string
	db   *sql.DB
}

func (kv *keyValue) ping() error {
	// TODO(bradfitz): something more efficient here?
	_, err := kv.SchemaVersion()
	return err
}

func (kv *keyValue) SchemaVersion() (version int, err error) {
	err = kv.db.QueryRow("SELECT value FROM meta WHERE metakey='version'").Scan(&version)
	return
}

// Wipe preserves the schema and prepared statements, and clears both the data
// and any incomplete-reindex marker in one transaction.
func (kv *keyValue) Wipe() error {
	kv.Gate.Start()
	defer kv.Gate.Done()
	conn, err := kv.db.Conn(context.Background())
	if err != nil {
		return err
	}
	defer conn.Close()
	tx, err := conn.BeginTx(context.Background(), nil)
	if err != nil {
		return err
	}
	defer func() {
		if err := tx.Rollback(); err != nil && err != sql.ErrTxDone {
			discardSQLiteConn(conn)
		}
	}()
	if err := clearRows(tx); err != nil {
		return err
	}
	if _, err := tx.Exec("DELETE FROM meta WHERE metakey='reindexing'"); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		discardSQLiteConn(conn)
		return err
	}
	return nil
}
