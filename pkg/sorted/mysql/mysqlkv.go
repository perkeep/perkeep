/*
Copyright 2011 The Camlistore Authors.

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

// Package mysql provides an implementation of sorted.KeyValue
// on top of MySQL.
package mysql // import "camlistore.org/pkg/sorted/mysql"

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"camlistore.org/pkg/env"
	"camlistore.org/pkg/sorted"
	"camlistore.org/pkg/sorted/sqlkv"
	_ "github.com/go-sql-driver/mysql"
	"go4.org/jsonconfig"
	"go4.org/syncutil"
)

func init() {
	sorted.RegisterKeyValue("mysql", newKeyValueFromJSONConfig)
}

// newKVDB returns an unusable KeyValue, with a database, but no tables yet. It
// should be followed by Wipe or finalize.
func newKVDB(cfg jsonconfig.Obj) (sorted.KeyValue, error) {
	var (
		user     = cfg.RequiredString("user")
		database = cfg.RequiredString("database")
		host     = cfg.OptionalString("host", "")
		password = cfg.OptionalString("password", "")
	)
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if !validDatabaseName(database) {
		return nil, fmt.Errorf("%q looks like an invalid database name", database)
	}
	var err error
	if host != "" {
		host, err = maybeRemapCloudSQL(host)
		if err != nil {
			return nil, err
		}
		if !strings.Contains(host, ":") {
			host += ":3306"
		}
		host = "tcp(" + host + ")"
	}
	// The DSN does NOT have a database name in it so it's
	// cacheable and can be shared between different queues & the
	// index, all sharing the same database server, cutting down
	// number of TCP connections required. We add the database
	// name in queries instead.
	dsn := fmt.Sprintf("%s:%s@%s/", user, password, host)

	db, err := openOrCachedDB(dsn)
	if err != nil {
		return nil, err
	}

	if err := CreateDB(db, database); err != nil {
		return nil, err
	}
	return &keyValue{
		database: database,
		dsn:      dsn,
		db:       db,
		KeyValue: &sqlkv.KeyValue{
			DB:          db,
			TablePrefix: database + ".",
			Gate:        syncutil.NewGate(20), // arbitrary limit. TODO: configurable, automatically-learned?
		},
	}, nil
}

// Wipe resets the KeyValue by dropping and recreating the database tables.
func (kv *keyValue) Wipe() error {
	if _, err := kv.db.Exec("DROP TABLE IF EXISTS " + kv.database + ".rows"); err != nil {
		return err
	}
	if _, err := kv.db.Exec("DROP TABLE IF EXISTS " + kv.database + ".meta"); err != nil {
		return err
	}
	return kv.finalize()
}

// finalize should be called on a keyValue initialized with newKVDB.
func (kv *keyValue) finalize() error {
	if err := createTables(kv.db, kv.database); err != nil {
		return err
	}

	if err := kv.ping(); err != nil {
		return fmt.Errorf("MySQL db unreachable: %v", err)
	}

	version, err := kv.SchemaVersion()
	if err != nil {
		return fmt.Errorf("error getting current database schema version: %v", err)
	}
	if version == 0 {
		// Newly created table case
		if _, err := kv.db.Exec(fmt.Sprintf(`REPLACE INTO %s.meta VALUES ('version', ?)`, kv.database), requiredSchemaVersion); err != nil {
			return fmt.Errorf("error setting schema version: %v", err)
		}
		return nil
	}
	if version != requiredSchemaVersion {
		if version == 20 && requiredSchemaVersion == 21 {
			fmt.Fprintf(os.Stderr, fixSchema20to21)
		}
		if env.IsDev() {
			// Good signal that we're using the devcam server, so help out
			// the user with a more useful tip:
			return sorted.NeedWipeError{
				Msg: fmt.Sprintf("database schema version is %d; expect %d (run \"devcam server --wipe\" to wipe both your blobs and re-populate the database schema)", version, requiredSchemaVersion),
			}
		}
		return sorted.NeedWipeError{
			Msg: fmt.Sprintf("database schema version is %d; expect %d (need to re-init/upgrade database?)",
				version, requiredSchemaVersion),
		}
	}

	return nil
}

func newKeyValueFromJSONConfig(cfg jsonconfig.Obj) (sorted.KeyValue, error) {
	kv, err := newKVDB(cfg)
	if err != nil {
		return nil, err
	}
	return kv, kv.(*keyValue).finalize()
}

var dbnameRx = regexp.MustCompile(`^[a-zA-Z0-9_]+$`)

// validDatabaseName reports whether dbname is a valid-looking database name.
func validDatabaseName(dbname string) bool {
	return dbnameRx.MatchString(dbname)
}

// CreateDB creates the named database if it does not already exist.
func CreateDB(db *sql.DB, dbname string) error {
	if dbname == "" {
		return errors.New("can not create database: database name is missing")
	}
	if _, err := db.Exec(fmt.Sprintf("CREATE DATABASE IF NOT EXISTS %s", dbname)); err != nil {
		return fmt.Errorf("error creating database %v: %v", dbname, err)
	}
	return nil
}

func createTables(db *sql.DB, database string) error {
	for _, tableSQL := range SQLCreateTables() {
		tableSQL = strings.Replace(tableSQL, "/*DB*/", database, -1)
		if _, err := db.Exec(tableSQL); err != nil {
			errMsg := "error creating table with %q: %v."
			createError := err
			sv, err := serverVersion(db)
			if err != nil {
				return err
			}
			if !hasLargeVarchar(sv) {
				errMsg += "\nYour MySQL server is too old (< 5.0.3) to support VARCHAR larger than 255."
			}
			return fmt.Errorf(errMsg, tableSQL, createError)
		}
	}
	return nil
}

// We keep a cache of open database handles.
var (
	dbsmu sync.Mutex
	dbs   = map[string]*sql.DB{} // DSN -> db
)

func openOrCachedDB(dsn string) (*sql.DB, error) {
	dbsmu.Lock()
	defer dbsmu.Unlock()
	if db, ok := dbs[dsn]; ok {
		return db, nil
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, err
	}
	dbs[dsn] = db
	return db, nil
}

type keyValue struct {
	*sqlkv.KeyValue

	database string
	dsn      string
	db       *sql.DB
}

// Close overrides KeyValue.Close because we need to remove the DB from the pool
// when closing.
func (kv *keyValue) Close() error {
	dbsmu.Lock()
	defer dbsmu.Unlock()
	delete(dbs, kv.dsn)
	return kv.DB.Close()
}

func (kv *keyValue) ping() error {
	// TODO(bradfitz): something more efficient here?
	_, err := kv.SchemaVersion()
	return err
}

// SchemaVersion returns the schema version found in the meta table. If no
// version is found it returns (0, nil), as the table should be considered empty.
func (kv *keyValue) SchemaVersion() (version int, err error) {
	err = kv.db.QueryRow("SELECT value FROM " + kv.KeyValue.TablePrefix + "meta WHERE metakey='version'").Scan(&version)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return
}

const fixSchema20to21 = `Character set in tables changed to binary, you can fix your tables with:
ALTER TABLE rows CONVERT TO CHARACTER SET binary;
ALTER TABLE meta CONVERT TO CHARACTER SET binary;
UPDATE meta SET value=21 WHERE metakey='version' AND value=20;
`

// serverVersion returns the MySQL server version as []int{major, minor, revision}.
func serverVersion(db *sql.DB) ([]int, error) {
	versionRx := regexp.MustCompile(`([0-9]+)\.([0-9]+)\.([0-9]+)-.*`)
	var version string
	if err := db.QueryRow("SELECT VERSION()").Scan(&version); err != nil {
		return nil, fmt.Errorf("error getting MySQL server version: %v", err)
	}
	m := versionRx.FindStringSubmatch(version)
	if len(m) < 4 {
		return nil, fmt.Errorf("bogus MySQL server version: %v", version)
	}
	major, _ := strconv.Atoi(m[1])
	minor, _ := strconv.Atoi(m[2])
	rev, _ := strconv.Atoi(m[3])
	return []int{major, minor, rev}, nil
}

// hasLargeVarchar returns whether the given version (as []int{major, minor, revision})
// supports VARCHAR larger than 255.
func hasLargeVarchar(version []int) bool {
	if len(version) < 3 {
		panic(fmt.Sprintf("bogus mysql server version %v: ", version))
	}
	if version[0] < 5 {
		return false
	}
	if version[1] > 0 {
		return true
	}
	return version[0] == 5 && version[1] == 0 && version[2] >= 3
}
