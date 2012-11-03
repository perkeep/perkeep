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
	"log"
	"os"
	"strings"

	"camlistore.org/pkg/index/mysql"
	"camlistore.org/pkg/index/postgres"

	_ "camlistore.org/third_party/github.com/bmizerany/pq"
	_ "camlistore.org/third_party/github.com/ziutek/mymysql/godrv"
)

var (
	flagUser     = flag.String("user", "root", "MySQL admin user")
	flagPassword = flag.String("password", "(prompt)", "MySQL admin password")
	flagHost     = flag.String("host", "localhost", "MySQ host[:port]")
	flagDatabase = flag.String("database", "", "MySQL camlistore to wipe/create database")

	flagType   = flag.String("type", "mysql", "Which RDMS to use; possible values: mysql, postgres")
	flagWipe   = flag.Bool("wipe", false, "Wipe the database and re-create it?")
	flagIgnore = flag.Bool("ignoreexists", false, "Treat existence of the database as okay and exit.")
)

func main() {
	flag.Parse()
	if *flagDatabase == "" {
		exitf("--database flag required")
	}

	if *flagType != "mysql" && *flagType != "postgres" {
		exitf("--type flag: wrong value")
	}
	var rootdb *sql.DB
	var err error
	switch *flagType {
	case "postgres":
		conninfo := fmt.Sprintf("user=%s dbname=%s host=%s password=%s sslmode=require", *flagUser, "postgres", *flagHost, *flagPassword)
		rootdb, err = sql.Open("postgres", conninfo)
	case "mysql":
		rootdb, err = sql.Open("mymysql", "mysql/"+*flagUser+"/"+*flagPassword)
	}
	if err != nil {
		exitf("Error connecting to the %s database: %v", *flagType, err)
	}

	dbname := *flagDatabase
	exists := dbExists(rootdb, dbname)
	if exists {
		if *flagIgnore {
			return
		}
		if !*flagWipe {
			exitf("Databases %q already exists, but --wipe not given. Stopping.", dbname)
		}
		do(rootdb, "DROP DATABASE "+dbname)
	}
	do(rootdb, "CREATE DATABASE "+dbname)

	var db *sql.DB
	switch *flagType {
	case "postgres":
		conninfo := fmt.Sprintf("user=%s dbname=%s host=%s password=%s sslmode=require", *flagUser, dbname, *flagHost, *flagPassword)
		db, err = sql.Open("postgres", conninfo)
	default:
		db, err = sql.Open("mymysql", dbname+"/"+*flagUser+"/"+*flagPassword)
	}
	if err != nil {
		exitf("Error connecting to the %s database: %v", *flagType, err)
	}

	switch *flagType {
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
}

func do(db *sql.DB, sql string) {
	_, err := db.Exec(sql)
	if err == nil {
		return
	}
	exitf("Error %v running SQL: %s", err, sql)
}

func doQuery(db *sql.DB, sql string) {
	r, err := db.Query(sql)
	if err == nil {
		r.Close()
		return
	}
	exitf("Error %v running SQL: %s", err, sql)
}

func dbExists(db *sql.DB, dbname string) bool {
	query := "SHOW DATABASES"
	switch *flagType {
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
	log.Fatalf("SQL error: %v", err)
}

func exitf(format string, args ...interface{}) {
	if !strings.HasSuffix(format, "\n") {
		format = format + "\n"
	}
	fmt.Fprintf(os.Stderr, format, args...)
	os.Exit(1)
}
