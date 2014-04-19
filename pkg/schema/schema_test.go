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

package schema

import (
	"encoding/json"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"camlistore.org/pkg/blob"
	. "camlistore.org/pkg/test/asserts"
)

const kExpectedHeader = `{"camliVersion"`

func TestJSON(t *testing.T) {
	fileName := "schema_test.go"
	fi, _ := os.Lstat(fileName)
	m := NewCommonFileMap(fileName, fi)
	json, err := m.JSON()
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	t.Logf("Got json: [%s]\n", json)
	// TODO: test it parses back

	if !strings.HasPrefix(json, kExpectedHeader) {
		t.Errorf("JSON does't start with expected header.")
	}

}

func TestRegularFile(t *testing.T) {
	fileName := "schema_test.go"
	fi, err := os.Lstat(fileName)
	AssertNil(t, err, "schema_test.go stat")
	m := NewCommonFileMap("schema_test.go", fi)
	json, err := m.JSON()
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	t.Logf("Got json for regular file: [%s]\n", json)
}

func TestSymlink(t *testing.T) {
	// We create the symlink now because make.go does not mirror
	// symlinks properly, and it is less intrusive to do that here.
	defer os.RemoveAll("testdata")
	err := os.Mkdir("testdata", 0755)
	AssertNil(t, err, "Mkdir")
	err = os.Chdir("testdata")
	AssertNil(t, err, "Chdir")
	err = os.Symlink("test-target", "test-symlink")
	AssertNil(t, err, "creating test-symlink")
	err = os.Chdir("..")
	AssertNil(t, err, "Chdir")
	fileName := filepath.Join("testdata", "test-symlink")
	fi, err := os.Lstat(fileName)
	AssertNil(t, err, "test-symlink stat")
	m := NewCommonFileMap(fileName, fi)
	json, err := m.JSON()
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	t.Logf("Got json for symlink file: [%s]\n", json)
}

type mixPartsTest struct {
	json, expected string
}

func TestStringFromMixedArray(t *testing.T) {
	tests := []mixPartsTest{
		{`["brad"]`, "brad"},
		{`["brad", 32, 70]`, "brad F"},
		{`["brad", "fitz"]`, "bradfitz"},
		{`["Am", 233, "lie.jpg"]`, "Am\xe9lie.jpg"},
	}
	for idx, test := range tests {
		var v []interface{}
		if err := json.Unmarshal([]byte(test.json), &v); err != nil {
			t.Fatalf("invalid JSON in test %d", idx)
		}
		got := stringFromMixedArray(v)
		if got != test.expected {
			t.Errorf("test %d got %q; expected %q", idx, got, test.expected)
		}
	}
}

func TestRFC3339(t *testing.T) {
	tests := []string{
		"2012-05-13T15:02:47Z",
		"2012-05-13T15:02:47.1234Z",
		"2012-05-13T15:02:47.123456789Z",
	}
	for _, in := range tests {
		tm, err := time.Parse(time.RFC3339, in)
		if err != nil {
			t.Errorf("error parsing %q", in)
			continue
		}
		if out := RFC3339FromTime(tm); in != out {
			t.Errorf("RFC3339FromTime(%q) = %q; want %q", in, out, in)
		}
	}
}

func TestBlobFromReader(t *testing.T) {
	br := blob.MustParse("sha1-f1d2d2f924e986ac86fdf7b36c94bcdf32beec15")
	blob, err := BlobFromReader(br, strings.NewReader(`{"camliVersion": 1, "camliType": "foo"}  `))
	if err != nil {
		t.Error(err)
	} else if blob.Type() != "foo" {
		t.Errorf("got type %q; want foo", blob.Type())
	}

	blob, err = BlobFromReader(br, strings.NewReader(`{"camliVersion": 1, "camliType": "foo"}  X  `))
	if err == nil {
		// TODO(bradfitz): fix this somehow. Currently encoding/json's
		// decoder over-reads.
		// See: https://code.google.com/p/go/issues/detail?id=1955 ,
		// which was "fixed", but not really.
		t.Logf("TODO(bradfitz): make sure bogus non-whitespace after the JSON object causes an error.")
	}
}

func TestAttribute(t *testing.T) {
	tm := time.Unix(123, 456)
	br := blob.MustParse("xxx-1234")
	tests := []struct {
		bb   *Builder
		want string
	}{
		{
			bb: NewSetAttributeClaim(br, "attr1", "val1"),
			want: `{"camliVersion": 1,
  "attribute": "attr1",
  "camliType": "claim",
  "claimDate": "1970-01-01T00:02:03.000000456Z",
  "claimType": "set-attribute",
  "permaNode": "xxx-1234",
  "value": "val1"
}`,
		},
		{
			bb: NewAddAttributeClaim(br, "tag", "funny"),
			want: `{"camliVersion": 1,
  "attribute": "tag",
  "camliType": "claim",
  "claimDate": "1970-01-01T00:02:03.000000456Z",
  "claimType": "add-attribute",
  "permaNode": "xxx-1234",
  "value": "funny"
}`,
		},
		{
			bb: NewDelAttributeClaim(br, "attr1", "val1"),
			want: `{"camliVersion": 1,
  "attribute": "attr1",
  "camliType": "claim",
  "claimDate": "1970-01-01T00:02:03.000000456Z",
  "claimType": "del-attribute",
  "permaNode": "xxx-1234",
  "value": "val1"
}`,
		},
		{
			bb: NewDelAttributeClaim(br, "attr2", ""),
			want: `{"camliVersion": 1,
  "attribute": "attr2",
  "camliType": "claim",
  "claimDate": "1970-01-01T00:02:03.000000456Z",
  "claimType": "del-attribute",
  "permaNode": "xxx-1234"
}`,
		},
		{
			bb: newClaim(&claimParam{
				permanode: br,
				claimType: SetAttributeClaim,
				attribute: "foo",
				value:     "bar",
			}, &claimParam{
				permanode: br,
				claimType: DelAttributeClaim,
				attribute: "foo",
				value:     "specific-del",
			}, &claimParam{
				permanode: br,
				claimType: DelAttributeClaim,
				attribute: "foo",
			}),
			want: `{"camliVersion": 1,
  "camliType": "claim",
  "claimDate": "1970-01-01T00:02:03.000000456Z",
  "claimType": "multi",
  "claims": [
    {
      "attribute": "foo",
      "claimType": "set-attribute",
      "permaNode": "xxx-1234",
      "value": "bar"
    },
    {
      "attribute": "foo",
      "claimType": "del-attribute",
      "permaNode": "xxx-1234",
      "value": "specific-del"
    },
    {
      "attribute": "foo",
      "claimType": "del-attribute",
      "permaNode": "xxx-1234"
    }
  ]
}`,
		},
	}
	for i, tt := range tests {
		tt.bb.SetClaimDate(tm)
		got, err := tt.bb.JSON()
		if err != nil {
			t.Errorf("%d. JSON error = %v", i, err)
			continue
		}
		if got != tt.want {
			t.Errorf("%d.\t got:\n%s\n\twant:q\n%s", i, got, tt.want)
		}
	}
}

func TestDeleteClaim(t *testing.T) {
	tm := time.Unix(123, 456)
	br := blob.MustParse("xxx-1234")
	delTest := struct {
		bb   *Builder
		want string
	}{
		bb: NewDeleteClaim(br),
		want: `{"camliVersion": 1,
  "camliType": "claim",
  "claimDate": "1970-01-01T00:02:03.000000456Z",
  "claimType": "delete",
  "target": "xxx-1234"
}`,
	}
	delTest.bb.SetClaimDate(tm)
	got, err := delTest.bb.JSON()
	if err != nil {
		t.Fatalf("JSON error = %v", err)
	}
	if got != delTest.want {
		t.Fatalf("got:\n%s\n\twant:q\n%s", got, delTest.want)
	}
}

func TestAsClaimAndAsShare(t *testing.T) {
	br := blob.MustParse("xxx-1234")
	signer := blob.MustParse("yyy-5678")

	bb := NewSetAttributeClaim(br, "title", "Test Title")
	bb = bb.SetSigner(signer)
	bb = bb.SetClaimDate(time.Now())
	c1 := bb.Blob()
	c1.ss.Sig = "non-null-sig" // required by AsShare

	bb = NewShareRef(ShareHaveRef, br, true)
	bb = bb.SetSigner(signer)
	bb = bb.SetClaimDate(time.Now())
	c2 := bb.Blob()
	c2.ss.Sig = "non-null-sig" // required by AsShare

	if !br.Valid() {
		t.Error("Blobref not valid")
	}

	_, ok := c1.AsClaim()
	if !ok {
		t.Error("Claim 1 not returned as claim")
	}

	_, ok = c2.AsClaim()
	if !ok {
		t.Error("Claim 2 not returned as claim")
	}

	s, ok := c1.AsShare()
	if ok {
		t.Error("Title claim returned share", s)
	}

	s, ok = c2.AsShare()
	if !ok {
		t.Error("Share claim failed to return share")
	}
}

func TestShareExpiration(t *testing.T) {
	defer func() { clockNow = time.Now }()
	b, err := BlobFromReader(
		blob.MustParse("sha1-64ffa72fa9bcb2f825e7ed40b9451e5cadca4c2c"),
		strings.NewReader(`{"camliVersion": 1,
  "authType": "haveref",
  "camliSigner": "sha1-f2b0b7da718b97ce8c31591d8ed4645c777f3ef4",
  "camliType": "claim",
  "claimDate": "2013-09-08T23:58:53.656549677Z",
  "claimType": "share",
  "expires": "2013-09-09T23:58:53.65658012Z",
  "target": "sha1-f1d2d2f924e986ac86fdf7b36c94bcdf32beec15",
  "transitive": false
,"camliSig":"wsBcBAABCAAQBQJSLQ89CRApMaZ8JvWr2gAAcuEIABRQolhn+yKksfaBx6oLo18NWvWQ+aYweF+5Gu0TH0Ixur7t1o5HFtFSSfFISyggSZDJSjsxoxaawhWrvCe9dZuU2s/zgRpgUtd2xmBt82tLOn9JidnUavsNGFXbfCwdUBSkzN0vDYLmgXW0VtiybB354uIKfOInZor2j8Mq0p6pkWzK3qq9W0dku7iE96YFaTb4W7eOikqoSC6VpjC1/4MQWOYRHLcPcIEY6xJ8es2sYMMSNXuVaR9nMupz8ZcTygP4jh+lPR1OH61q/FSjpRp7GKt4wZ1PknYjMbnpIzVjiSz0MkYd65bpZwuPOwZh/h2kHW7wvHNQZfWUJHEsOAI==J2ID"}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	s, ok := b.AsShare()
	if !ok {
		t.Fatal("expected share")
	}
	clockNow = func() time.Time { return time.Unix(100, 0) }
	if s.IsExpired() {
		t.Error("expected not expired")
	}
	clockNow = func() time.Time { return time.Unix(1378687181+2*86400, 0) }
	if !s.IsExpired() {
		t.Error("expected expired")
	}

	// And without an expiration time:
	b, err = BlobFromReader(
		blob.MustParse("sha1-931875ec6b8d917b7aae9f672f4f92de1ffaeeb1"),
		strings.NewReader(`{"camliVersion": 1,
  "authType": "haveref",
  "camliSigner": "sha1-f2b0b7da718b97ce8c31591d8ed4645c777f3ef4",
  "camliType": "claim",
  "claimDate": "2013-09-09T01:01:09.907842963Z",
  "claimType": "share",
  "target": "sha1-64ffa72fa9bcb2f825e7ed40b9451e5cadca4c2c",
  "transitive": false
,"camliSig":"wsBcBAABCAAQBQJSLR3VCRApMaZ8JvWr2gAA14kIAKmi5rCI5JTBvHbBuAu7wPVA87BLXm/BaD6zjqOENB4U8B+6KxyuT6KXe9P591IDXdZmJTP5tesbLtKw0iAWiRf2ea0Y7Ms3K77nLnSZM5QIOzb4aQKd1668p/5KqU3VfNayoHt69YkXyKBkqyEPjHINzC03QuLz5NIEBMYJaNqKKtEtSgh4gG8BBYq5qQzdKFg/Hx7VhkhW1y/1wwGSFJjaiPFMIJsF4d/gaO01Ip7XLro63ccyCy81tqKHnVjv0uULmZdbpgd3RHGGSnW3c9BfqkGvc3Wl11UQKzqc9OT+WTAWp8TXg6bLES9sQNzerx2wUfjKB9J4Yrk14iBfjl8==AynO"}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	s, ok = b.AsShare()
	if !ok {
		t.Fatal("expected share")
	}
	clockNow = func() time.Time { return time.Unix(100, 0) }
	if s.IsExpired() {
		t.Error("expected not expired")
	}
	clockNow = func() time.Time { return time.Unix(1378687181+2*86400, 0) }
	if s.IsExpired() {
		t.Error("expected not expired")
	}
}

// camlistore.org/issue/305
func TestIssue305(t *testing.T) {
	var in = `{"camliVersion": 1,
  "camliType": "file",
  "fileName": "2012-03-10 15.03.18.m4v",
  "parts": [
    {
      "bytesRef": "sha1-c76d8b17b887c207875e61a77b7eccc60289e61c",
      "size": 20032564
    }
  ]
}`
	var ss superset
	if err := json.NewDecoder(strings.NewReader(in)).Decode(&ss); err != nil {
		t.Fatal(err)
	}
	inref := blob.SHA1FromString(in)
	blob, err := BlobFromReader(inref, strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	if blob.BlobRef() != inref {
		t.Error("original ref = %s; want %s", blob.BlobRef(), inref)
	}
	bb := blob.Builder()
	jback, err := bb.JSON()
	if err != nil {
		t.Fatal(err)
	}
	if jback != in {
		t.Errorf("JSON doesn't match:\n got: %q\nwant: %q\n", jback, in)
	}
	out := bb.Blob()
	if got := out.BlobRef(); got != inref {
		t.Errorf("cloned ref = %v; want %v", got, inref)
	}
}

func TestStaticFileAndStaticSymlink(t *testing.T) {
	// TODO (marete): Split this into two test functions.
	fd, err := ioutil.TempFile("", "schema-test-")
	if err != nil {
		t.Fatalf("io.TempFile(): %v", err)
	}
	defer os.Remove(fd.Name())
	defer fd.Close()

	fi, err := os.Lstat(fd.Name())
	if err != nil {
		t.Fatalf("os.Lstat(): %v", err)
	}

	bb := NewCommonFileMap(fd.Name(), fi)
	bb.SetType("file")
	bb.SetFileName(fd.Name())
	blob := bb.Blob()

	sf, ok := blob.AsStaticFile()
	if !ok {
		t.Fatalf("Blob.AsStaticFile(): Unexpected return value: false")
	}
	if want, got := filepath.Base(fd.Name()), sf.FileName(); want != got {
		t.Fatalf("StaticFile.FileName(): Expected %s, got %s",
			want, got)
	}

	_, ok = sf.AsStaticSymlink()
	if ok {
		t.Fatalf("StaticFile.AsStaticSymlink(): Unexpected return value: true")
	}

	dir, err := ioutil.TempDir("", "schema-test-")
	if err != nil {
		t.Fatalf("ioutil.TempDir(): %v", err)
	}
	defer os.RemoveAll(dir)

	target := "bar"
	src := filepath.Join(dir, "foo")
	err = os.Symlink(target, src)
	fi, err = os.Lstat(src)
	if err != nil {
		t.Fatalf("os.Lstat():  %v", err)
	}

	bb = NewCommonFileMap(src, fi)
	bb.SetType("symlink")
	bb.SetFileName(src)
	bb.SetSymlinkTarget(target)
	blob = bb.Blob()

	sf, ok = blob.AsStaticFile()
	if !ok {
		t.Fatalf("Blob.AsStaticFile(): Unexpected return value: false")
	}
	sl, ok := sf.AsStaticSymlink()
	if !ok {
		t.Fatalf("StaticFile.AsStaticSymlink(): Unexpected return value: false")
	}

	if want, got := filepath.Base(src), sl.FileName(); want != got {
		t.Fatalf("StaticSymlink.FileName(): Expected %s, got %s",
			want, got)
	}

	if want, got := target, sl.SymlinkTargetString(); got != want {
		t.Fatalf("StaticSymlink.SymlinkTargetString(): Expected %s, got %s", want, got)
	}
}
