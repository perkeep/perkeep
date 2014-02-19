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

// Package asserts provides a bad implementation of test predicate
// helpers. This package should either go away or dramatically
// improve.
package asserts

import (
	"strings"
	"testing"
)

// NOTE: THESE FUNCTIONS ARE DEPRECATED. PLEASE DO NOT USE THEM IN
// NEW CODE.

func Expect(t *testing.T, got bool, what string) {
	if !got {
		t.Errorf("%s: got %v; expected %v", what, got, true)
	}
}

func Assert(t *testing.T, got bool, what string) {
	if !got {
		t.Fatalf("%s: got %v; expected %v", what, got, true)
	}
}

func ExpectErrorContains(t *testing.T, err error, substr, msg string) {
	errorContains((*testing.T).Errorf, t, err, substr, msg)
}

func AssertErrorContains(t *testing.T, err error, substr, msg string) {
	errorContains((*testing.T).Fatalf, t, err, substr, msg)
}

func errorContains(f func(*testing.T, string, ...interface{}), t *testing.T, err error, substr, msg string) {
	if err == nil {
		f(t, "%s: got nil error; expected error containing %q", msg, substr)
		return
	}
	if !strings.Contains(err.Error(), substr) {
		f(t, "%s: expected error containing %q; got instead error %q", msg, substr, err.Error())
	}
}

func ExpectString(t *testing.T, expect, got string, what string) {
	if expect != got {
		t.Errorf("%s: got %q; expected %q", what, got, expect)
	}
}

func AssertString(t *testing.T, expect, got string, what string) {
	if expect != got {
		t.Fatalf("%s: got %q; expected %q", what, got, expect)
	}
}

func ExpectBool(t *testing.T, expect, got bool, what string) {
	if expect != got {
		t.Errorf("%s: got %v; expected %v", what, got, expect)
	}
}

func AssertBool(t *testing.T, expect, got bool, what string) {
	if expect != got {
		t.Fatalf("%s: got %v; expected %v", what, got, expect)
	}
}

func ExpectInt(t *testing.T, expect, got int, what string) {
	if expect != got {
		t.Errorf("%s: got %d; expected %d", what, got, expect)
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
	t.Errorf("%s: expected nil; got %v", what, v)
}

func AssertNil(t *testing.T, v interface{}, what string) {
	if v == nil {
		return
	}
	t.Fatalf("%s: expected nil; got %v", what, v)
}
