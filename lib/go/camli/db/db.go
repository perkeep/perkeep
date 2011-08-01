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
}

func Open(driverName, database string) (*DB, os.Error) {
	driver, ok := drivers[driverName]
	if !ok {
		return nil, fmt.Errorf("db: unknown driver %q (forgotten import?)", driverName)
	}
	return &DB{driver: driver, dbarg: database}, nil
}

func (db *DB) conn() (dbimpl.Conn, os.Error) {
	return db.driver.Open(db.dbarg)
}

func (db *DB) Prepare(query string) (*Stmt, os.Error) {
	ci, err := db.conn()
	if err != nil {
		return nil, err
	}
	si, err := ci.Prepare(query)
	if err != nil {
		return nil, err
	}
	stmt := &Stmt{
		db: db,
		ci: ci,
		si: si,
	}
	return stmt, nil
}

func (db *DB) Exec(query string, args ...interface{}) os.Error {
	conn, err := db.conn()
	if err != nil {
		return err
	}
	defer conn.Close()
	// TODO(bradfitz): check to see if conn implements optional
	// dbimpl.ConnExecer interface and use that instead of
	// Prepare+Exec
	stmt, err := conn.Prepare(query)
	if err != nil {
		return err
	}
	defer stmt.Close()
	_, err = stmt.Exec(args)
	return err
}

func (db *DB) Query(query string, args ...interface{}) (*Rows, os.Error) {
	panic("TODO: implement")
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
	panic("TODO: implement")
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
	panic("TODO: implement")
}

func (tx *Tx) Rollback() os.Error {
	panic("TODO: implement")
}

func (tx *Tx) Prepare(query string) (*Stmt, os.Error) {
	panic("TODO: implement")
}

func (tx *Tx) Exec(query string, args ...interface{}) {
	panic("TODO: implement")
}

func (tx *Tx) Query(query string, args ...interface{}) (*Rows, os.Error) {
	panic("TODO: implement")
}

func (tx *Tx) QueryRow(query string, args ...interface{}) *Row {
	panic("TODO: implement")
}

type Stmt struct {
	db *DB         // where we came from
	ci dbimpl.Conn // owned
	si dbimpl.Stmt // owned	
}

func (s *Stmt) Exec(args ...interface{}) os.Error {
	panic("TODO: implement")
}

func (s *Stmt) Query(args ...interface{}) (*Rows, os.Error) {
	panic("TODO: implement")
}

func (s *Stmt) QueryRow(args ...interface{}) *Row {
	panic("TODO: implement")
}

func (s *Stmt) Close() os.Error {
	panic("TODO: implement")
}

type Rows struct {

}

func (rs *Rows) Next() bool {
	panic("TODO: implement")
}

func (rs *Rows) Error() os.Error {
	panic("TODO: implement")
}

func (rs *Rows) Scan(dest ...interface{}) os.Error {
	panic("TODO: implement")
}

func (rs *Rows) Close() os.Error {
	panic("TODO: implement")
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
