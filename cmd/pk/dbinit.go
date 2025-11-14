/*
Copyright 2011 The Perkeep Authors

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
	"bytes"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"strings"

	"perkeep.org/pkg/cmdmain"
	"perkeep.org/pkg/sorted/mongo"
	"perkeep.org/pkg/sorted/mysql"
	"perkeep.org/pkg/sorted/postgres"
	"perkeep.org/pkg/sorted/sqlite"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/lib/pq"
	"gopkg.in/mgo.v2"
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
}

func init() {
	cmdmain.RegisterMode("dbinit", func(flags *flag.FlagSet) cmdmain.CommandRunner {
		cmd := new(dbinitCmd)
		flags.StringVar(&cmd.user, "user", "root", "Admin user.")
		flags.StringVar(&cmd.password, "password", "", "Admin password.")
		flags.StringVar(&cmd.host, "host", "localhost", "host[:port]")
		flags.StringVar(&cmd.dbName, "dbname", "", "Database to wipe or create. For sqlite, this is the db filename.")
		flags.StringVar(&cmd.dbType, "dbtype", "mysql", "Which RDMS to use; possible values: mysql, postgres, sqlite, mongo.")
		flags.StringVar(&cmd.sslMode, "sslmode", "require", "Configure SSL mode for postgres. Possible values: require, verify-full, disable.")

		flags.BoolVar(&cmd.wipe, "wipe", false, "Wipe the database and re-create it?")
		flags.BoolVar(&cmd.keep, "ignoreexists", false, "Do nothing if database already exists.")
		return cmd
	})
}

func (c *dbinitCmd) Demote() bool { return true }

func (c *dbinitCmd) Describe() string {
	return "Set up the database for the indexer."
}

func (c *dbinitCmd) Usage() {
	fmt.Fprintf(os.Stderr, "Usage: pk [globalopts] dbinit [dbinitopts] \n")
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
		// need to use an empty dbname to query tables
		rootdb, err = sql.Open("mysql", c.mysqlDSN(""))
	case "sqlite":
		rootdb, err = sql.Open("sqlite", c.dbName)
	}
	if err != nil {
		exitf("Error connecting to the root %s database: %v", c.dbType, err)
	}
	defer rootdb.Close()

	// Validate the DSN to avoid confusion here
	err = rootdb.Ping()
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
		db, err = sql.Open("sqlite", dbname)
	default:
		db, err = sql.Open("mysql", c.mysqlDSN(dbname))
	}
	if err != nil {
		return fmt.Errorf("Connecting to the %s %s database: %v", dbname, c.dbType, err)
	}
	defer db.Close()

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
			exitf("error in CreateDB(%s): %v", dbname, err)
		}
		for _, tableSQL := range mysql.SQLCreateTables() {
			do(db, tableSQL)
		}
		do(db, fmt.Sprintf(`REPLACE INTO meta VALUES ('version', '%d')`, mysql.SchemaVersion()))
	case "sqlite":
		if err := sqlite.InitDB(dbname); err != nil {
			exitf("error calling InitDB(%s): %v", dbname, err)
		}
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
	check(err, query)
	defer rows.Close()
	for rows.Next() {
		var db string
		check(rows.Scan(&db), query)
		if db == c.dbName {
			return true
		}
	}
	return false
}

func check(err error, query string) {
	if err == nil {
		return
	}
	exitf("SQL error for query %q: %v", query, err)
}

func exitf(format string, args ...any) {
	if !strings.HasSuffix(format, "\n") {
		format = format + "\n"
	}
	cmdmain.Errorf(format, args...)
	cmdmain.Exit(1)
}

var WithSQLite = false

var ErrNoSQLite = errors.New("the command was not built with SQLite support. See https://code.google.com/p/camlistore/wiki/SQLite" + compileHint())

func compileHint() string {
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

func (c *dbinitCmd) mysqlDSN(dbname string) string {
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "%s:%s@", c.user, c.password)
	if c.host != "localhost" {
		host := c.host
		if _, _, err := net.SplitHostPort(host); err != nil {
			host = net.JoinHostPort(host, "3306")
		}
		fmt.Fprintf(&buf, "tcp(%s)", host)
	}
	fmt.Fprintf(&buf, "/%s", dbname)
	return buf.String()
}
