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

package main

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"perkeep.org/pkg/sorted"
	"perkeep.org/pkg/sorted/leveldb"
	"perkeep.org/pkg/sorted/sqlite"
)

// newSource returns the spec of a new leveldb store holding rows, which
// include bytes that need quoting.
func newSource(t *testing.T, rows map[string]string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "src.leveldb")
	kv, err := leveldb.NewStorage(path)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range rows {
		if err := kv.Set(k, v); err != nil {
			t.Fatal(err)
		}
	}
	if err := kv.Close(); err != nil {
		t.Fatal(err)
	}
	return "leveldb:" + path
}

var testRows = map[string]string{
	"a":              "1",
	"b:x":            "two words",
	"b:y":            "line\nbreak\x00\xff",
	"b;":             "",
	"c|\"quoted\"|k": "v",
}

func runKVCopy(t *testing.T, args ...string) (stdout string, err error) {
	t.Helper()
	var out, errOut strings.Builder
	err = run(args, &out, &errOut)
	t.Logf("stderr: %s", errOut.String())
	return out.String(), err
}

func TestDump(t *testing.T) {
	src := newSource(t, testRows)
	for _, tc := range []struct {
		args []string
		want []string // keys, in order
	}{
		{nil, []string{"a", "b:x", "b:y", "b;", "c|\"quoted\"|k"}},
		{[]string{"-start=b:", "-end=b;"}, []string{"b:x", "b:y"}},
		{[]string{"-start=b"}, []string{"b:x", "b:y", "b;", "c|\"quoted\"|k"}},
		{[]string{"-limit=2"}, []string{"a", "b:x"}},
		{[]string{"-start=b", "-limit=1"}, []string{"b:x"}},
	} {
		t.Run(strings.Join(tc.args, ","), func(t *testing.T) {
			got, err := runKVCopy(t, append(tc.args, src)...)
			if err != nil {
				t.Fatal(err)
			}
			var want strings.Builder
			for _, k := range tc.want {
				fmt.Fprintf(&want, "%q %q\n", k, testRows[k])
			}
			if got != want.String() {
				t.Errorf("got:\n%s\nwant:\n%s", got, want.String())
			}
		})
	}
}

func TestCopy(t *testing.T) {
	src := newSource(t, testRows)
	for _, tc := range []struct {
		name string
		args []string
		want []string // keys copied
	}{
		{"all", nil, []string{"a", "b:x", "b:y", "b;", "c|\"quoted\"|k"}},
		{"range", []string{"-start=b:", "-end=b;"}, []string{"b:x", "b:y"}},
		{"limit", []string{"-start=b", "-limit=3"}, []string{"b:x", "b:y", "b;"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dstPath := filepath.Join(t.TempDir(), "dst.sqlite")
			if _, err := runKVCopy(t, append(tc.args, src, "sqlite:"+dstPath)...); err != nil {
				t.Fatal(err)
			}
			dst, err := sqlite.NewStorage(dstPath)
			if err != nil {
				t.Fatal(err)
			}
			defer dst.Close()
			got := map[string]string{}
			if err := sorted.Foreach(dst, func(k, v string) error {
				got[k] = v
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if len(got) != len(tc.want) {
				t.Errorf("destination has %d rows; want %d: %q", len(got), len(tc.want), got)
			}
			for _, k := range tc.want {
				if v, ok := got[k]; !ok || v != testRows[k] {
					t.Errorf("destination[%q] = %q, %v; want %q", k, v, ok, testRows[k])
				}
			}
		})
	}
}

func TestJSONConfig(t *testing.T) {
	src := newSource(t, testRows)
	path := strings.TrimPrefix(src, "leveldb:")
	got, err := runKVCopy(t, "-limit=1", fmt.Sprintf(`{"type": "leveldb", "file": %q}`, path))
	if err != nil {
		t.Fatal(err)
	}
	if want := fmt.Sprintf("%q %q\n", "a", testRows["a"]); got != want {
		t.Errorf("got %q; want %q", got, want)
	}
}

func TestCopyRefusesNonEmptyDestination(t *testing.T) {
	src := newSource(t, testRows)
	dstPath := filepath.Join(t.TempDir(), "dst.sqlite")
	dst, err := sqlite.NewStorage(dstPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := dst.Set("existing", "row"); err != nil {
		t.Fatal(err)
	}
	if err := dst.Close(); err != nil {
		t.Fatal(err)
	}
	_, err = runKVCopy(t, src, "sqlite:"+dstPath)
	if err == nil || !strings.Contains(err.Error(), "not empty") {
		t.Fatalf("copy into non-empty destination = %v; want not-empty error", err)
	}
	dst, err = sqlite.NewStorage(dstPath)
	if err != nil {
		t.Fatal(err)
	}
	defer dst.Close()
	if _, err := dst.Get("a"); err != sorted.ErrNotFound {
		t.Errorf("Get(a) on refused destination = %v; want ErrNotFound", err)
	}
}

func TestBadArgs(t *testing.T) {
	src := newSource(t, testRows)
	for _, args := range [][]string{
		{},
		{src, src},
		{src, "sqlite:/x", "extra"},
		{"-limit=-1", src},
		{"nocolon"},
		{"bogustype:/x"},
		{"{not json"},
	} {
		if _, err := runKVCopy(t, args...); err == nil {
			t.Errorf("run(%q) succeeded; want error", args)
		}
	}
}
