/*
Copyright 2012 The Camlistore Authors.

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

package index_test

import (
	"testing"

	"camlistore.org/pkg/index/indextest"
	"camlistore.org/pkg/osutil"
	"camlistore.org/pkg/sorted"
	"camlistore.org/pkg/sorted/kvtest"
	_ "camlistore.org/pkg/sorted/mysql"
	"camlistore.org/pkg/test/dockertest"
	"go4.org/jsonconfig"
)

func newMySQLSorted(t *testing.T) (kv sorted.KeyValue, clean func()) {
	dbname := "camlitest_" + osutil.Username()
	containerID, ip := dockertest.SetupMySQLContainer(t, dbname)

	kv, err := sorted.NewKeyValue(jsonconfig.Obj{
		"type":     "mysql",
		"host":     ip + ":3306",
		"database": dbname,
		"user":     dockertest.MySQLUsername,
		"password": dockertest.MySQLPassword,
	})
	if err != nil {
		containerID.KillRemove(t)
		t.Fatal(err)
	}
	return kv, func() {
		kv.Close()
		containerID.KillRemove(t)
	}
}

func TestSorted_MySQL(t *testing.T) {
	kv, clean := newMySQLSorted(t)
	defer clean()
	kvtest.TestSorted(t, kv)
}

func TestIndex_MySQL(t *testing.T) {
	indexTest(t, newMySQLSorted, indextest.Index)
}

func TestPathsOfSignerTarget_MySQL(t *testing.T) {
	indexTest(t, newMySQLSorted, indextest.PathsOfSignerTarget)
}

func TestFiles_MySQL(t *testing.T) {
	indexTest(t, newMySQLSorted, indextest.Files)
}

func TestEdgesTo_MySQL(t *testing.T) {
	indexTest(t, newMySQLSorted, indextest.EdgesTo)
}

func TestDelete_MySQL(t *testing.T) {
	indexTest(t, newMySQLSorted, indextest.Delete)
}
