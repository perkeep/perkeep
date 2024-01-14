/*
Copyright 2012 The Perkeep Authors.

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

	"go4.org/jsonconfig"
	"perkeep.org/internal/osutil"
	"perkeep.org/pkg/index/indextest"
	"perkeep.org/pkg/sorted"
	"perkeep.org/pkg/sorted/kvtest"
	_ "perkeep.org/pkg/sorted/postgres"
	"perkeep.org/pkg/test/dockertest"
)

func newPostgresSorted(t *testing.T) sorted.KeyValue {
	dbname := "camlitest_" + osutil.Username()
	containerID, ip := dockertest.SetupPostgreSQLContainer(t, dbname)

	kv, err := sorted.NewKeyValue(jsonconfig.Obj{
		"type":     "postgres",
		"host":     ip,
		"database": dbname,
		"user":     dockertest.PostgresUsername,
		"password": dockertest.PostgresPassword,
		"sslmode":  "disable",
	})
	if err != nil {
		containerID.KillRemove(t)
		t.Fatal(err)
	}
	t.Cleanup(func() {
		kv.Close()
		containerID.KillRemove(t)
	})
	return kv
}

func TestSorted_Postgres(t *testing.T) {
	kv := newPostgresSorted(t)
	kvtest.TestSorted(t, kv)
}

func TestIndex_Postgres(t *testing.T) {
	indexTest(t, newPostgresSorted, indextest.Index)
}

func TestPathsOfSignerTarget_Postgres(t *testing.T) {
	indexTest(t, newPostgresSorted, indextest.PathsOfSignerTarget)
}

func TestFiles_Postgres(t *testing.T) {
	indexTest(t, newPostgresSorted, indextest.Files)
}

func TestEdgesTo_Postgres(t *testing.T) {
	indexTest(t, newPostgresSorted, indextest.EdgesTo)
}

func TestDelete_Postgres(t *testing.T) {
	indexTest(t, newPostgresSorted, indextest.Delete)
}
