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

// Package postgres implements the Camlistore index storage abstraction
// on top of Postgres.
package postgres

import (
	"database/sql"
	"fmt"
	"os"
	"regexp"

	"camlistore.org/pkg/blobserver"
	"camlistore.org/pkg/index"
	"camlistore.org/pkg/index/sqlindex"
	"camlistore.org/pkg/jsonconfig"
	"camlistore.org/pkg/sorted"

	_ "camlistore.org/third_party/github.com/lib/pq"
)

type myIndexStorage struct {
	*sqlindex.Storage
	host, user, password, database string
	db                             *sql.DB
}

var _ sorted.KeyValue = (*myIndexStorage)(nil)

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

// NewStorage returns an sorted.KeyValue implementation of the described PostgreSQL database.
// This exists mostly for testing and does not initialize the schema.
func NewStorage(host, user, password, dbname, sslmode string) (sorted.KeyValue, error) {
	conninfo := fmt.Sprintf("user=%s dbname=%s host=%s password=%s sslmode=%s", user, dbname, host, password, sslmode)
	db, err := sql.Open("postgres", conninfo)
	if err != nil {
		return nil, err
	}
	return &myIndexStorage{
		db: db,
		Storage: &sqlindex.Storage{
			DB:              db,
			SetFunc:         altSet,
			BatchSetFunc:    altBatchSet,
			PlaceHolderFunc: replacePlaceHolders,
		},
		host:     host,
		user:     user,
		password: password,
		database: dbname,
	}, nil
}

func newFromConfig(ld blobserver.Loader, config jsonconfig.Obj) (blobserver.Storage, error) {
	var (
		blobPrefix = config.RequiredString("blobSource")
		host       = config.OptionalString("host", "localhost")
		user       = config.RequiredString("user")
		password   = config.OptionalString("password", "")
		database   = config.RequiredString("database")
		sslmode    = config.OptionalString("sslmode", "require")
	)
	if err := config.Validate(); err != nil {
		return nil, err
	}
	sto, err := ld.GetStorage(blobPrefix)
	if err != nil {
		return nil, err
	}
	isto, err := NewStorage(host, user, password, database, sslmode)
	if err != nil {
		return nil, err
	}
	is := isto.(*myIndexStorage)
	if err := is.ping(); err != nil {
		return nil, err
	}

	version, err := is.SchemaVersion()
	if err != nil {
		return nil, fmt.Errorf("error getting schema version (need to init database?): %v", err)
	}
	if version != requiredSchemaVersion {
		if os.Getenv("CAMLI_DEV_CAMLI_ROOT") != "" {
			// Good signal that we're using the devcam server, so help out
			// the user with a more useful tip:
			return nil, fmt.Errorf("database schema version is %d; expect %d (run \"devcam server --wipe\" to wipe both your blobs and re-populate the database schema)", version, requiredSchemaVersion)
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
	blobserver.RegisterStorageConstructor("postgresindexer", blobserver.StorageConstructor(newFromConfig))
}

func (mi *myIndexStorage) ping() error {
	// TODO(bradfitz): something more efficient here?
	_, err := mi.SchemaVersion()
	return err
}

func (mi *myIndexStorage) SchemaVersion() (version int, err error) {
	err = mi.db.QueryRow("SELECT value FROM meta WHERE metakey='version'").Scan(&version)
	return
}
