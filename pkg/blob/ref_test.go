/*
Copyright 2013 Google Inc.

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

package blob

import (
	"testing"
)

func TestParse(t *testing.T) {
	in := "sha1-0beec7b5ea3f0fdbc95d0dd47f3c5bc275da8a33"
	r, ok := Parse(in)
	if !ok {
		t.Fatal("failed to parse")
	}
	if !r.Valid() {
		t.Error("not Valid")
	}
	got := r.String()
	if got != in {
		t.Errorf("parse(%q).String = %q; want same input", in, got)
	}
}

func TestEquality(t *testing.T) {
	in := "sha1-0beec7b5ea3f0fdbc95d0dd47f3c5bc275da8a33"
	in3 := "sha1-ffffffffffffffffffffffffffffffffffffffff"
	r := ParseOrZero(in)
	r2 := ParseOrZero(in)
	r3 := ParseOrZero(in3)
	if !r.Valid() || !r2.Valid() || !r3.Valid() {
		t.Fatal("not valid")
	}
	if r != r2 {
		t.Errorf("r and r2 should be equal")
	}
	if r == r3 {
		t.Errorf("r and r3 should not be equal")
	}
}
