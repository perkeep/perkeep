/*
Copyright 2012 Google Inc.

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

package mysql_test

import (
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"testing"
	"os"

	"camlistore.org/pkg/index"
	"camlistore.org/pkg/index/indextest"
	"camlistore.org/pkg/index/mysql"
	"camlistore.org/pkg/test/testdep"

	_ "camlistore.org/third_party/github.com/ziutek/mymysql/godrv"
)

var (
	once        sync.Once
	dbAvailable bool
	rootdb *sql.DB
)

func checkDB() {
	var err error
	if rootdb, err = sql.Open("mymysql", "mysql/root/root"); err == nil {
		var n int
		err := rootdb.QueryRow("SELECT COUNT(*) FROM user").Scan(&n)
		if err == nil {
			dbAvailable = true
		}
	}
}

func makeIndex() *index.Index {
	dbname := "camlitest_" + os.Getenv("USER")
	do(rootdb, "DROP DATABASE IF EXISTS " + dbname)
	do(rootdb, "CREATE DATABASE " + dbname)

	db, err := sql.Open("mymysql", dbname + "/root/root");
	if err != nil {
		panic("opening test database: " + err.Error())
	}
	for _, tableSql := range mysql.SQLCreateTables() {
		do(db, tableSql)
	}

	do(db, fmt.Sprintf(`REPLACE INTO meta VALUES ('version', '%d')`, mysql.SchemaVersion()))
	s, err := mysql.NewStorage("localhost", "root", "root", dbname)
	if err != nil {
		panic(err)
	}
	return index.New(s)
}

func do(db *sql.DB, sql string) {
	_, err := db.Exec(sql)
	if err == nil {
		return
	}
	panic(fmt.Sprintf("Error %v running SQL: %s", err, sql))
}

type mysqlTester struct{}

func (mysqlTester) test(t *testing.T, tfn func(*testing.T, func() *index.Index)) {
	once.Do(checkDB)
	if !dbAvailable {
		// TODO(bradfitz): accept somehow other passwords than
		// 'root', and/or try localhost unix socket
		// connections rather than using TCP localhost?
		err := errors.New("Not running; start a MySQL daemon on the standard port (3306) with root password 'root'")
		testdep.CheckEnv(t)
		t.Fatalf("MySQL not available locally for testing: %v", err)
	}
	tfn(t, makeIndex)
}

func TestIndex_MySQL(t *testing.T) {
	mysqlTester{}.test(t, indextest.Index)
}

func TestPathsOfSignerTarget_MySQL(t *testing.T) {
	mysqlTester{}.test(t, indextest.PathsOfSignerTarget)
}

func TestFiles_MysQL(t *testing.T) {
	mysqlTester{}.test(t, indextest.Files)
}
