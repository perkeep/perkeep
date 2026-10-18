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
	"bytes"
	"database/sql"
	"flag"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"perkeep.org/pkg/sorted/sqlite"
)

func TestDBInitFlags(t *testing.T) {
	for _, tt := range []struct {
		name string
		args []string
		want dbinitCmd
	}{
		{"defaults", nil, dbinitCmd{user: "root", host: "localhost", dbType: "mysql", sslMode: "require"}},
		{"explicit", []string{"--user=admin", "--password=secret", "--host=db:4406", "--dbname=index", "--dbtype=postgres", "--sslmode=verify-full", "--wipe", "--ignoreexists"}, dbinitCmd{user: "admin", password: "secret", host: "db:4406", dbName: "index", dbType: "postgres", sslMode: "verify-full", wipe: true, keep: true}},
		{"defaults-again", nil, dbinitCmd{user: "root", host: "localhost", dbType: "mysql", sslMode: "require"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			flags := flag.NewFlagSet("dbinit-test", flag.ContinueOnError)
			flags.SetOutput(io.Discard)
			cmd := newDBInitCmd(flags)
			if err := flags.Parse(tt.args); err != nil {
				t.Fatal(err)
			}
			if *cmd != tt.want {
				t.Errorf("flags = %+v; want %+v", *cmd, tt.want)
			}
		})
	}
}

func TestRunAdminDBInitArguments(t *testing.T) {
	root := adminConfigDir(t)
	file := filepath.Join(root, "untouched.sqlite")
	const contents = "not a database; argument validation must not open or truncate this file"
	if err := os.WriteFile(file, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
	serverFlags := func() map[string]string {
		m := make(map[string]string)
		flag.CommandLine.VisitAll(func(f *flag.Flag) { m[f.Name] = f.Value.String() })
		return m
	}
	before := serverFlags()
	for _, tt := range []struct {
		name string
		args []string
		code int
		text string
	}{
		{"short-help", []string{"-h"}, 0, "Usage: perkeepd admin dbinit"},
		{"help", []string{"-help"}, 0, "--ignoreexists takes precedence over --wipe"},
		{"long-help", []string{"--help"}, 0, "-sslmode"},
		{"missing-dbname", nil, 2, "--dbname flag required"},
		{"empty-dbname", []string{"--dbname="}, 2, "--dbname flag required"},
		{"missing-value", []string{"--dbname"}, 2, "flag needs an argument"},
		{"invalid-type", []string{"--dbname=" + file, "--dbtype=other", "--wipe"}, 2, "--dbtype flag"},
		{"invalid-bool", []string{"--wipe=invalid"}, 2, "invalid boolean value"},
		{"positional", []string{"--dbname=" + file, "--dbtype=sqlite", "--wipe", "extra"}, 2, "does not accept positional arguments"},
		{"delimiter", []string{"--dbname=" + file, "--dbtype=sqlite", "--wipe", "--", "extra"}, 2, "does not accept positional arguments"},
		{"server-listen", []string{"--listen=:0"}, 2, "flag provided but not defined"},
		{"server-config", []string{"--configfile=server-config.json"}, 2, "flag provided but not defined"},
		{"server-reindex", []string{"--reindex"}, 2, "flag provided but not defined"},
		{"server-version", []string{"--version"}, 2, "flag provided but not defined"},
		{"old-global-verbose", []string{"--verbose"}, 2, "flag provided but not defined"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			stderr := runDBInitTest(t, tt.code, tt.args...)
			if !strings.Contains(stderr, tt.text) || !strings.Contains(stderr, "Usage: perkeepd admin dbinit") {
				t.Errorf("stderr = %q; want %q and usage", stderr, tt.text)
			}
		})
	}
	if after := serverFlags(); !reflect.DeepEqual(before, after) {
		t.Errorf("dbinit changed server flags: before %v, after %v", before, after)
	}
	if got, err := os.ReadFile(file); err != nil || string(got) != contents {
		t.Errorf("invalid arguments changed SQLite input: %q, %v", got, err)
	}
	if entries, err := os.ReadDir(root); err != nil || len(entries) != 1 {
		t.Errorf("argument handling touched isolated config directory: %v, %v", entries, err)
	}
}

func runDBInitTest(t *testing.T, wantStatus int, args ...string) string {
	t.Helper()
	var stdout, stderr bytes.Buffer
	if got := runAdminDBInit(args, &stdout, &stderr); got != wantStatus {
		t.Fatalf("dbinit %q: status = %d; want %d; stderr: %s", args, got, wantStatus, &stderr)
	}
	if stdout.Len() != 0 {
		t.Errorf("unexpected stdout: %s", &stdout)
	}
	if wantStatus == 0 && stderr.Len() != 0 && !strings.Contains(stderr.String(), "Usage:") {
		t.Errorf("unexpected stderr: %s", &stderr)
	}
	return stderr.String()
}

func openDBInitSQLite(t *testing.T, file string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", file)
	if err != nil {
		t.Fatal(err)
	}
	return db
}

func checkDBInitSQLite(t *testing.T, file string, wantRows, wantMarker int) {
	t.Helper()
	db := openDBInitSQLite(t, file)
	defer db.Close()
	var version int
	if err := db.QueryRow("SELECT value FROM meta WHERE metakey='version'").Scan(&version); err != nil || version != sqlite.SchemaVersion() {
		t.Errorf("schema version = %d, %v; want %d", version, err, sqlite.SchemaVersion())
	}
	var journal string
	if err := db.QueryRow("PRAGMA journal_mode").Scan(&journal); err != nil || journal != "wal" {
		t.Errorf("journal mode = %q, %v; want wal", journal, err)
	}
	for _, tt := range []struct {
		query string
		want  int
	}{
		{"SELECT wr FROM pragma_table_list('rows') WHERE schema='main'", 1},
		{"SELECT count(*) FROM rows", wantRows},
		{"SELECT count(*) FROM meta WHERE metakey='reindexing'", wantMarker},
	} {
		var got int
		if err := db.QueryRow(tt.query).Scan(&got); err != nil || got != tt.want {
			t.Errorf("%s: got %d, %v; want %d", tt.query, got, err, tt.want)
		}
	}
	if wantRows == 1 {
		var value string
		if err := db.QueryRow("SELECT v FROM rows WHERE k='keep-me'").Scan(&value); err != nil || value != "unchanged" {
			t.Errorf("existing row = %q, %v; want unchanged", value, err)
		}
	}
}

func TestRunAdminDBInitSQLite(t *testing.T) {
	for _, existingEmpty := range []bool{false, true} {
		name := "missing"
		if existingEmpty {
			name = "empty"
		}
		t.Run(name, func(t *testing.T) {
			root := adminConfigDir(t)
			t.Chdir(root)
			file := "index.sqlite" // Explicit relative paths resolve from the working directory.
			if existingEmpty {
				if err := os.WriteFile(file, nil, 0600); err != nil {
					t.Fatal(err)
				}
			}
			args := []string{"--dbtype=sqlite", "--dbname=" + file}
			if stderr := runDBInitTest(t, 0, args...); stderr != "" {
				t.Fatalf("successful creation stderr: %s", stderr)
			}
			checkDBInitSQLite(t, file, 0, 0)
			db := openDBInitSQLite(t, file)
			for _, query := range []string{
				"INSERT INTO rows VALUES ('keep-me', 'unchanged')",
				"INSERT INTO meta VALUES ('reindexing', '1')",
			} {
				if _, err := db.Exec(query); err != nil {
					db.Close()
					t.Fatal(err)
				}
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			if stderr := runDBInitTest(t, 2, args...); !strings.Contains(stderr, "already exists, but --wipe not given") {
				t.Fatalf("existing refusal stderr: %s", stderr)
			}
			checkDBInitSQLite(t, file, 1, 1)
			for _, extra := range [][]string{{"--ignoreexists"}, {"--ignoreexists", "--wipe"}} {
				if stderr := runDBInitTest(t, 0, append(append([]string{}, args...), extra...)...); stderr != "" {
					t.Fatalf("ignoreexists stderr: %s", stderr)
				}
				checkDBInitSQLite(t, file, 1, 1)
			}
			if stderr := runDBInitTest(t, 0, append(args, "--wipe")...); stderr != "" {
				t.Fatalf("wipe stderr: %s", stderr)
			}
			checkDBInitSQLite(t, file, 0, 0)
		})
	}
}

func TestRunAdminDBInitOperationErrors(t *testing.T) {
	root := adminConfigDir(t)
	for _, tt := range []struct {
		name, file, text string
	}{
		{"missing-parent", filepath.Join(root, "missing", "index.sqlite"), "Error connecting to the root sqlite database"},
		{"directory", root, "Error connecting to the root sqlite database"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			stderr := runDBInitTest(t, 1, "--dbtype=sqlite", "--dbname="+tt.file)
			if !strings.Contains(stderr, tt.text) || strings.Count(stderr, "perkeepd admin dbinit:") != 1 || strings.Contains(stderr, "Usage:") {
				t.Errorf("stderr = %q; want one operation error containing %q, without usage", stderr, tt.text)
			}
		})
	}
}

func TestDBInitMySQLDSN(t *testing.T) {
	for _, tt := range []struct {
		host, dbname, want string
	}{
		{"localhost", "", "admin:secret@/"},
		{"localhost", "index", "admin:secret@/index"},
		{"db.example", "index", "admin:secret@tcp(db.example:3306)/index"},
		{"db.example:4406", "index", "admin:secret@tcp(db.example:4406)/index"},
		{"127.0.0.1", "index", "admin:secret@tcp(127.0.0.1:3306)/index"},
		{"::1", "index", "admin:secret@tcp([::1]:3306)/index"},
		{"[::1]:4406", "index", "admin:secret@tcp([::1]:4406)/index"},
	} {
		t.Run(tt.host+"/"+tt.dbname, func(t *testing.T) {
			cmd := dbinitCmd{user: "admin", password: "secret", host: tt.host}
			if got := cmd.mysqlDSN(tt.dbname); got != tt.want {
				t.Errorf("mysqlDSN(%q) = %q; want %q", tt.dbname, got, tt.want)
			}
		})
	}
}
