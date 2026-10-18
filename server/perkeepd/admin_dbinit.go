/*
Copyright 2011 The Perkeep Authors

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
	"bytes"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"strings"

	"perkeep.org/pkg/sorted/mysql"
	"perkeep.org/pkg/sorted/postgres"
	"perkeep.org/pkg/sorted/sqlite"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"
)

func runAdminDBInit(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("perkeepd admin dbinit", flag.ContinueOnError)
	flags.SetOutput(stderr)
	cmd := newDBInitCmd(flags)
	flags.Usage = func() {
		fmt.Fprint(stderr, `Usage: perkeepd admin dbinit [options]

Set up the database for the indexer. Stop the server before using this command.
Requires --dbname; for SQLite this is the database filename.
--ignoreexists takes precedence over --wipe.

Options:
`)
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if err := cmd.RunCommand(flags.Args()); err != nil {
		fmt.Fprintf(stderr, "perkeepd admin dbinit: %v\n", err)
		var ue dbinitUsageError
		if errors.As(err, &ue) {
			flags.Usage()
			return 2
		}
		return 1
	}
	return 0
}

type dbinitUsageError string

func (e dbinitUsageError) Error() string { return string(e) }

type dbinitCmd struct {
	user     string
	password string
	host     string
	dbName   string
	dbType   string
	sslMode  string // Postgres SSL mode configuration

	wipe bool
	keep bool
}

func newDBInitCmd(flags *flag.FlagSet) *dbinitCmd {
	cmd := new(dbinitCmd)
	flags.StringVar(&cmd.user, "user", "root", "Admin user.")
	flags.StringVar(&cmd.password, "password", "", "Admin password.")
	flags.StringVar(&cmd.host, "host", "localhost", "host[:port]")
	flags.StringVar(&cmd.dbName, "dbname", "", "Database to wipe or create. For sqlite, this is the db filename.")
	flags.StringVar(&cmd.dbType, "dbtype", "mysql", "Which RDMS to use; possible values: mysql, postgres, sqlite.")
	flags.StringVar(&cmd.sslMode, "sslmode", "require", "Configure SSL mode for postgres. Possible values: require, verify-full, disable.")

	flags.BoolVar(&cmd.wipe, "wipe", false, "Wipe the database and re-create it?")
	flags.BoolVar(&cmd.keep, "ignoreexists", false, "Do nothing if database already exists.")
	return cmd
}

func (c *dbinitCmd) RunCommand(args []string) error {
	if len(args) != 0 {
		return dbinitUsageError("dbinit does not accept positional arguments")
	}
	if c.dbName == "" {
		return dbinitUsageError("--dbname flag required")
	}

	if c.dbType != "mysql" && c.dbType != "postgres" && c.dbType != "sqlite" {
		return dbinitUsageError(fmt.Sprintf("--dbtype flag: got %v, want %v", c.dbType, `"mysql" or "postgres" or "sqlite"`))
	}

	var rootdb *sql.DB
	var err error
	switch c.dbType {
	case "postgres":
		conninfo := fmt.Sprintf("user=%s dbname=%s host=%s password=%s sslmode=%s", c.user, "postgres", c.host, c.password, c.sslMode)
		rootdb, err = sql.Open("pgx", conninfo)
	case "mysql":
		// need to use an empty dbname to query tables
		rootdb, err = sql.Open("mysql", c.mysqlDSN(""))
	case "sqlite":
		rootdb, err = sql.Open("sqlite", c.dbName)
	}
	if err != nil {
		return fmt.Errorf("Error connecting to the root %s database: %w", c.dbType, err)
	}
	defer rootdb.Close()

	// Validate the DSN to avoid confusion here
	err = rootdb.Ping()
	if err != nil {
		return fmt.Errorf("Error connecting to the root %s database: %w", c.dbType, err)
	}

	dbname := c.dbName
	exists, err := c.dbExists(rootdb)
	if err != nil {
		return err
	}
	if exists {
		if c.keep {
			return nil
		}
		if !c.wipe {
			return dbinitUsageError(fmt.Sprintf("Database %q already exists, but --wipe not given. Stopping.", dbname))
		}
		if c.dbType != "sqlite" {
			if err := dbinitExec(rootdb, "DROP DATABASE "+dbname); err != nil {
				return err
			}
		}
	}
	switch c.dbType {
	case "sqlite":
		// The existence check is done. Keeping this connection open across
		// truncation can leave SQLite's WAL locked during reinitialization.
		if err := rootdb.Close(); err != nil {
			return fmt.Errorf("Error closing the root sqlite database: %w", err)
		}
		f, err := os.Create(dbname)
		if err != nil {
			return fmt.Errorf("Error creating file %v for sqlite db: %w", dbname, err)
		}
		if err := f.Close(); err != nil {
			return fmt.Errorf("Error closing file %v for sqlite db: %w", dbname, err)
		}
	case "postgres":
		// because we want string comparison to work as on MySQL and SQLite.
		// in particular we want: 'foo|bar' < 'foo}' (which is not the case with an utf8 collation apparently).
		if err := dbinitExec(rootdb, "CREATE DATABASE "+dbname+" LC_COLLATE = 'C' TEMPLATE = template0"); err != nil {
			return err
		}
	default:
		if err := dbinitExec(rootdb, "CREATE DATABASE "+dbname); err != nil {
			return err
		}
	}

	var db *sql.DB
	switch c.dbType {
	case "postgres":
		conninfo := fmt.Sprintf("user=%s dbname=%s host=%s password=%s sslmode=%s", c.user, dbname, c.host, c.password, c.sslMode)
		db, err = sql.Open("pgx", conninfo)
	case "sqlite":
		db, err = sql.Open("sqlite", dbname)
	default:
		db, err = sql.Open("mysql", c.mysqlDSN(dbname))
	}
	if err != nil {
		return fmt.Errorf("Connecting to the %s %s database: %w", dbname, c.dbType, err)
	}
	defer db.Close()

	switch c.dbType {
	case "postgres":
		for _, tableSql := range postgres.SQLCreateTables() {
			if err := dbinitExec(db, tableSql); err != nil {
				return err
			}
		}
		for _, statement := range postgres.SQLDefineReplace() {
			if err := dbinitExec(db, statement); err != nil {
				return err
			}
		}
		if err := dbinitQuery(db, fmt.Sprintf(`SELECT replaceintometa('version', '%d')`, postgres.SchemaVersion())); err != nil {
			return err
		}
	case "mysql":
		if err := mysql.CreateDB(db, dbname); err != nil {
			return fmt.Errorf("error in CreateDB(%s): %w", dbname, err)
		}
		for _, tableSQL := range mysql.SQLCreateTables() {
			tableSQL = strings.ReplaceAll(tableSQL, "/*DB*/", dbname)
			if err := dbinitExec(db, tableSQL); err != nil {
				return err
			}
		}
		if err := dbinitExec(db, fmt.Sprintf(`REPLACE INTO meta VALUES ('version', '%d')`, mysql.SchemaVersion())); err != nil {
			return err
		}
	case "sqlite":
		if err := sqlite.InitDB(dbname); err != nil {
			return fmt.Errorf("error calling InitDB(%s): %w", dbname, err)
		}
	}
	return nil
}

func dbinitExec(db *sql.DB, query string) error {
	_, err := db.Exec(query)
	if err != nil {
		return fmt.Errorf("Error running SQL %q: %w", query, err)
	}
	return nil
}

func dbinitQuery(db *sql.DB, query string) error {
	r, err := db.Query(query)
	if err != nil {
		return fmt.Errorf("Error running SQL %q: %w", query, err)
	}
	return r.Close()
}

func (c *dbinitCmd) dbExists(db *sql.DB) (bool, error) {
	query := "SHOW DATABASES"
	switch c.dbType {
	case "postgres":
		query = "SELECT datname FROM pg_database"
	case "mysql":
		query = "SHOW DATABASES"
	case "sqlite":
		// There is no point in using sql.Open because it apparently does
		// not return an error when the file does not exist.
		fi, err := os.Stat(c.dbName)
		return err == nil && fi.Size() > 0, nil
	}
	rows, err := db.Query(query)
	if err != nil {
		return false, fmt.Errorf("SQL error for query %q: %w", query, err)
	}
	defer rows.Close()
	for rows.Next() {
		var db string
		if err := rows.Scan(&db); err != nil {
			return false, fmt.Errorf("SQL error for query %q: %w", query, err)
		}
		if db == c.dbName {
			return true, nil
		}
	}
	if err := rows.Err(); err != nil {
		return false, fmt.Errorf("SQL error for query %q: %w", query, err)
	}
	return false, nil
}

func (c *dbinitCmd) mysqlDSN(dbname string) string {
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "%s:%s@", c.user, c.password)
	if c.host != "localhost" {
		host := c.host
		if _, _, err := net.SplitHostPort(host); err != nil {
			host = net.JoinHostPort(host, "3306")
		}
		fmt.Fprintf(&buf, "tcp(%s)", host)
	}
	fmt.Fprintf(&buf, "/%s", dbname)
	return buf.String()
}
