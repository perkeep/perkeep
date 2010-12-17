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
