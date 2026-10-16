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

package index

import (
	"strings"
	"testing"
	"unsafe"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/types/camtypes"
)

// Ref contains an interface whose data word points at the boxed immutable
// digest. Value equality alone cannot test whether Corpus interning worked.
// This test inspects the current Ref representation, never
// dereferences the pointer, and must be updated if that representation changes.
func refBacking(t testing.TB, r blob.Ref) unsafe.Pointer {
	t.Helper()
	if unsafe.Sizeof(r) != unsafe.Sizeof([2]unsafe.Pointer{}) {
		t.Fatal("update backing-identity test for the new blob.Ref representation")
	}
	return (*[2]unsafe.Pointer)(unsafe.Pointer(&r))[1]
}

func requireCanonicalRef(t testing.TB, got, want blob.Ref) {
	t.Helper()
	if got != want || refBacking(t, got) != refBacking(t, want) {
		t.Fatalf("reference %v does not share the canonical backing for %v", got, want)
	}
}

func TestCorpusBlobParse(t *testing.T) {
	for _, text := range []string{
		"sha1-0beec7b5ea3f0fdbc95d0dd47f3c5bc275da8a33",
		"sha224-d14a028c2a3a2bc9476102bb288234c415a2b01f828ea62ac5b3e42f",
		"sha256-b5bb9d8014a0f9b1d61e21e796d78dccdf1352f23cd32812f4850b878ae4944c",
		"future-abc",
	} {
		t.Run(text, func(t *testing.T) {
			c := newLoggingCorpus(t)
			canonical := blob.MustParse(text)
			if err := c.mergeBlobMeta(camtypes.BlobMeta{Ref: canonical, Size: 1234}); err != nil {
				t.Fatal(err)
			}
			got, ok := c.blobParse([]byte(text))
			if !ok {
				t.Fatal("byte miss rejected valid ref")
			}
			requireCanonicalRef(t, got, canonical)
			got, ok = c.blobParseString(text)
			if !ok {
				t.Fatal("string hit rejected valid ref")
			}
			requireCanonicalRef(t, got, canonical)
			input := []byte(text)
			if n := testing.AllocsPerRun(100, func() { c.blobParse(input) }); n != 0 {
				t.Errorf("byte hits allocate %v; want zero", n)
			}
			if n := testing.AllocsPerRun(100, func() { c.blobParseString(text) }); n != 0 {
				t.Errorf("string hits allocate %v; want zero", n)
			}
		})
	}
	c := newLoggingCorpus(t)
	for _, s := range []string{"", "not-a-ref", "sha224-123", "foo-zz"} {
		if br, ok := c.blobParseString(s); ok || br.Valid() {
			t.Fatalf("accepted invalid %q", s)
		}
		if br, ok := c.blobParse([]byte(s)); ok || br.Valid() {
			t.Fatalf("accepted invalid bytes %q", s)
		}
	}
	if len(c.brOfStr) != 0 {
		t.Fatal("invalid refs were cached")
	}
}

func TestCorpusBlobParseInputOwnership(t *testing.T) {
	text := blob.RefFromString("ownership").String()
	c := newLoggingCorpus(t)
	input := []byte(text)
	first, ok := c.blobParse(input)
	if !ok {
		t.Fatal("parse")
	}
	input[0] = 'x'
	got, ok := c.blobParseString(text)
	if !ok {
		t.Fatal("reused bytes corrupted cached key")
	}
	requireCanonicalRef(t, got, first)

	c = newLoggingCorpus(t)
	row := strings.Repeat("prefix", 1000) + text + strings.Repeat("suffix", 1000)
	substring := row[6000 : 6000+len(text)]
	first, ok = c.blobParseString(substring)
	if !ok {
		t.Fatal("parse substring")
	}
	for key := range c.brOfStr {
		if unsafe.StringData(key) == unsafe.StringData(substring) {
			t.Fatal("string cache miss retained the larger row backing")
		}
	}
	got, ok = c.blobParse([]byte(text))
	if !ok {
		t.Fatal("cross-format cache hit")
	}
	requireCanonicalRef(t, got, first)
}

func TestCorpusBlobParseMetadataLate(t *testing.T) {
	c := newLoggingCorpus(t)
	text := blob.RefFromString("late").String()
	old, ok := c.blobParseString(text)
	if !ok {
		t.Fatal("parse")
	}
	canonical := blob.MustParse(text)
	if refBacking(t, old) == refBacking(t, canonical) {
		t.Fatal("test refs already share backing")
	}
	if err := c.mergeBlobMeta(camtypes.BlobMeta{Ref: canonical, Size: 1234}); err != nil {
		t.Fatal(err)
	}
	if c.brOfStr != nil {
		t.Fatal("metadata insertion did not invalidate stale cache")
	}
	got, ok := c.blobParse([]byte(text))
	if !ok {
		t.Fatal("parse after metadata")
	}
	requireCanonicalRef(t, got, canonical)
	got, ok = c.blobParseString(text)
	if !ok {
		t.Fatal("hit after metadata")
	}
	requireCanonicalRef(t, got, canonical)
}
