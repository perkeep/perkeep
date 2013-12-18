/*
Copyright 2012 The Camlistore Authors.

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
	"log"
	"sync"
	"testing"

	"camlistore.org/pkg/index"
	"camlistore.org/pkg/index/indextest"
	"camlistore.org/pkg/osutil"
	"camlistore.org/pkg/sorted"
	"camlistore.org/pkg/sorted/kvtest"
	"camlistore.org/pkg/sorted/mysql"
	"camlistore.org/pkg/test"

	_ "camlistore.org/third_party/github.com/ziutek/mymysql/godrv"
)

var (
	once        sync.Once
	dbAvailable bool
	rootdb      *sql.DB
)

func checkDB() {
	var err error
	rootdb, err = sql.Open("mymysql", "mysql/root/root")
	if err != nil {
		log.Printf("Could not open rootdb: %v", err)
		return
	}
	var n int
	err = rootdb.QueryRow("SELECT COUNT(*) FROM user").Scan(&n)
	if err == nil {
		dbAvailable = true
	}
}

func skipOrFailIfNoMySQL(t *testing.T) {
	once.Do(checkDB)
	if !dbAvailable {
		// TODO(bradfitz): accept somehow other passwords than
		// 'root', and/or try localhost unix socket
		// connections rather than using TCP localhost?
		err := errors.New("Not running; start a MySQL daemon on the standard port (3306) with root password 'root'")
		test.DependencyErrorOrSkip(t)
		t.Fatalf("MySQL not available locally for testing: %v", err)
	}
}

func do(db *sql.DB, sql string) {
	_, err := db.Exec(sql)
	if err == nil {
		return
	}
	log.Fatalf("Error %v running SQL: %s", err, sql)
}

func newSorted(t *testing.T) (kv sorted.KeyValue, clean func()) {
	skipOrFailIfNoMySQL(t)
	dbname := "camlitest_" + osutil.Username()
	do(rootdb, "DROP DATABASE IF EXISTS "+dbname)
	do(rootdb, "CREATE DATABASE "+dbname)

	db, err := sql.Open("mymysql", dbname+"/root/root")
	if err != nil {
		t.Fatalf("opening test database: " + err.Error())
	}
	for _, tableSql := range mysql.SQLCreateTables() {
		do(db, tableSql)
	}
	do(db, fmt.Sprintf(`REPLACE INTO meta VALUES ('version', '%d')`, mysql.SchemaVersion()))

	kv, err = mysql.NewKeyValue(mysql.Config{
		Database: dbname,
		User:     "root",
		Password: "root",
	})
	if err != nil {
		t.Fatal(err)
	}
	return kv, func() {
		kv.Close()
	}
}

func TestSortedKV(t *testing.T) {
	kv, clean := newSorted(t)
	defer clean()
	kvtest.TestSorted(t, kv)
}

type mysqlTester struct{}

func (mysqlTester) test(t *testing.T, tfn func(*testing.T, func() *index.Index)) {
	var mu sync.Mutex // guards cleanups
	var cleanups []func()
	defer func() {
		mu.Lock() // never unlocked
		for _, fn := range cleanups {
			fn()
		}
	}()
	makeIndex := func() *index.Index {
		s, cleanup := newSorted(t)
		mu.Lock()
		cleanups = append(cleanups, cleanup)
		mu.Unlock()
		return index.New(s)
	}
	tfn(t, makeIndex)
}

func TestIndex_MySQL(t *testing.T) {
	mysqlTester{}.test(t, indextest.Index)
}

func TestPathsOfSignerTarget_MySQL(t *testing.T) {
	mysqlTester{}.test(t, indextest.PathsOfSignerTarget)
}

func TestFiles_MySQL(t *testing.T) {
	mysqlTester{}.test(t, indextest.Files)
}

func TestEdgesTo_MySQL(t *testing.T) {
	mysqlTester{}.test(t, indextest.EdgesTo)
}

func TestDelete_MySQL(t *testing.T) {
	mysqlTester{}.test(t, indextest.Delete)
}
