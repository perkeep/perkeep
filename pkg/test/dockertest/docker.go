/*
Copyright 2014 The Perkeep Authors

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

/*
Package dockertest provides MySQL and PostgreSQL databases for tests, on
servers named by environment variables. The servers are typically Docker
containers, such as GitHub Actions service containers.
*/
package dockertest // import "perkeep.org/pkg/test/dockertest"

import (
	"crypto/rand"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"
)

const (
	MySQLUsername    = "root"
	MySQLPassword    = "root"
	PostgresUsername = "postgres"
	PostgresPassword = "postgres"
)

// DB is a database that a test can use.
type DB struct {
	// Host is the value for the "host" key of a sorted.KeyValue config.
	Host string
	// Database is the name of the database, unique to the test.
	Database string
}

// newDBName returns a new, unique database name.
func newDBName() string {
	return "camlitest_" + strings.ToLower(rand.Text())
}

// SetupMySQL returns a MySQL database for the test, accessible with
// MySQLUsername and MySQLPassword, on the server at host[:port] given by
// the PERKEEP_TEST_MYSQL_HOST environment variable. The database might not
// exist yet, and is dropped when the test ends.
//
// If PERKEEP_TEST_MYSQL_HOST is not set, the test is skipped. If the server
// is unreachable, the test fails.
func SetupMySQL(t *testing.T) DB {
	host := os.Getenv("PERKEEP_TEST_MYSQL_HOST")
	if host == "" {
		t.Skip("skipping MySQL test; PERKEEP_TEST_MYSQL_HOST not set")
	}
	if !strings.Contains(host, ":") {
		host += ":3306"
	}
	db := DB{Host: host, Database: newDBName()}
	rootdb, err := sql.Open("mysql", fmt.Sprintf("%s:%s@tcp(%s)/", MySQLUsername, MySQLPassword, host))
	if err != nil {
		t.Fatalf("opening MySQL at %s: %v", host, err)
	}
	if err := rootdb.Ping(); err != nil {
		rootdb.Close()
		t.Fatalf("MySQL server at %s (from PERKEEP_TEST_MYSQL_HOST) unreachable: %v", host, err)
	}
	t.Cleanup(func() {
		defer rootdb.Close()
		if _, err := rootdb.Exec("DROP DATABASE IF EXISTS " + db.Database); err != nil {
			t.Errorf("dropping MySQL database %s: %v", db.Database, err)
		}
	})
	return db
}

// SetupPostgreSQL returns a new PostgreSQL database for the test, using the
// C collation, accessible with PostgresUsername and PostgresPassword, on the
// server (on the default port) given by the PERKEEP_TEST_POSTGRES_HOST
// environment variable. The database is dropped when the test ends.
//
// If PERKEEP_TEST_POSTGRES_HOST is not set, the test is skipped. If the
// server is unreachable, the test fails.
func SetupPostgreSQL(t *testing.T) DB {
	host := os.Getenv("PERKEEP_TEST_POSTGRES_HOST")
	if host == "" {
		t.Skip("skipping PostgreSQL test; PERKEEP_TEST_POSTGRES_HOST not set")
	}
	db := DB{Host: host, Database: newDBName()}
	rootdb, err := sql.Open("postgres",
		fmt.Sprintf("user=%s password=%s host=%s dbname=postgres sslmode=disable", PostgresUsername, PostgresPassword, host))
	if err != nil {
		t.Fatalf("Could not open postgres rootdb: %v", err)
	}
	t.Cleanup(func() { rootdb.Close() })
	if _, err := rootdb.Exec("CREATE DATABASE " + db.Database + " LC_COLLATE = 'C' TEMPLATE = template0"); err != nil {
		t.Fatalf("creating database on PostgreSQL server at %s (from PERKEEP_TEST_POSTGRES_HOST): %v", host, err)
	}
	t.Cleanup(func() {
		if _, err := rootdb.Exec("DROP DATABASE IF EXISTS " + db.Database + " WITH (FORCE)"); err != nil {
			t.Errorf("dropping PostgreSQL database %s: %v", db.Database, err)
		}
	})
	return db
}
