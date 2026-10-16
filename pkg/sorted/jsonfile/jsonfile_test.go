/*
Copyright 2026 The Perkeep Authors

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

package jsonfile

import (
	"os"
	"path/filepath"
	"testing"

	"go4.org/jsonconfig"
	"perkeep.org/pkg/sorted"
	"perkeep.org/pkg/sorted/kvtest"
)

func TestSorted(t *testing.T) {
	kv, err := Open(filepath.Join(t.TempDir(), "kv.json"))
	if err != nil {
		t.Fatal(err)
	}
	kvtest.TestSorted(t, kv)
}

func TestPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	kv, err := sorted.NewKeyValue(jsonconfig.Obj{"type": "jsonfile", "file": path})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("file exists before any change: %v", err)
	}
	if err := kv.Set("b", "2"); err != nil {
		t.Fatal(err)
	}
	b := kv.BeginBatch()
	b.Set("a", "1")
	b.Set("c", "3")
	b.Delete("b")
	if err := kv.CommitBatch(b); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(data), "{\n\t\"a\": \"1\",\n\t\"c\": \"3\"\n}\n"; got != want {
		t.Errorf("file = %q; want %q", got, want)
	}

	kv2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	if err := sorted.Foreach(kv2, func(k, v string) error {
		got = append(got, k+"="+v)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "a=1" || got[1] != "c=3" {
		t.Errorf("reopened rows = %q; want [a=1 c=3]", got)
	}

	if err := os.WriteFile(path, []byte(`{"checks": {}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path); err == nil {
		t.Errorf("Open of a file that isn't an object of strings succeeded")
	}
}
