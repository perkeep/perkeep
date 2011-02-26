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

package testing

import (
	"testing"
)

func Expect(t *testing.T, got bool, what string) {
	if !got {
		t.Errorf("%s: got %v; expected %v", what, got, true)
	}
}

func ExpectBool(t *testing.T, expect, got bool, what string) {
	if expect != got {
		t.Errorf("%s: got %v; expected %v", what, got, expect)
	}
}

func ExpectInt(t *testing.T, expect, got int, what string) {
	if expect != got {
		t.Errorf("%s: got %d; expected %d", what, got, expect)
	}
}

func Assert(t *testing.T, got bool, what string) {
	if !got {
		t.Fatalf("%s: got %v; expected %v", what, got, true)
	}
}

func AssertBool(t *testing.T, expect, got bool, what string) {
	if expect != got {
		t.Fatalf("%s: got %v; expected %v", what, got, expect)
	}
}

func AssertInt(t *testing.T, expect, got int, what string) {
	if expect != got {
		t.Fatalf("%s: got %d; expected %d", what, got, expect)
	}
}

func ExpectNil(t *testing.T, v interface{}, what string) {
	if v == nil {
		return
	}
	t.Errorf("%s: expected nil; got %v", v)
}

func AssertNil(t *testing.T, v interface{}, what string) {
	if v == nil {
		return
	}
	t.Fatalf("%s: expected nil; got %v", v)
}

