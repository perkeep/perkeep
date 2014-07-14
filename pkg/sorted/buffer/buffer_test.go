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

package buffer

import (
	"testing"

	"camlistore.org/pkg/sorted"
)

// TODO(adg): test batch mutations
// TODO(adg): test auto-flush behavior

func TestBuffer(t *testing.T) {
	var (
		toBack = []mod{
			{false, "b", "b1"},
			{false, "d", "d1"},
			{false, "f", "f1"},
		}
		toBuf = []mod{
			{false, "a", "a2"},
			{false, "b", "b2"},
			{false, "c", "c2"},
			{false, "e", "e2"},
			{true, "f", ""},
			{false, "g", "g2"},
		}
		backBeforeFlush = []mod{
			{false, "b", "b1"},
			{false, "d", "d1"},
			// f deleted
		}
		want = []mod{
			{false, "a", "a2"},
			{false, "b", "b2"},
			{false, "c", "c2"},
			{false, "d", "d1"},
			{false, "e", "e2"},
			// f deleted
			{false, "g", "g2"},
		}
	)

	// Populate backing storage.
	backing := sorted.NewMemoryKeyValue()
	for _, m := range toBack {
		backing.Set(m.key, m.value)
	}
	// Wrap with buffered storage, populate.
	buf := New(sorted.NewMemoryKeyValue(), backing, 1<<20)
	for _, m := range toBuf {
		if m.isDelete {
			buf.Delete(m.key)
		} else {
			buf.Set(m.key, m.value)
		}
	}

	// Check contents of buffered storage.
	check(t, buf, "buffered", want)
	check(t, backing, "backing before flush", backBeforeFlush)

	// Flush.
	if err := buf.Flush(); err != nil {
		t.Fatal("flush error: ", err)
	}

	// Check contents of backing storage.
	check(t, backing, "backing after flush", want)
}

func check(t *testing.T, kv sorted.KeyValue, prefix string, want []mod) {
	it := kv.Find("", "")
	for i, m := range want {
		if !it.Next() {
			t.Fatalf("%v: unexpected it.Next == false on iteration %d", prefix, i)
		}
		if k, v := it.Key(), it.Value(); k != m.key || v != m.value {
			t.Errorf("%v: got key == %q value == %q, want key == %q value == %q on iteration %d",
				prefix, k, v, m.key, m.value, i)
		}
	}
	if it.Next() {
		t.Errorf("%v: unexpected it.Next == true after complete iteration", prefix)
	}
	if err := it.Close(); err != nil {
		t.Errorf("%v: error closing iterator: %v", prefix, err)
	}
}
