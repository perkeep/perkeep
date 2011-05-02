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
	"fmt"
	"flag"
	"os"
	"strings"

	mysql "camli/third_party/github.com/Philio/GoMySQL"
)

var flagUser = flag.String("user", "root", "MySQL admin user")
var flagPassword = flag.String("password", "(prompt)", "MySQL admin password")
var flagHost = flag.String("host", "localhost", "MySQ host[:port]")
var flagDatabase = flag.String("database", "", "MySQL camlistore to wipe/create database")

var flagWipe = flag.Bool("wipe", false, "Wipe the database and re-create it?")
var flagIgnore = flag.Bool("ignoreexists", false, "Treat existence of the database as okay and exit.")

func main() {
	flag.Parse()
	if *flagDatabase == "" {
		exitf("--database flag required")
	}

	db, err := mysql.DialTCP(*flagHost, *flagUser, *flagPassword, "")
	if err != nil {
		exitf("Error connecting to database: %v", err)
	}

	dbname := *flagDatabase
	exists := dbExists(db, dbname)
	if exists {
		if *flagIgnore {
			return
		}
		if !*flagWipe {
			exitf("Databases %q already exists, but --wipe not given. Stopping.", dbname)
		}
		do(db, "DROP DATABASE "+dbname)
	}
	do(db, "CREATE DATABASE "+dbname)
	do(db, "USE "+dbname)
	do(db, `CREATE TABLE blobs (
blobref VARCHAR(128) NOT NULL PRIMARY KEY,
size INTEGER NOT NULL,
type VARCHAR(100))`)
	do(db, `CREATE TABLE claims (
blobref VARCHAR(128) NOT NULL PRIMARY KEY,
signer VARCHAR(128) NOT NULL,
date VARCHAR(40) NOT NULL, 
INDEX (signer, date),
unverified CHAR(1) NULL,
claim VARCHAR(50) NOT NULL,
permanode VARCHAR(128) NOT NULL,
INDEX (permanode, signer, date),
attr VARCHAR(128) NULL,
value VARCHAR(128) NULL)`)
	do(db, `CREATE TABLE permanodes (
blobref VARCHAR(128) NOT NULL PRIMARY KEY,
unverified CHAR(1) NULL,
signer VARCHAR(128) NOT NULL DEFAULT '',
lastmod VARCHAR(40) NOT NULL DEFAULT '',
INDEX (signer, lastmod))`)
}

func do(db *mysql.Client, sql string) {
	err := db.Query(sql)
	if err == nil {
		return
	}
	exitf("Error %v running SQL: %s", err, sql)
}

func dbExists(db *mysql.Client, dbname string) bool {
	check(db.Query("SHOW DATABASES"))
	result, err := db.UseResult()
	check(err)
	defer result.Free()
	for {
		row := result.FetchRow()
		if row == nil {
			break
		}
		if row[0].(string) == dbname {
			return true
		}
	}
	return false
}

func check(err os.Error) {
	if err == nil {
		return
	}
	panic(err)
}

func exitf(format string, args ...interface{}) {
	if !strings.HasSuffix(format, "\n") {
		format = format + "\n"
	}
	fmt.Fprintf(os.Stderr, format, args...)
	os.Exit(1)
}
