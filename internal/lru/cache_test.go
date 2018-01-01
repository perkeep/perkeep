/*
Copyright 2011 Google Inc.

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

package lru

import (
	"reflect"
	"testing"
)

func TestLRU(t *testing.T) {
	c := New(2)

	expectMiss := func(k string) {
		v, ok := c.Get(k)
		if ok {
			t.Fatalf("expected cache miss on key %q but hit value %v", k, v)
		}
	}

	expectHit := func(k string, ev interface{}) {
		v, ok := c.Get(k)
		if !ok {
			t.Fatalf("expected cache(%q)=%v; but missed", k, ev)
		}
		if !reflect.DeepEqual(v, ev) {
			t.Fatalf("expected cache(%q)=%v; but got %v", k, ev, v)
		}
	}

	expectMiss("1")
	c.Add("1", "one")
	expectHit("1", "one")

	c.Add("2", "two")
	expectHit("1", "one")
	expectHit("2", "two")

	c.Add("3", "three")
	expectHit("3", "three")
	expectHit("2", "two")
	expectMiss("1")
}

func TestRemoveOldest(t *testing.T) {
	c := New(2)
	c.Add("1", "one")
	c.Add("2", "two")
	if k, v := c.RemoveOldest(); k != "1" || v != "one" {
		t.Fatalf("oldest = %q, %q; want 1, one", k, v)
	}
	if k, v := c.RemoveOldest(); k != "2" || v != "two" {
		t.Fatalf("oldest = %q, %q; want 2, two", k, v)
	}
	if k, v := c.RemoveOldest(); k != "" || v != nil {
		t.Fatalf("oldest = %v, %v; want \"\", nil", k, v)
	}
}
