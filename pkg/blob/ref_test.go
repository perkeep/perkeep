/*
Copyright 2013 The Perkeep Authors

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
	"reflect"
	"strings"
	"testing"
)

var parseFn = map[string]func(string) (Ref, bool){
	"Parse":      Parse,
	"ParseKnown": ParseKnown,
}

var parseTests = []struct {
	in        string
	bad       bool
	fn        string
	skipBytes bool // skip ParseBytes test
}{
	{in: "sha1-0beec7b5ea3f0fdbc95d0dd47f3c5bc275da8a33"},
	{in: "sha224-d14a028c2a3a2bc9476102bb288234c415a2b01f828ea62ac5b3e42f"},
	{in: "foo-0b"},
	{in: "foo-0b0c"},

	{in: "sha1-0beec7b5ea3f0fdbc95d0dd47f3c5bc275da8a34", fn: "ParseKnown"},
	{in: "foo-0b0c", bad: true, skipBytes: true, fn: "ParseKnown"},
	{in: "perma-1243", fn: "ParseKnown"},
	{in: "fakeref-0012", fn: "ParseKnown"},

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
		fn := tt.fn
		if fn == "" {
			fn = "Parse"
		}
		r, ok := parseFn[fn](tt.in)
		if r.Valid() != ok {
			t.Errorf("Valid != ok for %q", tt.in)
		}
		if ok && tt.bad {
			t.Errorf("%s(%q) didn't fail. It should've.", fn, tt.in)
			continue
		}
		if !ok {
			if !tt.bad {
				t.Errorf("%s(%q) failed to parse", fn, tt.in)
				continue
			}
			continue
		}
		if !tt.skipBytes {
			r2, ok := ParseBytes([]byte(tt.in))
			if r != r2 {
				t.Errorf("ParseBytes(%q) = %v, %v; want %v", tt.in, r2, ok, r)
			}
		}
		str := r.String()
		if str != tt.in {
			t.Errorf("Parsed %q but String() value differs: %q", tt.in, str)
		}
		wantDig := str[strings.Index(str, "-")+1:]
		if dig := r.Digest(); dig != wantDig {
			t.Errorf("Digest(%q) = %q; want %q", tt.in, dig, wantDig)
		}
		if !reflect.TypeOf(r).Comparable() {
			t.Errorf("%T is not comparable", r)
		}
		r2 := r
		_ = r == r2 // test that concrete type of r supports equality
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

	f = Foo{}
	if err := json.Unmarshal([]byte(`{}`), &f); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if f.B.Valid() {
		t.Fatal("blobref is valid and shouldn't be")
	}

	f = Foo{}
	if err := json.Unmarshal([]byte(`{"foo":null}`), &f); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if f.B.Valid() {
		t.Fatal("blobref is valid and shouldn't be")
	}
}

func TestJSONMarshal(t *testing.T) {
	f := &Foo{}
	bs, err := json.Marshal(f)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if g, e := string(bs), `{"foo":null}`; g != e {
		t.Errorf("got %q, want %q", g, e)
	}

	f = &Foo{B: MustParse("def-1234abcd")}
	bs, err = json.Marshal(f)
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

func TestRefStringMinusOne(t *testing.T) {
	br := MustParse("abc-1234")
	want := "abc-1233"
	if got := br.StringMinusOne(); got != want {
		t.Errorf("StringMinusOne = %q; want %q", got, want)
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

func BenchmarkParseBlob(b *testing.B) {
	b.ReportAllocs()
	ref := "sha1-0beec7b5ea3f0fdbc95d0dd47f3c5bc275da8a33"
	refb := []byte(ref)
	for i := 0; i < b.N; i++ {
		if _, ok := Parse(ref); !ok {
			b.FailNow()
		}
		if _, ok := ParseBytes(refb); !ok {
			b.FailNow()
		}
	}
}

func TestJSONUnmarshalSized(t *testing.T) {
	var sb SizedRef
	if err := json.Unmarshal([]byte(`{
		"blobRef": "sha1-ce284c167558a9ef22df04390c87a6d0c9ed9659",
		"size": 123}`), &sb); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	want := SizedRef{
		Ref:  MustParse("sha1-ce284c167558a9ef22df04390c87a6d0c9ed9659"),
		Size: 123,
	}
	if sb != want {
		t.Fatalf("got %q, want %q", sb, want)
	}

	sb = SizedRef{}
	if err := json.Unmarshal([]byte(`{}`), &sb); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if sb.Valid() {
		t.Fatal("sized blobref is valid and shouldn't be")
	}

	sb = SizedRef{}
	if err := json.Unmarshal([]byte(`{"blobRef":null, "size": 456}`), &sb); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if sb.Valid() {
		t.Fatal("sized blobref is valid and shouldn't be")
	}
}

func TestJSONMarshalSized(t *testing.T) {
	sb := SizedRef{
		Ref:  MustParse("sha1-ce284c167558a9ef22df04390c87a6d0c9ed9659"),
		Size: 123,
	}
	b, err := json.Marshal(sb)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if g, e := string(b), `{"blobRef":"sha1-ce284c167558a9ef22df04390c87a6d0c9ed9659","size":123}`; g != e {
		t.Fatalf("got %q, want %q", g, e)
	}

	sb = SizedRef{}
	b, err = json.Marshal(sb)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if g, e := string(b), `{"blobRef":null,"size":0}`; g != e {
		t.Fatalf("got %q, want %q", g, e)
	}
}

var equalStringTests = []struct {
	ref  Ref
	str  string
	want bool
}{
	{MustParse("sha1-ce284c167558a9ef22df04390c87a6d0c9ed9659"), "sha1-ce284c167558a9ef22df04390c87a6d0c9ed9659", true},
	// last digit wrong:
	{MustParse("sha1-ce284c167558a9ef22df04390c87a6d0c9ed9659"), "sha1-ce284c167558a9ef22df04390c87a6d0c9ed9658", false},
	// second to last digit wrong:
	{MustParse("sha1-ce284c167558a9ef22df04390c87a6d0c9ed9659"), "sha1-ce284c167558a9ef22df04390c87a6d0c9ed9669", false},
	// hyphen wrong:
	{MustParse("sha1-ce284c167558a9ef22df04390c87a6d0c9ed9659"), "sha1xce284c167558a9ef22df04390c87a6d0c9ed9659", false},
	// truncated:
	{MustParse("sha1-ce284c167558a9ef22df04390c87a6d0c9ed9659"), "sha1-", false},
	{MustParse("sha1-ce284c167558a9ef22df04390c87a6d0c9ed9659"), "sha1", false},
	{MustParse("sha1-ce284c167558a9ef22df04390c87a6d0c9ed9659"), "", false},
	// right length, wrong hash:
	{MustParse("sha1-ce284c167558a9ef22df04390c87a6d0c9ed9659"), "sha2-ce284c167558a9ef22df04390c87a6d0c9ed9659", false},

	// Other hashes:
	{MustParse("foo-cafe"), "foo-cafe", true},
	{MustParse("foo-caf"), "foo-caf", true},

	{MustParse("foo-cafe"), "foo-beef", false},
	{MustParse("foo-cafe"), "bar-cafe", false},
	{MustParse("foo-cafe"), "fooxbeef", false},
	{MustParse("foo-caf"), "foo-cae", false},
	{MustParse("foo-caf"), "foo-ca", false},

	{MustParse("sha224-d14a028c2a3a2bc9476102bb288234c415a2b01f828ea62ac5b3e42f"), "sha224-d14a028c2a3a2bc9476102bb288234c415a2b01f828ea62ac5b3e42f", true},
	{MustParse("sha224-d14a028c2a3a2bc9476102bb288234c415a2b01f828ea62ac5b3e42f"), "sha224-d14a028c2a3a2bc9476102bb288234c415a2b01f828ea62ac5b3e42g", false}, // last byte wrong
	{MustParse("sha224-d14a028c2a3a2bc9476102bb288234c415a2b01f828ea62ac5b3e42f"), "sha224-d14a028c2a3a2bc9476102bb288234c415a2b01f828ea62ac5b3e42e", false}, // last byte wrong
}

func TestEqualString(t *testing.T) {
	for _, tt := range equalStringTests {
		got := tt.ref.EqualString(tt.str)
		if got != tt.want {
			t.Errorf("ref %q EqualString(%q) = %v; want %v", tt.ref, tt.str, got, tt.want)
		}
	}
}

func BenchmarkEqualString(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		for _, tt := range equalStringTests {
			got := tt.ref.EqualString(tt.str)
			if got != tt.want {
				b.Fatalf("ref %q EqualString(%q) = %v; want %v", tt.ref, tt.str, got, tt.want)
			}
		}
	}
}

var hasPrefixTests = []struct {
	ref  Ref
	str  string
	want bool
}{
	{MustParse("sha1-ce284c167558a9ef22df04390c87a6d0c9ed9659"), "sha1-ce284c167558a9ef22df04390c87a6d0c9ed9659", true},
	{MustParse("sha1-ce284c167558a9ef22df04390c87a6d0c9ed9659"), "sha1-ce284c167558a9ef22df04390c87a6d0c9ed", true},
	{MustParse("sha1-ce284c167558a9ef22df04390c87a6d0c9ed9659"), "sha1-ce284c167558a9ef22df04390c87a6d0c9e", true},
	// last digit wrong:
	{MustParse("sha1-ce284c167558a9ef22df04390c87a6d0c9ed9659"), "sha1-ce284c167558a9ef22df04390c87a6d0c9ee", false},
	// second to last digit wrong:
	{MustParse("sha1-ce284c167558a9ef22df04390c87a6d0c9ed9659"), "sha1-ce284c167558a9ef22df04390c87a6d0c9f", false},
	// hyphen wrong:
	{MustParse("sha1-ce284c167558a9ef22df04390c87a6d0c9ed9659"), "sha1xce284c167558a9ef22df04390c87a6d0c9ed", false},
	// truncated:
	{MustParse("sha1-ce284c167558a9ef22df04390c87a6d0c9ed9659"), "sha1-c", true},
	{MustParse("sha1-ce284c167558a9ef22df04390c87a6d0c9ed9659"), "sha1-", false},
	{MustParse("sha1-ce284c167558a9ef22df04390c87a6d0c9ed9659"), "sha1", false},
	{MustParse("sha1-ce284c167558a9ef22df04390c87a6d0c9ed9659"), "", false},
	// wrong hash:
	{MustParse("sha1-ce284c167558a9ef22df04390c87a6d0c9ed9659"), "sha2-ce284c167558a9ef22df04390c87a6d0c9ed96", false},

	// Other hashes:
	{MustParse("foo-cafe"), "foo-cafe", true},
	{MustParse("foo-cafe"), "foo-caf", true},
	{MustParse("foo-cafe"), "foo-ca", true},
	{MustParse("foo-cafe"), "foo-c", true},

	{MustParse("foo-cafe"), "foo-", false},
	{MustParse("foo-cafe"), "", false},
	{MustParse("foo-cafe"), "foo-beef", false},
	{MustParse("foo-cafe"), "foo-bee", false},
	{MustParse("foo-cafe"), "bar-cafe", false},
	{MustParse("foo-cafe"), "fooxbe", false},
	{MustParse("foo-cafe"), "foo-c", true},
	{MustParse("foo-caf"), "foo-cae", false},
	{MustParse("foo-caf"), "foo-cb", false},

	{MustParse("sha224-d14a028c2a3a2bc9476102bb288234c415a2b01f828ea62ac5b3e42f"), "sha224-d14a", true},
	{MustParse("sha224-d14a028c2a3a2bc9476102bb288234c415a2b01f828ea62ac5b3e42f"), "sha224-d14b", false},
	{MustParse("sha224-d14a028c2a3a2bc9476102bb288234c415a2b01f828ea62ac5b3e42f"), "sha224-d14", true},
	{MustParse("sha224-d14a028c2a3a2bc9476102bb288234c415a2b01f828ea62ac5b3e42f"), "sha224-d15", false},
	{MustParse("sha224-d14a028c2a3a2bc9476102bb288234c415a2b01f828ea62ac5b3e42f"), "sha224-", false}, // TODO: make this true?
}

func TestHasPrefix(t *testing.T) {
	for _, tt := range hasPrefixTests {
		got := tt.ref.HasPrefix(tt.str)
		if got != tt.want {
			t.Errorf("ref %q HasPrefix(%q) = %v; want %v", tt.ref, tt.str, got, tt.want)
		}
	}
}

func BenchmarkHasPrefix(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		for _, tt := range hasPrefixTests {
			got := tt.ref.HasPrefix(tt.str)
			if got != tt.want {
				b.Fatalf("ref %q HasPrefix(%q) = %v; want %v", tt.ref, tt.str, got, tt.want)
			}
		}
	}
}
