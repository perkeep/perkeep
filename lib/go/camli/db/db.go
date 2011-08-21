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
	if n := len(db.freeConn); n > 0 {
		conn := db.freeConn[n-1]
		db.freeConn = db.freeConn[:n-1]
		return conn, nil
	}
	return db.driver.Open(db.dbarg)
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
		ci:    ci,
		si:    si,
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

type Stmt struct {
	db *DB         // where we came from
	ci dbimpl.Conn // the Conn that we're bound to. to execute, we need to wait for this Conn to be free.
	si dbimpl.Stmt // owned	

	// query is the query that created the Stmt
	query string
}

func todo() string {
	_, file, line, _ := runtime.Caller(1)
	return fmt.Sprintf("%s:%d: TODO: implement", file, line)
}

func (s *Stmt) Exec(args ...interface{}) os.Error {
	panic(todo())
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
