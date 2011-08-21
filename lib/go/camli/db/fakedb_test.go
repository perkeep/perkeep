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

package db

import (
	"fmt"
	"log"
	"os"
	"strings"
	"sync"

	"camli/db/dbimpl"
)

var _ = log.Printf

// fakeDriver is a fake database that implements Go's dbimpl.Driver
// interface, just for testing.
//
// It speaks a query language that's semantically similar to but
// syntantically different and simpler than SQL.  The syntax is as
// follows:
//
//   CREATE|<tablename>|<col>=<type>,<col>=<type>,...
//     where types are: "string", [u]int{8,16,32,64}, "bool"
//   INSERT|<tablename>|col=val,col2=val2,col3=?
//
// When opening a a fakeDriver's database, it starts empty with no
// tables.  All tables and data are stored in memory only.
type fakeDriver struct {
	mu        sync.Mutex
	openCount int
	dbs       map[string]*fakeDB
}

type fakeDB struct {
	name string

	mu     sync.Mutex
	free   []*fakeConn
	tables map[string]*table
}

type table struct {
	colname []string
	coltype []string
}

type fakeConn struct {
	db *fakeDB // where to return ourselves to

	currTx *fakeTx
}

type fakeTx struct {
	c *fakeConn
}

type fakeStmt struct {
	c *fakeConn
	q string // just for debugging

	cmd   string
	table string

	colName  []string // used by CREATE, INSERT
	colType  []string // used by CREATE
	colValue []string // used by INSERT (mix of strings and "?" for bound params)
}

var driver dbimpl.Driver = &fakeDriver{}

func init() {
	Register("test", driver)
}

// Supports dsn forms:
//    <dbname>
//    <dbname>;wipe
func (d *fakeDriver) Open(dsn string) (dbimpl.Conn, os.Error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.openCount++
	if d.dbs == nil {
		d.dbs = make(map[string]*fakeDB)
	}
	parts := strings.Split(dsn, ";")
	if len(parts) < 1 {
		return nil, os.NewError("fakedb: no database name")
	}
	name := parts[0]
	db, ok := d.dbs[name]
	if !ok {
		db = &fakeDB{name: name}
		d.dbs[name] = db
	}
	if len(parts) > 1 && parts[1] == "wipe" {
		db.wipe()
	}
	return &fakeConn{db: db}, nil
}

func (db *fakeDB) wipe() {
	db.mu.Lock()
	defer db.mu.Unlock()
	db.tables = nil
}

func (db *fakeDB) createTable(name string, columnNames, columnTypes []string) os.Error {
	db.mu.Lock()
	defer db.mu.Unlock()
	if db.tables == nil {
		db.tables = make(map[string]*table)
	}
	if _, exist := db.tables[name]; exist {
		return fmt.Errorf("table %q already exists", name)
	}
	if len(columnNames) != len(columnTypes) {
		return fmt.Errorf("create table of %q len(names) != len(types): %d vs %d",
			len(columnNames), len(columnTypes))
	}
	db.tables[name] = &table{colname: columnNames, coltype: columnTypes}
	return nil
}

func (db *fakeDB) columnType(table, column string) (typ string, ok bool) {
	db.mu.Lock()
	defer db.mu.Unlock()
	if db.tables == nil {
		println("no tables exist")
		return
	}
	t, ok := db.tables[table]
	if !ok {
		println("table no exist")
		return
	}
	for n, cname := range t.colname {
		if cname == column {
			return t.coltype[n], true
		}
	}
	return "", false
}

func (c *fakeConn) Begin() (dbimpl.Tx, os.Error) {
	if c.currTx != nil {
		return nil, os.NewError("already in a transaction")
	}
	c.currTx = &fakeTx{c: c}
	return c.currTx, nil
}

func (c *fakeConn) Close() os.Error {
	if c.currTx != nil {
		return os.NewError("can't close; in a Transaction")
	}
	if c.db == nil {
		return os.NewError("can't close; already closed")
	}
	c.db = nil
	return nil
}

func errf(msg string, args ...interface{}) os.Error {
	return os.NewError("fakedb: " + fmt.Sprintf(msg, args...))
}

func (c *fakeConn) Prepare(query string) (dbimpl.Stmt, os.Error) {
	if c.db == nil {
		panic("nil c.db; conn = " + fmt.Sprintf("%#v", c))
	}
	parts := strings.Split(query, "|")
	if len(parts) < 1 {
		return nil, errf("empty query")
	}
	cmd := parts[0]
	stmt := &fakeStmt{q: query, c: c, cmd: cmd}
	switch cmd {
	case "CREATE":
		if len(parts) != 3 {
			return nil, errf("invalid %q syntax with %d parts; want 3", cmd, len(parts))
		}
		stmt.table = parts[1]
		for n, colspec := range strings.Split(parts[2], ",") {
			nameType := strings.Split(colspec, "=")
			if len(nameType) != 2 {
				return nil, errf("CREATE table %q has invalid column spec of %q (index %d)", stmt.table, colspec, n)
			}
			stmt.colName = append(stmt.colName, nameType[0])
			stmt.colType = append(stmt.colType, nameType[1])
		}
	case "INSERT":
		if len(parts) != 3 {
			return nil, errf("invalid %q syntax with %d parts; want 3", cmd, len(parts))
		}
		stmt.table = parts[1]
		for n, colspec := range strings.Split(parts[2], ",") {
			nameVal := strings.Split(colspec, "=")
			if len(nameVal) != 2 {
				return nil, errf("INSERT table %q has invalid column spec of %q (index %d)", stmt.table, colspec, n)
			}
			column, value := nameVal[0], nameVal[1]
			ctype, ok := c.db.columnType(stmt.table, column)
			if !ok {
				return nil, errf("INSERT table %q references non-existent column %q", stmt.table, column)
			}
			if value != "?" {
				// TODO(bradfitz): check that
				// pre-bound value type conversion is
				// valid for this column type
				_ = ctype
			}
			stmt.colName = append(stmt.colName, column)
			stmt.colValue = append(stmt.colValue, value)
		}
	default:
		return nil, errf("unsupported command type %q", cmd)
	}
	return stmt, nil
}

func (s *fakeStmt) Close() os.Error {
	return nil
}

func (s *fakeStmt) Exec(args []interface{}) (dbimpl.Result, os.Error) {
	switch s.cmd {
	case "CREATE":
		if err := s.c.db.createTable(s.table, s.colName, s.colType); err != nil {
			return nil, err
		}
		return dbimpl.DDLSuccess, nil
	}
	fmt.Printf("EXEC statement, cmd=%q: %#v\n", s.cmd, s)
	return nil, fmt.Errorf("unimplemented statement Exec command type of %q", s.cmd)
}

func (s *fakeStmt) Query(args []interface{}) (dbimpl.Rows, os.Error) {
	println("QUERY")
	fmt.Println(args...)
	return nil, os.NewError(todo())
}

func (s *fakeStmt) NumInput() int {
	return 0
}

func (tx *fakeTx) Commit() os.Error {
	tx.c.currTx = nil
	return nil
}

func (tx *fakeTx) Rollback() os.Error {
	tx.c.currTx = nil
	return nil
}
