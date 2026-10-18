/*
Copyright 2026 The Perkeep Authors

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
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"perkeep.org/pkg/sorted/mysql"
	"perkeep.org/pkg/sorted/postgres"
)

func TestAdminDBInitMySQL(t *testing.T) {
	testAdminDBInitDBMS(t, "mysql", "PERKEEP_TEST_MYSQL_HOST", "root", "root", mysql.SchemaVersion())
}

func TestAdminDBInitPostgres(t *testing.T) {
	testAdminDBInitDBMS(t, "postgres", "PERKEEP_TEST_POSTGRES_HOST", "postgres", "postgres", postgres.SchemaVersion())
}

// Use only explicitly configured test services, never the server's config or a
// default database. Each invocation owns one unique database and drops only it.
func testAdminDBInitDBMS(t *testing.T, dbtype, hostEnv, user, password string, schemaVersion int) {
	t.Helper()
	host := os.Getenv(hostEnv)
	if host == "" {
		t.Skipf("%s not set; requires an isolated %s test service", hostEnv, dbtype)
	}
	name := fmt.Sprintf("pkadmin_%d_%d", os.Getpid(), time.Now().UnixNano())
	cmd := dbinitCmd{dbType: dbtype, host: host, user: user, password: password, dbName: name, sslMode: "disable"}
	open := func(database string) *sql.DB {
		t.Helper()
		driver, dsn := "mysql", cmd.mysqlDSN(database)
		if dbtype == "postgres" {
			driver = "pgx"
			dsn = fmt.Sprintf("user=%s password=%s host=%s dbname=%s sslmode=disable", user, password, host, database)
		}
		db, err := sql.Open(driver, dsn)
		if err != nil {
			t.Fatal(err)
		}
		return db
	}
	rootName := ""
	if dbtype == "postgres" {
		rootName = "postgres"
	}
	root := open(rootName)
	t.Cleanup(func() { root.Close() })
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	if err := root.PingContext(ctx); err != nil {
		t.Fatalf("test service %s at %s unreachable: %v", dbtype, host, err)
	}
	if exists, err := cmd.dbExists(root); err != nil || exists {
		t.Fatalf("temporary database %q must not already exist: exists %v, error %v", name, exists, err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if _, err := root.ExecContext(ctx, "DROP DATABASE IF EXISTS "+name); err != nil {
			t.Errorf("cleaning up own temporary database %q: %v", name, err)
		}
	})
	args := []string{"--dbtype=" + dbtype, "--host=" + host, "--user=" + user, "--password=" + password, "--dbname=" + name, "--sslmode=disable"}
	// On MySQL, successful creation is also a regression guard against sending
	// SQLCreateTables' literal /*DB*/ placeholders to the database server.
	if stderr := runDBInitTest(t, 0, args...); stderr != "" {
		t.Fatalf("create stderr: %s", stderr)
	}
	rowsTable := "rows"
	if dbtype == "mysql" {
		rowsTable = name + ".rows"
	}
	observe := func(wantRows int) {
		t.Helper()
		db := open(name)
		defer db.Close() // PostgreSQL must have no target connections at DROP.
		var version, count int
		if err := db.QueryRow("SELECT value FROM meta WHERE metakey='version'").Scan(&version); err != nil || version != schemaVersion {
			t.Fatalf("schema version = %d, %v; want %d", version, err, schemaVersion)
		}
		if err := db.QueryRow("SELECT count(*) FROM " + rowsTable).Scan(&count); err != nil || count != wantRows {
			t.Fatalf("rows count = %d, %v; want %d", count, err, wantRows)
		}
		if wantRows != 0 {
			var value string
			if err := db.QueryRow("SELECT v FROM " + rowsTable + " WHERE k='pkadmin-sentinel'").Scan(&value); err != nil || value != "untouched" {
				t.Fatalf("sentinel = %q, %v; want untouched", value, err)
			}
		}
	}
	observe(0)
	db := open(name)
	_, err := db.Exec("INSERT INTO " + rowsTable + " (k, v) VALUES ('pkadmin-sentinel', 'untouched')")
	closeErr := db.Close()
	if err != nil || closeErr != nil {
		t.Fatalf("inserting sentinel: %v; closing observer: %v", err, closeErr)
	}
	if stderr := runDBInitTest(t, 2, args...); !strings.Contains(stderr, "already exists, but --wipe not given") {
		t.Fatalf("existing refusal stderr: %s", stderr)
	}
	observe(1)
	if stderr := runDBInitTest(t, 0, append(append([]string{}, args...), "--ignoreexists", "--wipe")...); stderr != "" {
		t.Fatalf("ignoreexists stderr: %s", stderr)
	}
	observe(1)
	if stderr := runDBInitTest(t, 0, append(args, "--wipe")...); stderr != "" {
		t.Fatalf("wipe stderr: %s", stderr)
	}
	observe(0)
}
