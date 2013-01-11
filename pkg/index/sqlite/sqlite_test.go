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

package sqlite_test

import (
	"database/sql"
	"fmt"
	"io/ioutil"
	"os"
	"sync"
	"testing"

	"camlistore.org/pkg/index"
	"camlistore.org/pkg/index/indextest"
	"camlistore.org/pkg/index/sqlite"

	_ "camlistore.org/third_party/github.com/mattn/go-sqlite3"
)

var (
	once        sync.Once
	dbAvailable bool
	rootdb      *sql.DB
)

func do(db *sql.DB, sql string) {
	_, err := db.Exec(sql)
	if err == nil {
		return
	}
	panic(fmt.Sprintf("Error %v running SQL: %s", err, sql))
}

type sqliteTester struct{}

func (sqliteTester) test(t *testing.T, tfn func(*testing.T, func() *index.Index)) {
	f, err := ioutil.TempFile("", "sqlite-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())
	makeIndex := func() *index.Index {
		db, err := sql.Open("sqlite3", f.Name())
		if err != nil {
			t.Fatalf("opening test database: %v", err)
			return nil
		}
		for _, tableSql := range sqlite.SQLCreateTables() {
			do(db, tableSql)
		}
		do(db, fmt.Sprintf(`REPLACE INTO meta VALUES ('version', '%d')`, sqlite.SchemaVersion()))
		s, err := sqlite.NewStorage(f.Name())
		if err != nil {
			panic(err)
		}
		return index.New(s)
	}
	tfn(t, makeIndex)
}

func TestIndex_SQLite(t *testing.T) {
	sqliteTester{}.test(t, indextest.Index)
}

func TestPathsOfSignerTarget_SQLite(t *testing.T) {
	sqliteTester{}.test(t, indextest.PathsOfSignerTarget)
}

func TestFiles_SQLite(t *testing.T) {
	sqliteTester{}.test(t, indextest.Files)
}

func TestEdgesTo_SQLite(t *testing.T) {
	sqliteTester{}.test(t, indextest.EdgesTo)
}
