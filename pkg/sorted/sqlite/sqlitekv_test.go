/*
Copyright 2014 The Perkeep Authors

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

package sqlite

import (
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"

	"go4.org/jsonconfig"
	"perkeep.org/pkg/sorted"
	"perkeep.org/pkg/sorted/kvtest"
)

func TestSQLiteKV(t *testing.T) {
	tmpDir, err := ioutil.TempDir("", "camlistore-sqlitekv_test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)
	dbname := filepath.Join(tmpDir, "testdb.sqlite")
	kv, err := sorted.NewKeyValue(jsonconfig.Obj{
		"type": "sqlite",
		"file": dbname,
	})
	if err != nil {
		t.Fatalf("Could not create sqlite sorted kv at %v: %v", dbname, err)
	}
	kvtest.TestSorted(t, kv)
}
