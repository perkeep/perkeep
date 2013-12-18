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

	"camlistore.org/pkg/jsonconfig"
	"camlistore.org/pkg/sorted"
	"camlistore.org/pkg/sorted/sqlkv"

	_ "camlistore.org/third_party/github.com/ziutek/mymysql/godrv"
)

func init() {
	sorted.RegisterKeyValue("mysql", newKeyValueFromJSONConfig)
}

// Config holds the parameters used to connect to the MySQL db.
type Config struct {
	Host     string // Optional. Defaults to "localhost" in ConfigFromJSON.
	Database string // Required.
	User     string // Required.
	Password string // Optional.
}

// ConfigFromJSON populates Config from config, and validates
// config. It returns an error if config fails to validate.
func ConfigFromJSON(config jsonconfig.Obj) (Config, error) {
	conf := Config{
		Host:     config.OptionalString("host", "localhost"),
		User:     config.RequiredString("user"),
		Password: config.OptionalString("password", ""),
		Database: config.RequiredString("database"),
	}
	if err := config.Validate(); err != nil {
		return Config{}, err
	}
	return conf, nil
}

func newKeyValueFromJSONConfig(cfg jsonconfig.Obj) (sorted.KeyValue, error) {
	conf, err := ConfigFromJSON(cfg)
	if err != nil {
		return nil, err
	}
	return NewKeyValue(conf)
}

// NewKeyValue returns a sorted.KeyValue implementation of the described MySQL database.
func NewKeyValue(cfg Config) (sorted.KeyValue, error) {
	// TODO(bradfitz,mpl): host is ignored for now. I think we can connect to host with:
	// tcp:ADDR*DBNAME/USER/PASSWD (http://godoc.org/github.com/ziutek/mymysql/godrv#Driver.Open)
	// I suppose we'll have to do a lookup first.
	dsn := cfg.Database + "/" + cfg.User + "/" + cfg.Password
	db, err := sql.Open("mymysql", dsn)
	if err != nil {
		return nil, err
	}
	kv := &keyValue{
		db: db,
		KeyValue: &sqlkv.KeyValue{
			DB: db,
		},
		conf: cfg,
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

	conf Config
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

const fixSchema20to21 = `Character set in tables changed to binary, you can fix your tables with:
ALTER TABLE rows CONVERT TO CHARACTER SET binary;
ALTER TABLE meta CONVERT TO CHARACTER SET binary;
UPDATE meta SET value=21 WHERE metakey='version' AND value=20;
`
