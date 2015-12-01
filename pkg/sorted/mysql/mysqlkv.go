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
package mysql

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
	_ "camlistore.org/third_party/github.com/go-sql-driver/mysql"
	"go4.org/jsonconfig"
)

func init() {
	sorted.RegisterKeyValue("mysql", newKeyValueFromJSONConfig)
}

func newKeyValueFromJSONConfig(cfg jsonconfig.Obj) (sorted.KeyValue, error) {
	var (
		user     = cfg.RequiredString("user")
		database = cfg.RequiredString("database")
		host     = cfg.OptionalString("host", "")
		password = cfg.OptionalString("password", "")
	)
	if err := cfg.Validate(); err != nil {
		return nil, err
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
	for _, tableSQL := range SQLCreateTables() {
		tableSQL = strings.Replace(tableSQL, "/*DB*/", database, -1)
		if _, err := db.Exec(tableSQL); err != nil {
			errMsg := "error creating table with %q: %v."
			createError := err
			sv, err := serverVersion(db)
			if err != nil {
				return nil, err
			}
			if !hasLargeVarchar(sv) {
				errMsg += "\nYour MySQL server is too old (< 5.0.3) to support VARCHAR larger than 255."
			}
			return nil, fmt.Errorf(errMsg, tableSQL, createError)
		}
	}
	if _, err := db.Exec(fmt.Sprintf(`REPLACE INTO %s.meta VALUES ('version', '%d')`, database, SchemaVersion())); err != nil {
		return nil, fmt.Errorf("error setting schema version: %v", err)
	}

	kv := &keyValue{
		db: db,
		KeyValue: &sqlkv.KeyValue{
			DB:          db,
			TablePrefix: database + ".",
		},
	}
	if err := kv.ping(); err != nil {
		return nil, fmt.Errorf("MySQL db unreachable: %v", err)
	}
	version, err := kv.SchemaVersion()
	if err != nil {
		return nil, fmt.Errorf("error getting schema version (need to init database?): %v", err)
	}
	if version != requiredSchemaVersion {
		if version == 20 && requiredSchemaVersion == 21 {
			fmt.Fprintf(os.Stderr, fixSchema20to21)
		}
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

	db *sql.DB
}

func (kv *keyValue) ping() error {
	// TODO(bradfitz): something more efficient here?
	_, err := kv.SchemaVersion()
	return err
}

func (kv *keyValue) SchemaVersion() (version int, err error) {
	err = kv.db.QueryRow("SELECT value FROM " + kv.KeyValue.TablePrefix + "meta WHERE metakey='version'").Scan(&version)
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
