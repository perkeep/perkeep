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
	mu      sync.Mutex
	colname []string
	coltype []string
	rows    []*row
}

func (t *table) columnIndex(name string) int {
	for n, nname := range t.colname {
		if name == nname {
			return n
		}
	}
	return -1
}

type row struct {
	cols []interface{} // must be same size as its table colname + coltype
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

	colName      []string // used by CREATE, INSERT
	colType      []string // used by CREATE
	colValue     []string // used by INSERT (mix of strings and "?" for bound params)
	placeholders int      // number of ? params

	placeholderConverter []dbimpl.ValueConverter // used by INSERT
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

// must be called with db.mu lock held
func (db *fakeDB) table(table string) (*table, bool) {
	if db.tables == nil {
		return nil, false
	}
	t, ok := db.tables[table]
	return t, ok
}

func (db *fakeDB) columnType(table, column string) (typ string, ok bool) {
	db.mu.Lock()
	defer db.mu.Unlock()
	t, ok := db.table(table)
	if !ok {
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
	case "WIPE":
		// Nothing
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
			} else {
				stmt.placeholders++
				stmt.placeholderConverter = append(stmt.placeholderConverter, converterForType(ctype))
			}
			stmt.colName = append(stmt.colName, column)
			stmt.colValue = append(stmt.colValue, value)
		}
	default:
		return nil, errf("unsupported command type %q", cmd)
	}
	return stmt, nil
}

func (s *fakeStmt) ColumnCoverter(idx int) dbimpl.ValueConverter {
	return s.placeholderConverter[idx]
}

func (s *fakeStmt) Close() os.Error {
	return nil
}

func (s *fakeStmt) Exec(args []interface{}) (dbimpl.Result, os.Error) {
	db := s.c.db
	switch s.cmd {
	case "WIPE":
		db.wipe()
		return dbimpl.DDLSuccess, nil
	case "CREATE":
		if err := db.createTable(s.table, s.colName, s.colType); err != nil {
			return nil, err
		}
		return dbimpl.DDLSuccess, nil
	case "INSERT":
		return s.execInsert(args)
	}
	fmt.Printf("EXEC statement, cmd=%q: %#v\n", s.cmd, s)
	return nil, fmt.Errorf("unimplemented statement Exec command type of %q", s.cmd)
}

func (s *fakeStmt) execInsert(args []interface{}) (dbimpl.Result, os.Error) {
	db := s.c.db
	if len(args) != s.placeholders {
		panic("error in pkg db; should only get here if size is correct")
	}
	db.mu.Lock()
	t, ok := db.table(s.table)
	db.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("fakedb: table %q doesn't exist", s.table)
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	cols := make([]interface{}, len(t.colname))
	argPos := 0
	for n, colname := range s.colName {
		colidx := t.columnIndex(colname)
		if colidx == -1 {
			return nil, fmt.Errorf("fakedb: column %q doesn't exist or dropped since prepared statement was created", colname)
		}
		var val interface{}
		if s.colValue[n] == "?" {
			val = args[argPos]
			argPos++
		} else {
			val = s.colValue[n]
		}
		cols[colidx] = val
	}

	t.rows = append(t.rows, &row{cols: cols})
	return dbimpl.RowsAffected(1), nil
}

func (s *fakeStmt) Query(args []interface{}) (dbimpl.Rows, os.Error) {
	println("QUERY")
	fmt.Println(args...)
	return nil, os.NewError(todo())
}

func (s *fakeStmt) NumInput() int {
	return s.placeholders
}

func (tx *fakeTx) Commit() os.Error {
	tx.c.currTx = nil
	return nil
}

func (tx *fakeTx) Rollback() os.Error {
	tx.c.currTx = nil
	return nil
}

func converterForType(typ string) dbimpl.ValueConverter {
	switch typ {
	case "bool":
		return dbimpl.Bool
	case "int32":
		return dbimpl.Int32
	case "string":
		return dbimpl.String
	}
	panic("invalid fakedb column type of " + typ)
}
