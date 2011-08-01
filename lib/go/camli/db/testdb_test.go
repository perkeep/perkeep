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
	"os"
	"sync"

	"camli/db/dbimpl"
)

type testDriver struct {
	mu  sync.Mutex
	dbs map[string]*testDB
}

type testDB struct {
	name string

	mu   sync.Mutex
	free []*testConn
}

type testConn struct {
	db *testDB // where to return ourselves to

	currTx *testTx
}

type testTx struct {
	c *testConn
}

type testStmt struct {
	q string
	c *testConn
}

var driver dbimpl.Driver = &testDriver{}

func init() {
	Register("test", driver)
}

func (d *testDriver) Open(name string) (dbimpl.Conn, os.Error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.dbs == nil {
		d.dbs = make(map[string]*testDB)
	}
	db, ok := d.dbs[name]
	if !ok {
		db = &testDB{name: name}
		d.dbs[name] = db
	}
	return db.conn()
}

func (db *testDB) returnConn(c *testConn) {
	db.mu.Lock()
	defer db.mu.Unlock()
	db.free = append(db.free, c)
}

func (db *testDB) conn() (dbimpl.Conn, os.Error) {
	db.mu.Lock()
	defer db.mu.Unlock()
	if len(db.free) > 0 {
		conn := db.free[len(db.free)-1]
		db.free = db.free[:len(db.free)-1]
		return conn, nil
	}
	return &testConn{db: db}, nil
}

func (c *testConn) Begin() (dbimpl.Tx, os.Error) {
	if c.currTx != nil {
		return nil, os.NewError("already in a transaction")
	}
	c.currTx = &testTx{c: c}
	return c.currTx, nil
}

func (c *testConn) Close() os.Error {
	if c.currTx != nil {
		return os.NewError("can't close; in a Transaction")
	}
	if c.db == nil {
		return os.NewError("can't close; already closed")
	}
	c.db.returnConn(c)
	c.db = nil
	return nil
}

func (c *testConn) Prepare(query string) (dbimpl.Stmt, os.Error) {
	fmt.Printf("Prepare: %q\n", query)
	return &testStmt{q: query, c: c}, nil
}

func (s *testStmt) Close() os.Error {
	return nil
}

func (s *testStmt) Exec(args []interface{}) (dbimpl.Result, os.Error) {
	fmt.Printf("EXEC(%#v)\n", args)
	return nil, os.NewError("TODO: implement")
}

func (s *testStmt) Query(args []interface{}) (dbimpl.Rows, os.Error) {
	println("QUERY")
	fmt.Println(args...)
	return nil, os.NewError("TODO: implement")
}

func (s *testStmt) NumInput() int {
	return 0
}

func (tx *testTx) Commit() os.Error {
	tx.c.currTx = nil
	return nil
}

func (tx *testTx) Rollback() os.Error {
	tx.c.currTx = nil
	return nil
}
