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
	"os"

	"camli/db/dbimpl"
)

type DB struct {
	impl dbimpl.Driver
}

func Open(driver, name string) (*DB, os.Error) {
	panic("TODO: implement")
}

func (db *DB) Prepare(query string) (*Stmt, os.Error) {
	panic("TODO: implement")
}

func (db *DB) Exec(query string, args ...interface{}) os.Error {
	panic("TODO: implement")
}

func (db *DB) Query(query string, args ...interface{}) (*Rows, os.Error) {
	panic("TODO: implement")
}

func (db *DB) QueryRow(query string, args ...interface{}) *Row {
	panic("TODO: implement")
}

func (db *DB) Begin() (*Tx, os.Error) {
	panic("TODO: implement")
}

// DriverDatabase returns the database's underlying driver.
// This is non-portable and should only be used when
// needed.
func (db *DB) DriverDatabase() interface{} {
	return db.impl
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
	err os.Error
}

func (r *Row) Scan(dest ...interface{}) os.Error {
	if r.err != nil {
		return r.err
	}
	panic("TODO: implement")
}
