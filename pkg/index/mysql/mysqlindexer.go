/*
Copyright 2011 Google Inc.

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

// Package mysql implements the Camlistore index storage abstraction
// on top of MySQL.
package mysql

import (
	"database/sql"
	"fmt"
	"os"

	"camlistore.org/pkg/blobserver"
	"camlistore.org/pkg/index"
	"camlistore.org/pkg/index/sqlindex"
	"camlistore.org/pkg/jsonconfig"

	_ "camlistore.org/third_party/github.com/ziutek/mymysql/godrv"
)

type myIndexStorage struct {
	*sqlindex.Storage

	host, user, password, database string
	db                             *sql.DB
}

var _ index.Storage = (*myIndexStorage)(nil)

// NewStorage returns an index.Storage implementation of the described MySQL database.
// This exists mostly for testing and does not initialize the schema.
func NewStorage(host, user, password, dbname string) (index.Storage, error) {
	// TODO(bradfitz): host is ignored; how to plumb it through with mymysql?
	dsn := dbname + "/" + user + "/" + password
	db, err := sql.Open("mymysql", dsn)
	if err != nil {
		return nil, err
	}
	// TODO(bradfitz): ping db, check that it's reachable.
	return &myIndexStorage{
		db: db,
		Storage: &sqlindex.Storage{
			DB: db,
		},
		host:     host,
		user:     user,
		password: password,
		database: dbname,
	}, nil
}

const fixSchema20to21 = `Character set in tables changed to binary, you can fix your tables with:
ALTER TABLE rows CONVERT TO CHARACTER SET binary;
ALTER TABLE meta CONVERT TO CHARACTER SET binary;
UPDATE meta SET value=21 WHERE metakey='version' AND value=20;
`

func newFromConfig(ld blobserver.Loader, config jsonconfig.Obj) (blobserver.Storage, error) {
	var (
		blobPrefix = config.RequiredString("blobSource")
		host       = config.OptionalString("host", "localhost")
		user       = config.RequiredString("user")
		password   = config.OptionalString("password", "")
		database   = config.RequiredString("database")
	)
	if err := config.Validate(); err != nil {
		return nil, err
	}
	sto, err := ld.GetStorage(blobPrefix)
	if err != nil {
		return nil, err
	}
	isto, err := NewStorage(host, user, password, database)
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
		if version == 20 && requiredSchemaVersion == 21 {
			fmt.Fprintf(os.Stderr, fixSchema20to21)
		}
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
	blobserver.RegisterStorageConstructor("mysqlindexer", blobserver.StorageConstructor(newFromConfig))
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
