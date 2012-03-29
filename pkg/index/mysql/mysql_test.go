/*
Copyright 2012 Google Inc.

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

package mysql_test

import (
	"database/sql"
	"errors"
	"sync"
	"testing"

	"camlistore.org/pkg/index"
	"camlistore.org/pkg/index/indextest"
	_ "camlistore.org/pkg/index/mysql" // TODO: use

	_ "camlistore.org/third_party/github.com/ziutek/mymysql/godrv"
)

var (
	once        sync.Once
	dbAvailable bool
	rootdb *sql.DB
)

func checkDB() {
	var err error
	if rootdb, err = sql.Open("mymysql", "mysql/root/root"); err == nil {
		dbAvailable = true
		return
	}
	if rootdb, err = sql.Open("mymysql", "mysql/root/"); err == nil {
		dbAvailable = true
		return
	}
}

func makeIndex() *index.Index {
	panic("TODO")
}

type mysqlTester struct{}

func (mysqlTester) test(t *testing.T, tfn func(*testing.T, func() *index.Index)) {
	t.Logf("TODO: implement")
	return

	once.Do(checkDB)
	if !dbAvailable {
		err := errors.New("Not running; start a MySQL daemon on the standard port (3306) with root password 'root' or '' (empty).")
		t.Fatalf("MySQL not available locally for testing: %v", err)
	}
	tfn(t, makeIndex)
}

func TestIndex_MySQL(t *testing.T) {
	mysqlTester{}.test(t, indextest.Index)
}

func TestPathsOfSignerTarget_MySQL(t *testing.T) {
	mysqlTester{}.test(t, indextest.PathsOfSignerTarget)
}

func TestFiles_MysQL(t *testing.T) {
	mysqlTester{}.test(t, indextest.Files)
}
