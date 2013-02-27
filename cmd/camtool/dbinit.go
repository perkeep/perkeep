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
	"flag"
	"fmt"
	"os"
	"strings"

	"camlistore.org/pkg/cmdmain"
	"camlistore.org/pkg/index/mysql"
	"camlistore.org/pkg/index/postgres"

	_ "camlistore.org/third_party/github.com/bmizerany/pq"
	_ "camlistore.org/third_party/github.com/ziutek/mymysql/godrv"
)

type dbinitCmd struct {
	user     string
	password string
	host     string
	dbName   string
	dbType   string

	wipe bool
	keep bool
}

func init() {
	cmdmain.RegisterCommand("dbinit", func(flags *flag.FlagSet) cmdmain.CommandRunner {
		cmd := new(dbinitCmd)
		flags.StringVar(&cmd.user, "user", "root", "Admin user.")
		flags.StringVar(&cmd.password, "password", "", "Admin password.")
		flags.StringVar(&cmd.host, "host", "localhost", "host[:port]")
		flags.StringVar(&cmd.dbName, "dbname", "", "Database to wipe or create.")
		flags.StringVar(&cmd.dbType, "dbtype", "mysql", "Which RDMS to use; possible values: mysql, postgres.")

		flags.BoolVar(&cmd.wipe, "wipe", false, "Wipe the database and re-create it?")
		flags.BoolVar(&cmd.keep, "ignoreexists", false, "Do nothing if database already exists.")

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

	if c.dbType != "mysql" && c.dbType != "postgres" {
		return cmdmain.UsageError(fmt.Sprintf("--dbtype flag: got %v, want %v", c.dbType, `"mysql" or "postgres"`))
	}
	var rootdb *sql.DB
	var err error
	switch c.dbType {
	case "postgres":
		conninfo := fmt.Sprintf("user=%s dbname=%s host=%s password=%s sslmode=require", c.user, "postgres", c.host, c.password)
		rootdb, err = sql.Open("postgres", conninfo)
	case "mysql":
		rootdb, err = sql.Open("mymysql", "mysql/"+c.user+"/"+c.password)
	}
	if err != nil {
		exitf("Error connecting to the root %s database: %v", c.dbType, err)
	}

	dbname := c.dbName
	exists := dbExists(rootdb, c.dbType, dbname)
	if exists {
		if c.keep {
			return nil
		}
		if !c.wipe {
			return cmdmain.UsageError(fmt.Sprintf("Database %q already exists, but --wipe not given. Stopping.", dbname))
		}
		do(rootdb, "DROP DATABASE "+dbname)
	}
	do(rootdb, "CREATE DATABASE "+dbname)

	var db *sql.DB
	switch c.dbType {
	case "postgres":
		conninfo := fmt.Sprintf("user=%s dbname=%s host=%s password=%s sslmode=require", c.user, dbname, c.host, c.password)
		db, err = sql.Open("postgres", conninfo)
	default:
		db, err = sql.Open("mymysql", dbname+"/"+c.user+"/"+c.password)
	}
	if err != nil {
		return fmt.Errorf("Error connecting to the %s %s database: %v", dbname, c.dbType, err)
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
		for _, tableSql := range mysql.SQLCreateTables() {
			do(db, tableSql)
		}
		do(db, fmt.Sprintf(`REPLACE INTO meta VALUES ('version', '%d')`, mysql.SchemaVersion()))
	}
	return nil
}

func do(db *sql.DB, sql string) {
	_, err := db.Exec(sql)
	if err != nil {
		exitf("Error %v running SQL: %s", err, sql)
	}
}

func doQuery(db *sql.DB, sql string) {
	r, err := db.Query(sql)
	if err == nil {
		r.Close()
		return
	}
	exitf("Error %v running SQL: %s", err, sql)
}

func dbExists(db *sql.DB, dbtype, dbname string) bool {
	query := "SHOW DATABASES"
	switch dbtype {
	case "postgres":
		query = "SELECT datname FROM pg_database"
	case "mysql":
		query = "SHOW DATABASES"
	}
	rows, err := db.Query(query)
	check(err)
	defer rows.Close()
	for rows.Next() {
		var db string
		check(rows.Scan(&db))
		if db == dbname {
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
	cmdmain.Errorf(format, args)
	cmdmain.Exit(1)
}
