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

package main

import (
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"camlistore.org/pkg/cmdmain"
	"camlistore.org/pkg/sorted/mongo"
	"camlistore.org/pkg/sorted/mysql"
	"camlistore.org/pkg/sorted/postgres"
	"camlistore.org/pkg/sorted/sqlite"

	_ "camlistore.org/third_party/github.com/go-sql-driver/mysql"
	_ "camlistore.org/third_party/github.com/lib/pq"
	"camlistore.org/third_party/labix.org/v2/mgo"
)

type dbinitCmd struct {
	user     string
	password string
	host     string
	dbName   string
	dbType   string
	sslMode  string // Postgres SSL mode configuration

	wipe bool
	keep bool
	wal  bool // Write-Ahead Logging for SQLite
}

func init() {
	cmdmain.RegisterCommand("dbinit", func(flags *flag.FlagSet) cmdmain.CommandRunner {
		cmd := new(dbinitCmd)
		flags.StringVar(&cmd.user, "user", "root", "Admin user.")
		flags.StringVar(&cmd.password, "password", "", "Admin password.")
		flags.StringVar(&cmd.host, "host", "localhost", "host[:port]")
		flags.StringVar(&cmd.dbName, "dbname", "", "Database to wipe or create. For sqlite, this is the db filename.")
		flags.StringVar(&cmd.dbType, "dbtype", "mysql", "Which RDMS to use; possible values: mysql, postgres, sqlite, mongo.")
		flags.StringVar(&cmd.sslMode, "sslmode", "require", "Configure SSL mode for postgres. Possible values: require, verify-full, disable.")

		flags.BoolVar(&cmd.wipe, "wipe", false, "Wipe the database and re-create it?")
		flags.BoolVar(&cmd.keep, "ignoreexists", false, "Do nothing if database already exists.")
		// Defaults to true, because it fixes http://camlistore.org/issues/114
		flags.BoolVar(&cmd.wal, "wal", true, "Enable Write-Ahead Logging with SQLite, for better concurrency. Requires SQLite >= 3.7.0.")

		return cmd
	})
}

func (c *dbinitCmd) Describe() string {
	return "Set up the database for the indexer."
}

func (c *dbinitCmd) Usage() {
	fmt.Fprintf(os.Stderr, "Usage: camtool [globalopts] dbinit [dbinitopts] \n")
}

func (c *dbinitCmd) Examples() []string {
	return []string{
		"-user root -password root -host localhost -dbname camliprod -wipe",
	}
}

func (c *dbinitCmd) RunCommand(args []string) error {
	if c.dbName == "" {
		return cmdmain.UsageError("--dbname flag required")
	}

	if c.dbType != "mysql" && c.dbType != "postgres" && c.dbType != "mongo" {
		if c.dbType == "sqlite" {
			if !WithSQLite {
				return ErrNoSQLite
			}
			c.wal = c.wal && sqlite.IsWALCapable()
			if !c.wal {
				fmt.Print("WARNING: An SQLite indexer without Write Ahead Logging will most likely fail. See http://camlistore.org/issues/114\n")
			}
		} else {
			return cmdmain.UsageError(fmt.Sprintf("--dbtype flag: got %v, want %v", c.dbType, `"mysql" or "postgres" or "sqlite", or "mongo"`))
		}
	}

	var rootdb *sql.DB
	var err error
	switch c.dbType {
	case "postgres":
		conninfo := fmt.Sprintf("user=%s dbname=%s host=%s password=%s sslmode=%s", c.user, "postgres", c.host, c.password, c.sslMode)
		rootdb, err = sql.Open("postgres", conninfo)
	case "mysql":
		rootdb, err = sql.Open("mysql", c.user+":"+c.password+"@/mysql")
	}
	if err != nil {
		exitf("Error connecting to the root %s database: %v", c.dbType, err)
	}

	dbname := c.dbName
	exists := c.dbExists(rootdb)
	if exists {
		if c.keep {
			return nil
		}
		if !c.wipe {
			return cmdmain.UsageError(fmt.Sprintf("Database %q already exists, but --wipe not given. Stopping.", dbname))
		}
		if c.dbType == "mongo" {
			return c.wipeMongo()
		}
		if c.dbType != "sqlite" {
			do(rootdb, "DROP DATABASE "+dbname)
		}
	}
	switch c.dbType {
	case "sqlite":
		_, err := os.Create(dbname)
		if err != nil {
			exitf("Error creating file %v for sqlite db: %v", dbname, err)
		}
	case "mongo":
		return nil
	case "postgres":
		// because we want string comparison to work as on MySQL and SQLite.
		// in particular we want: 'foo|bar' < 'foo}' (which is not the case with an utf8 collation apparently).
		do(rootdb, "CREATE DATABASE "+dbname+" LC_COLLATE = 'C' TEMPLATE = template0")
	default:
		do(rootdb, "CREATE DATABASE "+dbname)
	}

	var db *sql.DB
	switch c.dbType {
	case "postgres":
		conninfo := fmt.Sprintf("user=%s dbname=%s host=%s password=%s sslmode=%s", c.user, dbname, c.host, c.password, c.sslMode)
		db, err = sql.Open("postgres", conninfo)
	case "sqlite":
		db, err = sql.Open("sqlite3", dbname)
	default:
		db, err = sql.Open("mysql", c.user+":"+c.password+"@/"+dbname)
	}
	if err != nil {
		return fmt.Errorf("Connecting to the %s %s database: %v", dbname, c.dbType, err)
	}

	switch c.dbType {
	case "postgres":
		for _, tableSql := range postgres.SQLCreateTables() {
			do(db, tableSql)
		}
		for _, statement := range postgres.SQLDefineReplace() {
			do(db, statement)
		}
		doQuery(db, fmt.Sprintf(`SELECT replaceintometa('version', '%d')`, postgres.SchemaVersion()))
	case "mysql":
		if err := mysql.CreateDB(db, dbname); err != nil {
			exitf("%v", err)
		}
		for _, tableSQL := range mysql.SQLCreateTables() {
			do(db, tableSQL)
		}
		do(db, fmt.Sprintf(`REPLACE INTO meta VALUES ('version', '%d')`, mysql.SchemaVersion()))
	case "sqlite":
		for _, tableSql := range sqlite.SQLCreateTables() {
			do(db, tableSql)
		}
		if c.wal {
			do(db, sqlite.EnableWAL())
		}
		do(db, fmt.Sprintf(`REPLACE INTO meta VALUES ('version', '%d')`, sqlite.SchemaVersion()))
	}
	return nil
}

func do(db *sql.DB, sql string) {
	_, err := db.Exec(sql)
	if err != nil {
		exitf("Error %q running SQL: %q", err, sql)
	}
}

func doQuery(db *sql.DB, sql string) {
	r, err := db.Query(sql)
	if err == nil {
		r.Close()
		return
	}
	exitf("Error %q running SQL: %q", err, sql)
}

func (c *dbinitCmd) dbExists(db *sql.DB) bool {
	query := "SHOW DATABASES"
	switch c.dbType {
	case "postgres":
		query = "SELECT datname FROM pg_database"
	case "mysql":
		query = "SHOW DATABASES"
	case "sqlite":
		// There is no point in using sql.Open because it apparently does
		// not return an error when the file does not exist.
		fi, err := os.Stat(c.dbName)
		return err == nil && fi.Size() > 0
	case "mongo":
		session, err := c.mongoSession()
		if err != nil {
			exitf("%v", err)
		}
		defer session.Close()
		n, err := session.DB(c.dbName).C(mongo.CollectionName).Find(nil).Limit(1).Count()
		if err != nil {
			exitf("%v", err)
		}
		return n != 0
	}
	rows, err := db.Query(query)
	check(err)
	defer rows.Close()
	for rows.Next() {
		var db string
		check(rows.Scan(&db))
		if db == c.dbName {
			return true
		}
	}
	return false
}

func check(err error) {
	if err == nil {
		return
	}
	exitf("SQL error: %v", err)
}

func exitf(format string, args ...interface{}) {
	if !strings.HasSuffix(format, "\n") {
		format = format + "\n"
	}
	cmdmain.Errorf(format, args...)
	cmdmain.Exit(1)
}

var WithSQLite = false

var ErrNoSQLite = errors.New("the command was not built with SQLite support. See https://code.google.com/p/camlistore/wiki/SQLite" + compileHint())

func compileHint() string {
	if _, err := os.Stat("/etc/apt"); err == nil {
		return " (Required: apt-get install libsqlite3-dev)"
	}
	return ""
}

// mongoSession returns an *mgo.Session or nil if c.dbtype is
// not "mongo" or if there was an error.
func (c *dbinitCmd) mongoSession() (*mgo.Session, error) {
	if c.dbType != "mongo" {
		return nil, nil
	}
	url := ""
	if c.user == "" || c.password == "" {
		url = c.host
	} else {
		url = c.user + ":" + c.password + "@" + c.host + "/" + c.dbName
	}
	return mgo.Dial(url)
}

// wipeMongo erases all documents from the mongo collection
// if c.dbType is "mongo".
func (c *dbinitCmd) wipeMongo() error {
	if c.dbType != "mongo" {
		return nil
	}
	session, err := c.mongoSession()
	if err != nil {
		return err
	}
	defer session.Close()
	if _, err := session.DB(c.dbName).C(mongo.CollectionName).RemoveAll(nil); err != nil {
		return err
	}
	return nil
}
