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
	"os"
	"path/filepath"
	"testing"

	"go4.org/jsonconfig"
	"perkeep.org/pkg/sorted"
	"perkeep.org/pkg/sorted/kvtest"
)

func TestSQLiteKV(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "camlistore-sqlitekv_test")
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

// TestReadsDontWaitForEachOther checks that reads don't hold the
// single-writer gate: with an iterator open, the same goroutine can
// open another iterator, Get, and Set. Each of those used to wait
// forever for the gate the first iterator held.
func TestReadsDontWaitForEachOther(t *testing.T) {
	kv, err := NewStorage(filepath.Join(t.TempDir(), "readers.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer kv.Close()
	for _, k := range []string{"a", "b", "c"} {
		if err := kv.Set(k, "v"+k); err != nil {
			t.Fatal(err)
		}
	}

	it1 := kv.Find("", "")
	if !it1.Next() || it1.Key() != "a" {
		t.Fatalf("first iterator: want key a; err=%v", it1.Close())
	}
	it2 := kv.Find("b", "")
	if !it2.Next() || it2.Key() != "b" {
		t.Fatalf("second iterator: want key b; err=%v", it2.Close())
	}
	if v, err := kv.Get("c"); err != nil || v != "vc" {
		t.Errorf("Get(c) = %q, %v; want vc", v, err)
	}
	if err := kv.Set("d", "vd"); err != nil {
		t.Errorf("Set(d) = %v", err)
	}
	// The Set committed, so a new read sees it, even while the
	// iterators (whose snapshots predate it) are still open.
	if v, err := kv.Get("d"); err != nil || v != "vd" {
		t.Errorf("Get(d) after Set = %q, %v; want vd", v, err)
	}
	if err := it2.Close(); err != nil {
		t.Error(err)
	}
	if err := it1.Close(); err != nil {
		t.Error(err)
	}
}
