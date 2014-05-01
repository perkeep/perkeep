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
	"fmt"
	"os"
	"strings"

	"camlistore.org/pkg/jsonconfig"
	"camlistore.org/pkg/sorted"
	"camlistore.org/pkg/sorted/sqlkv"

	_ "camlistore.org/third_party/github.com/go-sql-driver/mysql"
)

func init() {
	sorted.RegisterKeyValue("mysql", newKeyValueFromJSONConfig)
}

func newKeyValueFromJSONConfig(cfg jsonconfig.Obj) (sorted.KeyValue, error) {
	host := cfg.OptionalString("host", "")
	dsn := fmt.Sprintf("%s:%s@/%s",
		cfg.RequiredString("user"),
		cfg.OptionalString("password", ""),
		cfg.RequiredString("database"),
	)
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if host != "" {
		// TODO(mpl): document that somewhere
		if !strings.Contains(host, ":") {
			host = host + ":3306"
		}
		dsn = strings.Replace(dsn, "@", fmt.Sprintf("@tcp(%v)", host), 1)
	}

	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, err
	}
	for _, tableSql := range SQLCreateTables() {
		if _, err := db.Exec(tableSql); err != nil {
			return nil, fmt.Errorf("error creating table with %q: %v", tableSql, err)
		}
	}
	if _, err := db.Exec(fmt.Sprintf(`REPLACE INTO meta VALUES ('version', '%d')`, SchemaVersion())); err != nil {
		return nil, fmt.Errorf("error setting schema version: %v", err)
	}

	kv := &keyValue{
		db: db,
		KeyValue: &sqlkv.KeyValue{
			DB: db,
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
		if os.Getenv("CAMLI_DEV_CAMLI_ROOT") != "" {
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

func (kv *keyValue) ping() error {
	// TODO(bradfitz): something more efficient here?
	_, err := kv.SchemaVersion()
	return err
}

func (kv *keyValue) SchemaVersion() (version int, err error) {
	err = kv.db.QueryRow("SELECT value FROM meta WHERE metakey='version'").Scan(&version)
	return
}

const fixSchema20to21 = `Character set in tables changed to binary, you can fix your tables with:
ALTER TABLE rows CONVERT TO CHARACTER SET binary;
ALTER TABLE meta CONVERT TO CHARACTER SET binary;
UPDATE meta SET value=21 WHERE metakey='version' AND value=20;
`
