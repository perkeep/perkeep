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
	"database/sql"
	"fmt"
	"os"

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
	kv := &keyValue{
		file: file,
		db:   db,
		KeyValue: &sqlkv.KeyValue{
			DB:   db,
			Gate: syncutil.NewGate(1),
		},
	}

	version, err := kv.SchemaVersion()
	if err != nil {
		return nil, fmt.Errorf("error getting schema version (need to init database with 'camtool dbinit %s'?): %w", file, err)
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
