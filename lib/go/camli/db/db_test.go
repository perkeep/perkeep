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
	"testing"
)

func newTestDB(t *testing.T, name string) *DB {
	db, err := Open("test", "foo")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := db.Exec("WIPE"); err != nil {
		t.Fatalf("exec wipe: %v", err)
	}
	return db
}

func exec(t *testing.T, db *DB, query string, args ...interface{}) {
	err := db.Exec(query, args...)
	if err != nil {
		t.Fatalf("Exec of %q: %v", query, err)
	}
}

func TestQuery(t *testing.T) {
	db := newTestDB(t, "foo")
	exec(t, db, "CREATE|t1|name=string,age=int32,dead=bool")
	exec(t, db, "INSERT|t1|name=Brad,age=?", 31)

}

func TestDb(t *testing.T) {
	db := newTestDB(t, "foo")
	exec(t, db, "CREATE|t1|name=string,age=int32,dead=bool")
	stmt, err := db.Prepare("INSERT|t1|name=?,age=?")
	if err != nil {
		t.Errorf("Stmt, err = %v, %v", stmt, err)
	}

	type execTest struct {
		args    []interface{}
		wantErr string
	}
	execTests := []execTest{
		// Okay:
		{[]interface{}{"Brad", 31}, ""},
		{[]interface{}{"Brad", int64(31)}, ""},
		{[]interface{}{"Bob", "32"}, ""},
		{[]interface{}{7, 9}, ""},

		// Invalid conversions:
		{[]interface{}{"Brad", int64(0xFFFFFFFF)}, "db: converting Exec column index 1: value 4294967295 overflows int32"},
		{[]interface{}{"Brad", "strconv fail"}, "db: converting Exec column index 1: value \"strconv fail\" can't be converted to int32"},

		// Wrong number of args:
		{[]interface{}{}, "db: expected 2 arguments, got 0"},
		{[]interface{}{1, 2, 3}, "db: expected 2 arguments, got 3"},
	}
	for n, et := range execTests {
		err := stmt.Exec(et.args...)
		errStr := ""
		if err != nil {
			errStr = err.String()
		}
		if errStr != et.wantErr {
			t.Errorf("stmt.Execute #%d: for %v, got error %q, want error %q",
				n, et.args, errStr, et.wantErr)
		}
	}
}
