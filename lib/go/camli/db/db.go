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

func Register(name string, driver dbimpl.Driver) {
	if driver == nil {
		panic("db: Register driver is nil")
	}
	if _, dup := drivers[name]; dup {
		panic("db: Register called twice for driver " + name)
	}
	drivers[name] = driver
}

type DB struct {
	driver dbimpl.Driver
	dbarg  string

	mu       sync.Mutex
	freeConn []dbimpl.Conn
}

func Open(driverName, database string) (*DB, os.Error) {
	driver, ok := drivers[driverName]
	if !ok {
		return nil, fmt.Errorf("db: unknown driver %q (forgotten import?)", driverName)
	}
	return &DB{driver: driver, dbarg: database}, nil
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
	return db.driver.Open(db.dbarg)
}

func (db *DB) connIfFree(wanted dbimpl.Conn) (conn dbimpl.Conn, ok bool) {
	db.mu.Lock()
	defer db.mu.Unlock()
	for n, conn := range db.freeConn {
		if conn == wanted {
			db.freeConn[n] = db.freeConn[len(db.freeConn)-1]
			db.freeConn = db.freeConn[:n-1]
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
	panic(todo())
}

var ErrNoRows = os.NewError("db: no rows in result set")

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

	mu  sync.Mutex
	css []connStmt // can use any that have idle connections
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
		args[n], err = valueToImpl(arg)
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
	panic(todo())
}

func (s *Stmt) QueryRow(args ...interface{}) *Row {
	panic(todo())
}

func (s *Stmt) Close() os.Error {
	panic(todo())
}

type Rows struct {

}

func (rs *Rows) Next() bool {
	panic(todo())
}

func (rs *Rows) Error() os.Error {
	panic(todo())
}

func (rs *Rows) Scan(dest ...interface{}) os.Error {
	panic(todo())
}

func (rs *Rows) Close() os.Error {
	panic(todo())
}

type Row struct {
	// One of these two will be non-nil:
	err  os.Error // deferred error for easy chaining
	rows *Rows
}

func (r *Row) Scan(dest ...interface{}) os.Error {
	if r.err != nil {
		return r.err
	}
	if !r.rows.Next() {
		return ErrNoRows
	}
	return r.rows.Scan(dest...)
}
