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

package blobref

import (
	"testing"
)

func TestAll(t *testing.T) {
 	br := Parse("sha1-0beec7b5ea3f0fdbc95d0dd47f3c5bc275da8a33")
	if br == nil {
		t.Fatalf("Failed to parse blobref")
	}
	if br.hashName != "sha1" {
		t.Errorf("Expected sha1 hashName")
	}
	if br.digest != "0beec7b5ea3f0fdbc95d0dd47f3c5bc275da8a33" {
		t.Errorf("Invalid digest")
	}
	if !br.IsSupported() {
		t.Errorf("sha1 should be supported")
	}

	hash := br.Hash()
	hash.Write([]byte("foo"))
	if !br.HashMatches(hash) {
		t.Errorf("Expected hash of bytes 'foo' to match")
	}
	hash.Write([]byte("bogusextra"))
	if br.HashMatches(hash) {
		t.Errorf("Unexpected hash match with bogus extra bytes")
	}
}

func TestNotSupported(t *testing.T) {
 	br := Parse("unknownfunc-0beec7b5ea3f0fdbc95d0dd47f3c5bc275da8a33")
	if br == nil {
		t.Fatalf("Failed to parse blobref")
	}
	if br.IsSupported() {
		t.Fatalf("Unexpected IsSupported() on unknownfunc")
	}
}
