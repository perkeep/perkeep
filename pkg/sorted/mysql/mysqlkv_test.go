/*
Copyright 2014 The Camlistore Authors

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

package mysql

import (
	"database/sql"
	"errors"
	"testing"
	"time"

	"camlistore.org/pkg/osutil"
	"camlistore.org/pkg/sorted"
	"camlistore.org/pkg/sorted/kvtest"
	"camlistore.org/pkg/test/dockertest"
	"go4.org/jsonconfig"
)

// TestMySQLKV tests against a real MySQL instance, using a Docker container.
func TestMySQLKV(t *testing.T) {
	dbname := "camlitest_" + osutil.Username()
	containerID, ip := dockertest.SetupMySQLContainer(t, dbname)
	defer containerID.KillRemove(t)

	// TODO(mpl): add test for serverVersion once we host the docker image ourselves
	// (and hence have the control over the version).

	kv, err := sorted.NewKeyValue(jsonconfig.Obj{
		"type":     "mysql",
		"host":     ip + ":3306",
		"database": dbname,
		"user":     dockertest.MySQLUsername,
		"password": dockertest.MySQLPassword,
	})
	if err != nil {
		t.Fatalf("mysql.NewKeyValue = %v", err)
	}
	kvtest.TestSorted(t, kv)
}

func TestRollback(t *testing.T) {
	dbname := "camlitest_" + osutil.Username()
	containerID, ip := dockertest.SetupMySQLContainer(t, dbname)
	defer containerID.KillRemove(t)

	kv, err := sorted.NewKeyValue(jsonconfig.Obj{
		"type":     "mysql",
		"host":     ip + ":3306",
		"database": dbname,
		"user":     dockertest.MySQLUsername,
		"password": dockertest.MySQLPassword,
	})
	if err != nil {
		t.Fatalf("mysql.NewKeyValue = %v", err)
	}

	kv.(*keyValue).KeyValue.BatchSetFunc = func(*sql.Tx, string, string) error {
		return errors.New("Forced failure to trigger a rollback")
	}

	nbConnections := 2
	tick := time.AfterFunc(5*time.Second, func() {
		// We have to force close the connection, otherwise the connection hogging does not even
		// let us exit the func with t.Fatal (How? why?)
		kv.(*keyValue).DB.Close()
		t.Fatal("Test failed because SQL connections blocked by unrolled transactions")
	})
	kv.(*keyValue).DB.SetMaxOpenConns(nbConnections)
	for i := 0; i < nbConnections+1; i++ {
		b := kv.BeginBatch()
		// Making the transaction fail, to force a rollback
		// -> this whole test fails before we introduce the rollback in CommitBatch.
		b.Set("foo", "bar")
		if err := kv.CommitBatch(b); err == nil {
			t.Fatal("wanted failed commit because too large a key")
		}
	}
	tick.Stop()
}
