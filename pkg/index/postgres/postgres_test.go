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

package postgres_test

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"

	"camlistore.org/pkg/index"
	"camlistore.org/pkg/index/indextest"
	"camlistore.org/pkg/index/postgres"
	"camlistore.org/pkg/test/testdep"

	_ "camlistore.org/third_party/github.com/lib/pq"
)

var (
	once        sync.Once
	dbAvailable bool
	rootdb      *sql.DB
	testdb      *sql.DB
)

func checkDB() {
	var err error
	if rootdb, err = sql.Open("postgres", "user=postgres password=postgres host=localhost dbname=postgres"); err == nil {
		var n int
		err := rootdb.QueryRow("SELECT COUNT(*) FROM user").Scan(&n)
		if err == nil {
			dbAvailable = true
		}
	}
}

// TODO(mpl): figure out why we run into that problem of sessions still open
// and then remove that hack.
func closeAllSessions(dbname string) {
	query := `
SELECT
    pg_terminate_backend(pg_stat_activity.pid)
FROM
    pg_stat_activity
WHERE
    pg_stat_activity.pid <> pg_backend_pid()
    AND datname = '` + dbname + `'`
	doQuery(rootdb, query)
}

func makeIndex() *index.Index {
	dbname := "camlitest_" + os.Getenv("USER")
	closeAllSessions(dbname)
	do(rootdb, "DROP DATABASE IF EXISTS "+dbname)
	do(rootdb, "CREATE DATABASE "+dbname)
	var err error

	testdb, err = sql.Open("postgres", "user=postgres password=postgres host=localhost sslmode=require dbname="+dbname)
	if err != nil {
		panic("opening test database: " + err.Error())
	}
	for _, tableSql := range postgres.SQLCreateTables() {
		do(testdb, tableSql)
	}
	for _, statement := range postgres.SQLDefineReplace() {
		do(testdb, statement)
	}

	doQuery(testdb, fmt.Sprintf(`SELECT replaceintometa('version', '%d')`, postgres.SchemaVersion()))
	s, err := postgres.NewStorage("localhost", "postgres", "postgres", dbname, "require")
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

func doQuery(db *sql.DB, sql string) {
	r, err := db.Query(sql)
	if err == nil {
		r.Close()
		return
	}
	panic(fmt.Sprintf("Error %v running SQL: %s", err, sql))
}

type postgresTester struct{}

func (postgresTester) test(t *testing.T, tfn func(*testing.T, func() *index.Index)) {
	once.Do(checkDB)
	if !dbAvailable {
		err := errors.New("Not running; start a postgres daemon on the standard port (5432) with password 'postgres' for postgres user")
		testdep.CheckEnv(t)
		t.Fatalf("PostGreSQL not available locally for testing: %v", err)
	}
	tfn(t, makeIndex)
}

func TestIndex_Postgres(t *testing.T) {
	if testing.Short() {
		t.Logf("skipping test in short mode")
		return
	}
	postgresTester{}.test(t, indextest.Index)
}

func TestPathsOfSignerTarget_Postgres(t *testing.T) {
	if testing.Short() {
		t.Logf("skipping test in short mode")
		return
	}
	postgresTester{}.test(t, indextest.PathsOfSignerTarget)
}

func TestFiles_Postgres(t *testing.T) {
	if testing.Short() {
		t.Logf("skipping test in short mode")
		return
	}
	postgresTester{}.test(t, indextest.Files)
}

func TestEdgesTo_Postgres(t *testing.T) {
	if testing.Short() {
		t.Logf("skipping test in short mode")
		return
	}
	postgresTester{}.test(t, indextest.EdgesTo)
}
