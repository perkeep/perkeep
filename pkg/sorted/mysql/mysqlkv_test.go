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
	"testing"

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
