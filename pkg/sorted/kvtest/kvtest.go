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

	// Verify that < comparison works identically for all DBs (because it is affected by collation rules)
	// http://postgresql.1045698.n5.nabble.com/String-comparison-and-the-SQL-standard-td5740721.html
	set("foo|abc", "foo|abcv")
	testEnumerate(t, kv, "foo|", "", "foo|abcv")
	testEnumerate(t, kv, "foo|", "foo}", "foo|abcv")

	// Verify that the value isn't being used instead of the key in the range comparison.
	set("y", "x:foo")
	testEnumerate(t, kv, "x:", "x~")

	testInsertLarge(t, kv)
	testInsertTooLarge(t, kv)

	// TODO: test batch commits
}

func testInsertLarge(t *testing.T, kv sorted.KeyValue) {
	largeKey := make([]byte, sorted.MaxKeySize-1)
	// setting all the bytes because postgres whines about an invalid byte sequence
	// otherwise
	for k, _ := range largeKey {
		largeKey[k] = 'A'
	}
	largeKey[sorted.MaxKeySize-2] = 'B'
	largeValue := make([]byte, sorted.MaxValueSize-1)
	for k, _ := range largeValue {
		largeValue[k] = 'A'
	}
	largeValue[sorted.MaxValueSize-2] = 'B'

	// insert with large key
	if err := kv.Set(string(largeKey), "whatever"); err != nil {
		t.Fatalf("Insertion of large key failed: %v", err)
	}

	// and verify we can get it back, i.e. that the key hasn't been truncated.
	it := kv.Find(string(largeKey), "")
	if !it.Next() || it.Key() != string(largeKey) || it.Value() != "whatever" {
		it.Close()
		t.Fatalf("Find(largeKey) = %q, %q; want %q, %q", it.Key(), it.Value(), largeKey, "whatever")
	}
	it.Close()

	// insert with large value
	if err := kv.Set("whatever", string(largeValue)); err != nil {
		t.Fatalf("Insertion of large value failed: %v", err)
	}
	// and verify we can get it back, i.e. that the value hasn't been truncated.
	if v, err := kv.Get("whatever"); err != nil || v != string(largeValue) {
		t.Fatalf("get(\"whatever\") = %q, %v; want %q", v, err, largeValue)
	}

	// insert with large key and large value
	if err := kv.Set(string(largeKey), string(largeValue)); err != nil {
		t.Fatalf("Insertion of large key and value failed: %v", err)
	}
	// and verify we can get them back
	it = kv.Find(string(largeKey), "")
	defer it.Close()
	if !it.Next() || it.Key() != string(largeKey) || it.Value() != string(largeValue) {
		t.Fatalf("Find(largeKey) = %q, %q; want %q, %q", it.Key(), it.Value(), largeKey, largeValue)
	}
}

func testInsertTooLarge(t *testing.T, kv sorted.KeyValue) {
	largeKey := make([]byte, sorted.MaxKeySize+1)
	largeValue := make([]byte, sorted.MaxValueSize+1)
	if err := kv.Set(string(largeKey), "whatever"); err == nil || err != sorted.ErrKeyTooLarge {
		t.Fatalf("Insertion of too large a key should have failed, but err was %v", err)
	}
	if err := kv.Set("whatever", string(largeValue)); err == nil || err != sorted.ErrValueTooLarge {
		t.Fatalf("Insertion of too large a value should have failed, but err was %v", err)
	}
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
