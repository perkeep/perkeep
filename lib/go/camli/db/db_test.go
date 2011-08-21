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

func TestDb(t *testing.T) {
	db, err := Open("test", "foo;wipe")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	err = db.Exec("CREATE|t1|name=string,age=int32,dead=bool")
	if err != nil {
		t.Errorf("Exec: %v", err)
	}
	stmt, err := db.Prepare("INSERT|t1|name=?,age=?")
	if err != nil {
		t.Errorf("Stmt, err = %v, %v", stmt, err)
	}
}
