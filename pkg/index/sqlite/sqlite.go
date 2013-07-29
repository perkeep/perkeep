/*
Copyright 2012 Google Inc.

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

// Package sqlite implements the Camlistore index storage abstraction
// using an SQLite database file.
package sqlite

import (
	"database/sql"
	"errors"
	"fmt"
	"os"

	"camlistore.org/pkg/blobserver"
	"camlistore.org/pkg/index"
	"camlistore.org/pkg/index/sqlindex"
	"camlistore.org/pkg/jsonconfig"
)

type storage struct {
	*sqlindex.Storage

	file string
	db   *sql.DB
}

var _ index.Storage = (*storage)(nil)

var compiled = false

// CompiledIn returns whether SQLite support is compiled in.
// If it returns false, the build tag "with_sqlite" was not specified.
func CompiledIn() bool {
	return compiled
}

var ErrNotCompiled = errors.New("camlistored was not built with SQLite support. If you built with make.go, use go run make.go --sqlite=true. If you used go get or get install, use go {get,install} --tags=with_sqlite" + compileHint())

func compileHint() string {
	if _, err := os.Stat("/etc/apt"); err == nil {
		return " (Hint: apt-get install libsqlite3-dev)"
	}
	return ""
}

// NewStorage returns an index.Storage implementation of the described SQLite database.
// This exists mostly for testing and does not initialize the schema.
func NewStorage(file string) (index.Storage, error) {
	if !compiled {
		return nil, ErrNotCompiled
	}
	db, err := sql.Open("sqlite3", file)
	if err != nil {
		return nil, err
	}
	return &storage{
		file: file,
		db:   db,
		Storage: &sqlindex.Storage{
			DB: db,
			Serial: true,
		},
	}, nil
}

func newFromConfig(ld blobserver.Loader, config jsonconfig.Obj) (blobserver.Storage, error) {
	var (
		blobPrefix = config.RequiredString("blobSource")
		file       = config.RequiredString("file")
	)
	if err := config.Validate(); err != nil {
		return nil, err
	}
	sto, err := ld.GetStorage(blobPrefix)
	if err != nil {
		return nil, err
	}
	fi, err := os.Stat(file)
	if os.IsNotExist(err) || (err == nil && fi.Size() == 0) {
		return nil, fmt.Errorf(`You need to initialize your SQLite index database with: camtool dbinit --dbname=%s --dbtype=sqlite`, file)
	}
	isto, err := NewStorage(file)
	if err != nil {
		return nil, err
	}
	is := isto.(*storage)

	version, err := is.SchemaVersion()
	if err != nil {
		return nil, fmt.Errorf("error getting schema version (need to init database with 'camtool dbinit %s'?): %v", file, err)
	}

	if err := is.ping(); err != nil {
		return nil, err
	}

	if version != requiredSchemaVersion {
		if os.Getenv("CAMLI_ADVERTISED_PASSWORD") != "" {
			// Good signal that we're using the dev-server script, so help out
			// the user with a more useful tip:
			return nil, fmt.Errorf("database schema version is %d; expect %d (run \"./dev-server --wipe\" to wipe both your blobs and re-populate the database schema)", version, requiredSchemaVersion)
		}
		return nil, fmt.Errorf("database schema version is %d; expect %d (need to re-init/upgrade database?)",
			version, requiredSchemaVersion)
	}

	ix := index.New(is)
	ix.BlobSource = sto
	// Good enough, for now:
	ix.KeyFetcher = ix.BlobSource

	return ix, nil
}

func init() {
	blobserver.RegisterStorageConstructor("sqliteindexer", blobserver.StorageConstructor(newFromConfig))
}

func (mi *storage) ping() error {
	// TODO(bradfitz): something more efficient here?
	_, err := mi.SchemaVersion()
	return err
}

func (mi *storage) SchemaVersion() (version int, err error) {
	err = mi.db.QueryRow("SELECT value FROM meta WHERE metakey='version'").Scan(&version)
	return
}
