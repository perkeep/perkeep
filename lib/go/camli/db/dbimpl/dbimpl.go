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

// Package dbimpl defines interfaces to be implemented by database
// drivers as used by package db.
//
// Code simply using databases should use package db.
package dbimpl

import (
	"os"
)

// must only deal with values:
//   int64    (no uint64 support, for now?)
//   float64
//   bool
//   nil
//   []byte

// Driver is the interface that must be implemented by database
// driver.
type Driver interface {
	// Open returns a new or cached connection to the database.
	// The dsn parameter, the Data Source Name, contains a
	// driver-specific string containing the database name,
	// connection parameters, authentication parameters, etc.
	//
	// The returned connection is only used by one goroutine at a
	// time.
	Open(dsn string) (Conn, os.Error)
}

// Execer is an optional interface that may be implemented by a Driver
// or a Conn.
//
// If not implemented by a Driver, the db package's DB.Exec method
// first obtains a free connection from its free pool or from the
// driver's Open method. Execer should only be implemented by drivers
// which can provide a more effcient implementation.
//
// If not implemented by a Conn, the db package's DB.Exec will first
// prepare a query, execute the statement, and then close the
// statement.
type Execer interface {
	Exec(query string, args []interface{}) (Result, os.Error)
}

type Conn interface {
	// Prepare returns a prepared statement, bound to this connection.
	Prepare(query string) (Stmt, os.Error)

	// Close invalidates and potentially stops any current
	// prepared statements and transactions, marking this
	// connection as no longer in use.  The driver may cache or
	// close its underlying connection to its database.
	Close() os.Error

	// Begin starts and returns a new transaction.
	Begin() (Tx, os.Error)
}

type Result interface {
	AutoIncrementId() (int64, os.Error)
	RowsAffected() (int64, os.Error)
}

// Stmt is a prepared statement. It is bound to a Conn and not
// used by multiple goroutines concurrently.
type Stmt interface {
	Close() os.Error
	NumInput() int
	Exec(args []interface{}) (Result, os.Error)
	Query(args []interface{}) (Rows, os.Error)
}

// ColumnConverter may be optionally implemented by Stmt to signal
// to the db package to do type conversions.
type ColumnConverter interface {
	ColumnCoverter(idx int) ValueConverter
}

type ValueConverter interface {
	ConvertValue(v interface{}) (interface{}, os.Error)
}

type Rows interface {
	Columns() []string
	Close() os.Error

	// Returns os.EOF at end of cursor
	Next(dest []interface{}) os.Error
}

type Tx interface {
	Commit() os.Error
	Rollback() os.Error
}

type RowsAffected int64

func (RowsAffected) AutoIncrementId() (int64, os.Error) {
	return 0, os.NewError("no AutoIncrementId available")
}

func (v RowsAffected) RowsAffected() (int64, os.Error) {
	return int64(v), nil
}

type ddlSuccess struct{}

var DDLSuccess Result = ddlSuccess{}

func (ddlSuccess) AutoIncrementId() (int64, os.Error) {
	return 0, os.NewError("no AutoIncrementId available after DDL statement")
}

func (ddlSuccess) RowsAffected() (int64, os.Error) {
	return 0, os.NewError("no RowsAffected available after DDL statement")
}
