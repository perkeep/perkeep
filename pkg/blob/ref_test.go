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
	"encoding/json"
	"strings"
	"testing"
)

var parseTests = []struct {
	in  string
	bad bool
}{
	{in: "sha1-0beec7b5ea3f0fdbc95d0dd47f3c5bc275da8a33"},
	{in: "foo-0b"},
	{in: "foo-0b0c"},

	{in: "/camli/sha1-0beec7b5ea3f0fdbc95d0dd47f3c5bc275da8a33", bad: true},
	{in: "", bad: true},
	{in: "foo", bad: true},
	{in: "-0f", bad: true},
	{in: "sha1-xx", bad: true},
	{in: "-", bad: true},
	{in: "sha1-0b", bad: true},

	// TODO: renable this later, once we clean all tests:
	//{in: "foo-0b0cd", bad: true}, // odd number
	{in: "foo-abc"}, // accepted for now. will delete later.
}

func TestParse(t *testing.T) {
	for _, tt := range parseTests {
		r, ok := Parse(tt.in)
		if r.Valid() != ok {
			t.Errorf("Valid != ok for %q", tt.in)
		}
		if ok && tt.bad {
			t.Errorf("Parse(%q) didn't fail. It should've.", tt.in)
			continue
		}
		if !ok {
			if !tt.bad {
				t.Errorf("Parse(%q) failed to parse", tt.in)
				continue
			}
			continue
		}
		str := r.String()
		if str != tt.in {
			t.Errorf("Parsed %q but String() value differs: %q", tt.in, str)
		}
		wantDig := str[strings.Index(str, "-")+1:]
		if dig := r.Digest(); dig != wantDig {
			t.Errorf("Digest(%q) = %q; want %q", tt.in, dig, wantDig)
		}
		_ = r == r // test that concrete type of r supports equality
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

func TestSum32(t *testing.T) {
	got := MustParse("sha1-1234567800000000000000000000000000000000").Sum32()
	want := uint32(0x12345678)
	if got != want {
		t.Errorf("Sum32 = %x, want %x", got, want)
	}
}

func TestSum64(t *testing.T) {
	got := MustParse("sha1-12345678876543210000000000000000000000ff").Sum64()
	want := uint64(0x1234567887654321)
	if got != want {
		t.Errorf("Sum64 = %x, want %x", got, want)
	}
}

type Foo struct {
	B Ref `json:"foo"`
}

func TestJSONUnmarshal(t *testing.T) {
	var f Foo
	if err := json.Unmarshal([]byte(`{"foo": "abc-def123", "other": 123}`), &f); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if !f.B.Valid() {
		t.Fatal("blobref is nil")
	}
	if g, e := f.B.String(), "abc-def123"; g != e {
		t.Errorf("got %q, want %q", g, e)
	}
}

func TestJSONMarshal(t *testing.T) {
	f := &Foo{B: MustParse("def-1234abcd")}
	bs, err := json.Marshal(f)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if g, e := string(bs), `{"foo":"def-1234abcd"}`; g != e {
		t.Errorf("got %q, want %q", g, e)
	}
}

func TestSizedBlobRefString(t *testing.T) {
	sr := SizedRef{Ref: MustParse("abc-1234"), Size: 456}
	want := "[abc-1234; 456 bytes]"
	if got := sr.String(); got != want {
		t.Errorf("SizedRef.String() = %q, want %q", got, want)
	}
}

func TestMarshalBinary(t *testing.T) {
	br := MustParse("abc-00ff4869")
	data, _ := br.MarshalBinary()
	if got, want := string(data), "abc-\x00\xffHi"; got != want {
		t.Fatalf("MarshalBinary = %q; want %q", got, want)
	}
	br2 := new(Ref)
	if err := br2.UnmarshalBinary(data); err != nil {
		t.Fatal(err)
	}
	if *br2 != br {
		t.Error("UnmarshalBinary result != original")
	}

	if err := br2.UnmarshalBinary(data); err == nil {
		t.Error("expect error on second UnmarshalBinary")
	}
}
