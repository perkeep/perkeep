/*
Copyright 2013 The Camlistore Authors

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

// Package kvtest tests sorted.KeyValue implementations.
package kvtest

import (
	"reflect"
	"testing"

	"camlistore.org/pkg/sorted"
	"camlistore.org/pkg/test"
)

func TestSorted(t *testing.T, kv sorted.KeyValue) {
	defer test.TLog(t)()
	if !isEmpty(t, kv) {
		t.Fatal("kv for test is expected to be initially empty")
	}
	set := func(k, v string) {
		if err := kv.Set(k, v); err != nil {
			t.Fatalf("Error setting %q to %q: %v", k, v, err)
		}
	}
	set("foo", "bar")
	if isEmpty(t, kv) {
		t.Fatalf("iterator reports the kv is empty after adding foo=bar; iterator must be broken")
	}
	if v, err := kv.Get("foo"); err != nil || v != "bar" {
		t.Errorf("get(foo) = %q, %v; want bar", v, err)
	}
	if v, err := kv.Get("NOT_EXIST"); err != sorted.ErrNotFound {
		t.Errorf("get(NOT_EXIST) = %q, %v; want error sorted.ErrNotFound", v, err)
	}
	for i := 0; i < 2; i++ {
		if err := kv.Delete("foo"); err != nil {
			t.Errorf("Delete(foo) (on loop %d/2) returned error %v", i+1, err)
		}
	}
	set("a", "av")
	set("b", "bv")
	set("c", "cv")
	testEnumerate(t, kv, "", "", "av", "bv", "cv")
	testEnumerate(t, kv, "a", "", "av", "bv", "cv")
	testEnumerate(t, kv, "b", "", "bv", "cv")
	testEnumerate(t, kv, "a", "c", "av", "bv")
	testEnumerate(t, kv, "a", "b", "av")
	testEnumerate(t, kv, "a", "a")
	testEnumerate(t, kv, "d", "")
	testEnumerate(t, kv, "d", "e")

	// Verify that the value isn't being used instead of the key in the range comparison.
	set("y", "x:foo")
	testEnumerate(t, kv, "x:", "x~")

	// TODO: test batch commits
}

func testEnumerate(t *testing.T, kv sorted.KeyValue, start, end string, want ...string) {
	var got []string
	it := kv.Find(start, end)
	for it.Next() {
		key, val := it.Key(), it.Value()
		keyb, valb := it.KeyBytes(), it.ValueBytes()
		if key != string(keyb) {
			t.Errorf("Key and KeyBytes disagree: %q vs %q", key, keyb)
		}
		if val != string(valb) {
			t.Errorf("Value and ValueBytes disagree: %q vs %q", val, valb)
		}
		if key+"v" != val {
			t.Errorf("iterator returned unexpected pair for test: %q, %q", key, val)
		}
		got = append(got, val)
	}
	err := it.Close()
	if err != nil {
		t.Errorf("for enumerate of (%q, %q), Close error: %v", start, end, err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("for enumerate of (%q, %q), got: %q; want %q", start, end, got, want)
	}
}

func isEmpty(t *testing.T, kv sorted.KeyValue) bool {
	it := kv.Find("", "")
	hasRow := it.Next()
	if err := it.Close(); err != nil {
		t.Fatalf("Error closing iterator while testing for emptiness: %v", err)
	}
	return !hasRow
}
