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

type Driver interface {
	Open(name string) (Conn, os.Error)
}

type Conn interface {
	Prepare(query string) (Stmt, os.Error)
	Close()
	Begin() (Tx, os.Error)
}

type Result interface {
	AutoIncrementId() (int64, os.Error)
	RowsAffected() (int64, os.Error)
}

type Stmt interface {
	Close()
	NumInput() int
	Exec(args []interface{}) (Result, os.Error)
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




