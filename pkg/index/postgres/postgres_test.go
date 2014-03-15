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
	"log"
	"strings"
	"sync"
	"testing"

	"camlistore.org/pkg/index"
	"camlistore.org/pkg/index/indextest"
	"camlistore.org/pkg/osutil"
	"camlistore.org/pkg/sorted"
	"camlistore.org/pkg/sorted/kvtest"
	"camlistore.org/pkg/sorted/postgres"
	"camlistore.org/pkg/test"

	_ "camlistore.org/third_party/github.com/lib/pq"
)

var (
	once        sync.Once
	dbAvailable bool
	rootdb      *sql.DB
)

func checkDB() {
	var err error
	rootdb, err = sql.Open("postgres", "user=postgres password=postgres host=localhost dbname=postgres")
	if err != nil {
		log.Printf("Could not open postgres rootdb: %v", err)
		return
	}
	var n int
	err = rootdb.QueryRow("SELECT COUNT(*) FROM user").Scan(&n)
	if err == nil {
		dbAvailable = true
	}
}

func skipOrFailIfNoPostgreSQL(t *testing.T) {
	once.Do(checkDB)
	if !dbAvailable {
		err := errors.New("Not running; start a postgres daemon on the standard port (5432) with password 'postgres' for postgres user")
		test.DependencyErrorOrSkip(t)
		t.Fatalf("PostGreSQL not available locally for testing: %v", err)
	}
}

func do(db *sql.DB, sql string) {
	_, err := db.Exec(sql)
	if err == nil {
		return
	}
	log.Fatalf("Error %v running SQL: %s", err, sql)
}

func doQuery(db *sql.DB, sql string) {
	r, err := db.Query(sql)
	if err == nil {
		r.Close()
		return
	}
	log.Fatalf("Error %v running SQL query: %s", err, sql)
}

// closeAllSessions connects to the "postgres" DB on cfg.Host, and closes all connections to cfg.Database.
func closeAllSessions(cfg postgres.Config) error {
	conninfo := fmt.Sprintf("user=%s dbname=postgres host=%s password=%s sslmode=%s",
		cfg.User, cfg.Host, cfg.Password, cfg.SSLMode)
	rootdb, err := sql.Open("postgres", conninfo)
	if err != nil {
		return fmt.Errorf("Could not open root db: %v", err)
	}
	defer rootdb.Close()
	query := `
SELECT
    pg_terminate_backend(pg_stat_activity.pid)
FROM
    pg_stat_activity
WHERE
	datname = '` + cfg.Database +
		`' AND pid <> pg_backend_pid()`

	// They changed procpid to pid in 9.2
	if version(rootdb) < "9.2" {
		query = strings.Replace(query, ".pid", ".procpid", 1)
		query = strings.Replace(query, "AND pid", "AND procpid", 1)
	}
	r, err := rootdb.Query(query)
	if err != nil {
		return fmt.Errorf("Error running SQL query\n %v\n: %s", query, err)
	}
	r.Close()
	return nil
}

func version(db *sql.DB) string {
	version := ""
	if err := db.QueryRow("SELECT version()").Scan(&version); err != nil {
		log.Fatalf("Could not get PostgreSQL version: %v", err)
	}
	fields := strings.Fields(version)
	if len(fields) < 2 {
		log.Fatalf("Could not get PostgreSQL version because bogus answer: %q", version)
	}
	return fields[1]
}

func newSorted(t *testing.T) (kv sorted.KeyValue, clean func()) {
	skipOrFailIfNoPostgreSQL(t)
	dbname := "camlitest_" + osutil.Username()
	if err := closeAllSessions(postgres.Config{
		User:     "postgres",
		Password: "postgres",
		SSLMode:  "require",
		Database: dbname,
		Host:     "localhost",
	}); err != nil {
		t.Fatalf("Could not close all old sessions to %q: %v", dbname, err)
	}
	do(rootdb, "DROP DATABASE IF EXISTS "+dbname)
	do(rootdb, "CREATE DATABASE "+dbname+" LC_COLLATE = 'C' TEMPLATE = template0")

	testdb, err := sql.Open("postgres", "user=postgres password=postgres host=localhost sslmode=require dbname="+dbname)
	if err != nil {
		t.Fatalf("opening test database: " + err.Error())
	}
	for _, tableSql := range postgres.SQLCreateTables() {
		do(testdb, tableSql)
	}
	for _, statement := range postgres.SQLDefineReplace() {
		do(testdb, statement)
	}
	doQuery(testdb, fmt.Sprintf(`SELECT replaceintometa('version', '%d')`, postgres.SchemaVersion()))

	kv, err = postgres.NewKeyValue(postgres.Config{
		Host:     "localhost",
		Database: dbname,
		User:     "postgres",
		Password: "postgres",
		SSLMode:  "require",
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

type postgresTester struct{}

func (postgresTester) test(t *testing.T, tfn func(*testing.T, func() *index.Index)) {
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
		return index.MustNew(t, s)
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

func TestDelete_Postgres(t *testing.T) {
	postgresTester{}.test(t, indextest.Delete)
}
