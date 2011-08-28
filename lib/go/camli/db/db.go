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

// Package db provides a generic interface around SQL (or SQL-like)
// databases.
package db

import (
	"fmt"
	"os"
	"runtime"
	"sync"

	"camli/db/dbimpl"
)

var drivers = make(map[string]dbimpl.Driver)

// Register makes a database driver available by the provided name.
// It's a fatal error to register the same or different drivers with
// a duplicate name.
func Register(name string, driver dbimpl.Driver) {
	if driver == nil {
		panic("db: Register driver is nil")
	}
	if _, dup := drivers[name]; dup {
		panic("db: Register called twice for driver " + name)
	}
	drivers[name] = driver
}

// MaybeString is type representing a string which may be null. A pointer
// to a MaybeString may be used in scan to test whether a column is null.
// TODO(bradfitz): implement, and add other types.
type MaybeString struct {
	String string
	Ok     bool
}

// ScannerInto is an interface used by Scan.
// TODO(bradfitz): flesh this out?
type ScannerInto interface {
	// v is nil for NULL database columns.
	ScanInto(v interface{}) os.Error
}

// ErrNoRows is returned by Scan when QueryRow doesn't return a
// row. In such a case, QueryRow returns a placeholder *Row value that
// defers this error until a Scan.
var ErrNoRows = os.NewError("db: no rows in result set")

// DB is a database handle. It's safe for concurrent use by multiple
// goroutines.
type DB struct {
	driver dbimpl.Driver
	dsn    string

	mu       sync.Mutex
	freeConn []dbimpl.Conn
}

// Open opens a database specified by its database driver name and a
// driver-specific data source name, usually consisting of at least a
// database name and connection information.
//
// Most users will open a database via a driver-specific connection
// helper function that returns a *DB.
func Open(driverName, dataSourceName string) (*DB, os.Error) {
	driver, ok := drivers[driverName]
	if !ok {
		return nil, fmt.Errorf("db: unknown driver %q (forgotten import?)", driverName)
	}
	return &DB{driver: driver, dsn: dataSourceName}, nil
}

func (db *DB) maxIdleConns() int {
	const defaultMaxIdleConns = 2
	// TODO(bradfitz): ask driver, if supported, for its default preference
	// TODO(bradfitz): let users override?
	return defaultMaxIdleConns
}

// conn returns a newly-opened or cached dbimpl.Conn
func (db *DB) conn() (dbimpl.Conn, os.Error) {
	db.mu.Lock()
	if n := len(db.freeConn); n > 0 {
		conn := db.freeConn[n-1]
		db.freeConn = db.freeConn[:n-1]
		db.mu.Unlock()
		return conn, nil
	}
	db.mu.Unlock()
	return db.driver.Open(db.dsn)
}

func (db *DB) connIfFree(wanted dbimpl.Conn) (conn dbimpl.Conn, ok bool) {
	db.mu.Lock()
	defer db.mu.Unlock()
	for n, conn := range db.freeConn {
		if conn == wanted {
			db.freeConn[n] = db.freeConn[len(db.freeConn)-1]
			db.freeConn = db.freeConn[:len(db.freeConn)-1]
			return wanted, true
		}
	}
	return nil, false
}

func (db *DB) putConn(c dbimpl.Conn) {
	if n := len(db.freeConn); n < db.maxIdleConns() {
		db.freeConn = append(db.freeConn, c)
		return
	}
	db.closeConn(c)
}

func (db *DB) closeConn(c dbimpl.Conn) {
	// TODO: check to see if we need this Conn for any prepared statements
	// that are active.
	c.Close()
}

func (db *DB) Prepare(query string) (*Stmt, os.Error) {
	// TODO: check if db.driver supports an optional
	// dbimpl.Preparer interface and call that instead, if so,
	// otherwise we make a prepared statement that's bound
	// to a connection, and to execute this prepared statement
	// we either need to use this connection (if it's free), else
	// get a new connection + re-prepare + execute on that one.
	ci, err := db.conn()
	if err != nil {
		return nil, err
	}
	defer db.putConn(ci)
	si, err := ci.Prepare(query)
	if err != nil {
		return nil, err
	}
	stmt := &Stmt{
		db:    db,
		query: query,
		css:   []connStmt{{ci, si}},
	}
	return stmt, nil
}

func (db *DB) Exec(query string, args ...interface{}) os.Error {
	// TODO(bradfitz): check to see if db.driver implements
	// optional dbimpl.Execer interface and use that instead of
	// even asking for a connection.
	conn, err := db.conn()
	if err != nil {
		return err
	}
	defer db.putConn(conn)
	// TODO(bradfitz): check to see if db.driver implements
	// optional dbimpl.ConnExecer interface and use that instead
	// of Prepare+Exec
	sti, err := conn.Prepare(query)
	if err != nil {
		return err
	}
	defer sti.Close()
	_, err = sti.Exec(args)
	return err
}

func (db *DB) Query(query string, args ...interface{}) (*Rows, os.Error) {
	stmt, err := db.Prepare(query)
	if err != nil {
		return nil, err
	}
	defer stmt.Close()
	return stmt.Query(args...)
}

func (db *DB) QueryRow(query string, args ...interface{}) *Row {
	rows, err := db.Query(query, args...)
	if err != nil {
		return &Row{err: err}
	}
	return &Row{rows: rows}
}

func (db *DB) Begin() (*Tx, os.Error) {
	panic(todo())
}

// DriverDatabase returns the database's underlying driver.
// This is non-portable and should only be used when
// needed.
func (db *DB) Driver() dbimpl.Driver {
	return db.driver
}

// Tx is an in-progress database transaction.
type Tx struct {

}

func (tx *Tx) Commit() os.Error {
	panic(todo())
}

func (tx *Tx) Rollback() os.Error {
	panic(todo())
}

func (tx *Tx) Prepare(query string) (*Stmt, os.Error) {
	panic(todo())
}

func (tx *Tx) Exec(query string, args ...interface{}) {
	panic(todo())
}

func (tx *Tx) Query(query string, args ...interface{}) (*Rows, os.Error) {
	panic(todo())
}

func (tx *Tx) QueryRow(query string, args ...interface{}) *Row {
	panic(todo())
}

// connStmt is a prepared statement on a particular connection.
type connStmt struct {
	ci dbimpl.Conn
	si dbimpl.Stmt
}

type Stmt struct {
	// Immutable:
	db    *DB    // where we came from
	query string // that created the Sttm

	mu     sync.Mutex
	closed bool
	css    []connStmt // can use any that have idle connections
}

func todo() string {
	_, file, line, _ := runtime.Caller(1)
	return fmt.Sprintf("%s:%d: TODO: implement", file, line)
}

func (s *Stmt) Exec(args ...interface{}) os.Error {
	ci, si, err := s.connStmt()
	if err != nil {
		return err
	}
	defer s.db.putConn(ci)

	if want := si.NumInput(); len(args) != want {
		return fmt.Errorf("db: expected %d arguments, got %d", want, len(args))
	}

	// Convert args if the driver knows its own types.
	if cc, ok := si.(dbimpl.ColumnConverter); ok {
		for n, arg := range args {
			args[n], err = cc.ColumnCoverter(n).ConvertValue(arg)
			if err != nil {
				return fmt.Errorf("db: converting Exec column index %d: %v", n, err)
			}
		}
	}

	// Then convert everything into the restricted subset
	// of types that the dbimpl package needs to know about.
	// all integers -> int64, etc
	for n, arg := range args {
		var err os.Error
		args[n], err = dbimpl.SubsetValue(arg)
		if err != nil {
			return fmt.Errorf("db: error converting index %d: %v", n, err)
		}
	}

	resi, err := si.Exec(args)
	if err != nil {
		return err
	}
	_ = resi // TODO(bradfitz): return these stats, converted to pkg db type
	return nil
}

func (s *Stmt) connStmt(args ...interface{}) (dbimpl.Conn, dbimpl.Stmt, os.Error) {
	s.mu.Lock()
	if s.closed {
		return nil, nil, os.NewError("db: statement is closed")
	}
	var cs connStmt
	match := false
	for _, v := range s.css {
		// TODO(bradfitz): lazily clean up entries in this
		// list with dead conns while enumerating
		if _, match = s.db.connIfFree(cs.ci); match {
			cs = v
			break
		}
	}
	s.mu.Unlock()

	// Make a new conn if all are busy.
	// TODO(bradfitz): or wait for one? make configurable later?
	if !match {
		ci, err := s.db.conn()
		if err != nil {
			return nil, nil, err
		}
		si, err := ci.Prepare(s.query)
		if err != nil {
			return nil, nil, err
		}
		s.mu.Lock()
		cs = connStmt{ci, si}
		s.css = append(s.css, cs)
		s.mu.Unlock()
	}

	return cs.ci, cs.si, nil
}

func (s *Stmt) Query(args ...interface{}) (*Rows, os.Error) {
	ci, si, err := s.connStmt(args...)
	if err != nil {
		return nil, err
	}
	if len(args) != si.NumInput() {
		return nil, fmt.Errorf("db: statement expects %d inputs; got %d", si.NumInput(), len(args))
	}
	rowsi, err := si.Query(args)
	if err != nil {
		s.db.putConn(ci)
		return nil, err
	}
	// Note: ownership of ci passes to the *Rows
	rows := &Rows{
		db:    s.db,
		ci:    ci,
		rowsi: rowsi,
	}
	return rows, nil
}

func (s *Stmt) QueryRow(args ...interface{}) *Row {
	panic(todo())
}

func (s *Stmt) Close() os.Error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return os.NewError("db: statement already closed")
	}
	s.closed = true
	for _, v := range s.css {
		if ci, match := s.db.connIfFree(v.ci); match {
			v.si.Close()
			s.db.putConn(ci)
		} else {
			// TODO(bradfitz): care that we can't close
			// this statement because the statement's
			// connection is in use?
		}
	}
	return nil
}

// Rows is the result of a query. Its cursor starts before the first row
// of the result set. Use Next to advance through the rows:
//
//     rows, err := db.Query("SELECT ...")
//     ...
//     for rows.Next() {
//         var id int
//         var name string
//         err = rows.Scan(&id, &name)
//         ...
//     }
//     err = rows.Error() // get any Error encountered during iteration
//     ...
type Rows struct {
	db    *DB
	ci    dbimpl.Conn // owned; must be returned when Rows is closed
	rowsi dbimpl.Rows

	closed   bool
	lastcols []interface{}
	lasterr  os.Error
}

// Next advances the Rows' cursor (which starts before the first
// row). Next returns true if there's another row and the cursor was
// moved.
func (rs *Rows) Next() bool {
	if rs.closed {
		return false
	}
	if rs.lasterr != nil {
		return false
	}
	if rs.lastcols == nil {
		rs.lastcols = make([]interface{}, len(rs.rowsi.Columns()))
	}
	rs.lasterr = rs.rowsi.Next(rs.lastcols)
	return rs.lasterr == nil
}

// Error returns the error, if any, that was encountered during iteration.
func (rs *Rows) Error() os.Error {
	if rs.lasterr == os.EOF {
		return nil
	}
	return rs.lasterr
}

// Scan copies the columns in the current row into variables referened in dest.
func (rs *Rows) Scan(dest ...interface{}) os.Error {
	if rs.closed {
		return os.NewError("db: Rows closed")
	}
	if rs.lasterr != nil {
		return rs.lasterr
	}
	if rs.lastcols == nil {
		return os.NewError("db: Scan called without calling Next")
	}
	if len(dest) != len(rs.lastcols) {
		return fmt.Errorf("db: expected %d destination arguments in Scan, not %d", len(rs.lastcols), len(dest))
	}
	for i, sv := range rs.lastcols {
		err := copyConvert(dest[i], sv)
		if err != nil {
			return fmt.Errorf("db: Scan error on column index %d: %v", i, err)
		}
	}
	return nil
}

// Close closes the Rows, preventing further enumeration. If the end
// if encountered normally the Rows are closed automatically. Calling
// Close is idempotent.
func (rs *Rows) Close() os.Error {
	if rs.closed {
		return nil
	}
	rs.closed = true
	err := rs.rowsi.Close()
	rs.db.putConn(rs.ci)
	return err
}

// Row is the result of calling QueryRow to select a single row. If no
// matching row was found, an error is deferred until the Scan method
// is called.
type Row struct {
	// One of these two will be non-nil:
	err  os.Error // deferred error for easy chaining
	rows *Rows
}

// Scan copies the columns from the matched row into the variables
// pointed at in dest. If no row was matched, ErrNoRows is returned.
func (r *Row) Scan(dest ...interface{}) os.Error {
	if r.err != nil {
		return r.err
	}
	defer r.rows.Close()
	if !r.rows.Next() {
		return ErrNoRows
	}
	return r.rows.Scan(dest...)
}
