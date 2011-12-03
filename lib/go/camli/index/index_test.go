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

package index

import (
	"fmt"
	"log"
	"os"
	"reflect"
	"testing"
	"time"

	"camli/blobref"
	"camli/jsonsign"
	"camli/schema"
	"camli/search"
	"camli/test"
)

var _ = log.Printf

type IndexDeps struct {
	Index *Index

	BlobSource *test.Fetcher

	// Following three needed for signing:
	PublicKeyFetcher *test.Fetcher
	EntityFetcher    jsonsign.EntityFetcher // fetching decrypted openpgp entities
	SignerBlobRef    *blobref.BlobRef

	now int64 // fake clock, nanos since epoch
}

func (id *IndexDeps) Get(key string) string {
	v, _ := id.Index.s.Get(key)
	return v
}

func (id *IndexDeps) dumpIndex(t *testing.T) {
	t.Logf("Begin index dump:")
	it := id.Index.s.Find("")
	for it.Next() {
		t.Logf("  %q = %q", it.Key(), it.Value())
	}
	if err := it.Close(); err != nil {
		t.Fatalf("iterator close = %v", err)
	}
	t.Logf("End index dump.")
}

func (id *IndexDeps) uploadAndSignMap(m map[string]interface{}) *blobref.BlobRef {
	m["camliSigner"] = id.SignerBlobRef
	unsigned, err := schema.MapToCamliJson(m)
	if err != nil {
		panic("uploadAndSignMap: " + err.String())
	}
	sr := &jsonsign.SignRequest{
		UnsignedJson:  unsigned,
		Fetcher:       id.PublicKeyFetcher,
		EntityFetcher: id.EntityFetcher,
	}
	signed, err := sr.Sign()
	if err != nil {
		panic("problem signing: " + err.String())
	}
	tb := &test.Blob{Contents: signed}
	_, err = id.Index.ReceiveBlob(tb.BlobRef(), tb.Reader())
	if err != nil {
		panic(fmt.Sprintf("problem indexing blob: %v\nblob was:\n%s", err, signed))
	}
	return tb.BlobRef()
}

// NewPermanode creates (& signs) a new permanode and adds it
// to the index, returning its blobref.
func (id *IndexDeps) NewPermanode() *blobref.BlobRef {
	unsigned := schema.NewUnsignedPermanode()
	return id.uploadAndSignMap(unsigned)
}

func (id *IndexDeps) advanceTime() string {
	id.now += 1e9
	return schema.RFC3339FromNanos(id.now)
}

func (id *IndexDeps) lastTimeNanos() int64 {
	return id.now
}

func (id *IndexDeps) SetAttribute(permaNode *blobref.BlobRef, attr, value string) *blobref.BlobRef {
	m := schema.NewSetAttributeClaim(permaNode, attr, value)
	m["claimDate"] = id.advanceTime()
	return id.uploadAndSignMap(m)
}

func (id *IndexDeps) UploadFile(fileName string, contents string) (fileRef, wholeRef *blobref.BlobRef) {
	cb := &test.Blob{Contents: contents}
	id.BlobSource.AddBlob(cb)
	wholeRef = cb.BlobRef()
	_, err := id.Index.ReceiveBlob(wholeRef, cb.Reader())
	if err != nil {
		panic(err)
	}

	m := schema.NewFileMap(fileName)
	schema.PopulateParts(m, int64(len(contents)), []schema.BytesPart{
		schema.BytesPart{
			Size:    uint64(len(contents)),
			BlobRef: wholeRef,
		}})
	fjson, err := schema.MapToCamliJson(m)
	if err != nil {
		panic(err)
	}
	fb := &test.Blob{Contents: fjson}
	log.Printf("Blob is: %s", fjson)
	id.BlobSource.AddBlob(fb)
	fileRef = fb.BlobRef()
	_, err = id.Index.ReceiveBlob(fileRef, fb.Reader())
	if err != nil {
		panic(err)
	}
	return
}

func NewIndexDeps() *IndexDeps {
	secretRingFile := "../../../../lib/go/camli/jsonsign/testdata/test-secring.gpg"
	pubKey := &test.Blob{Contents: `-----BEGIN PGP PUBLIC KEY BLOCK-----

xsBNBEzgoVsBCAC/56aEJ9BNIGV9FVP+WzenTAkg12k86YqlwJVAB/VwdMlyXxvi
bCT1RVRfnYxscs14LLfcMWF3zMucw16mLlJCBSLvbZ0jn4h+/8vK5WuAdjw2YzLs
WtBcjWn3lV6tb4RJz5gtD/o1w8VWxwAnAVIWZntKAWmkcChCRgdUeWso76+plxE5
aRYBJqdT1mctGqNEISd/WYPMgwnWXQsVi3x4z1dYu2tD9uO1dkAff12z1kyZQIBQ
rexKYRRRh9IKAayD4kgS0wdlULjBU98aeEaMz1ckuB46DX3lAYqmmTEL/Rl9cOI0
Enpn/oOOfYFa5h0AFndZd1blMvruXfdAobjVABEBAAE=
=28/7
-----END PGP PUBLIC KEY BLOCK-----`}

	id := &IndexDeps{
		Index:            newMemoryIndex(),
		BlobSource:       new(test.Fetcher),
		PublicKeyFetcher: new(test.Fetcher),
		EntityFetcher: &jsonsign.CachingEntityFetcher{
			Fetcher: &jsonsign.FileEntityFetcher{File: secretRingFile},
		},
		SignerBlobRef: pubKey.BlobRef(),
		now:           1322443956*1e9 + 123456,
	}
	// Add dev-camput's test key public key, keyid 26F5ABDA,
	// blobref sha1-ad87ca5c78bd0ce1195c46f7c98e6025abbaf007
	if id.SignerBlobRef.String() != "sha1-ad87ca5c78bd0ce1195c46f7c98e6025abbaf007" {
		panic("unexpected signer blobref")
	}
	id.PublicKeyFetcher.AddBlob(pubKey)
	id.Index.KeyFetcher = id.PublicKeyFetcher
	id.Index.BlobSource = id.BlobSource
	return id
}

func TestIndex(t *testing.T) {
	id := NewIndexDeps()
	pn := id.NewPermanode()
	t.Logf("uploaded permanode %q", pn)
	br1 := id.SetAttribute(pn, "foo", "foo1")
	br1Time := id.lastTimeNanos()
	t.Logf("set attribute %q", br1)
	br2 := id.SetAttribute(pn, "foo", "foo2")
	br2Time := id.lastTimeNanos()
	t.Logf("set attribute %q", br2)
	rootClaim := id.SetAttribute(pn, "camliRoot", "rootval")
	rootClaimTime := id.lastTimeNanos()
	t.Logf("set attribute %q", rootClaim)

	id.dumpIndex(t)

	key := "signerkeyid:sha1-ad87ca5c78bd0ce1195c46f7c98e6025abbaf007"
	if g, e := id.Get(key), "2931A67C26F5ABDA"; g != e {
		t.Fatalf("%q = %q, want %q", key, g, e)
	}

	key = "have:" + pn.String()
	pnSizeStr := id.Get(key)
	if pnSizeStr == "" {
		t.Fatalf("missing key %q", key)
	}

	key = "meta:" + pn.String()
	if g, e := id.Get(key), pnSizeStr+"|application/json; camliType=permanode"; g != e {
		t.Errorf("key %q = %q, want %q", key, g, e)
	}

	key = "recpn|2931A67C26F5ABDA|rt7988-88-71T98:67:62.999876543Z|" + br1.String()
	if g, e := id.Get(key), pn.String(); g != e {
		t.Fatalf("%q = %q, want %q (permanode)", key, g, e)
	}

	key = "recpn|2931A67C26F5ABDA|rt7988-88-71T98:67:61.999876543Z|" + br2.String()
	if g, e := id.Get(key), pn.String(); g != e {
		t.Fatalf("%q = %q, want %q (permanode)", key, g, e)
	}

	// PermanodeOfSignerAttrValue
	{
		gotPN, err := id.Index.PermanodeOfSignerAttrValue(id.SignerBlobRef, "camliRoot", "rootval")
		if err != nil {
			t.Fatalf("id.Index.PermanodeOfSignerAttrValue = %v", err)
		}
		if gotPN.String() != pn.String() {
			t.Errorf("id.Index.PermanodeOfSignerAttrValue = %q, want %q", gotPN, pn)
		}
		_, err = id.Index.PermanodeOfSignerAttrValue(id.SignerBlobRef, "camliRoot", "MISSING")
		if err == nil {
			t.Errorf("expected an error from PermanodeOfSignerAttrValue on missing value")
		}
	}

	// GetRecentPermanodes
	{
		ch := make(chan *search.Result, 10) // only expect 1 result, but 3 if buggy.
		err := id.Index.GetRecentPermanodes(ch, id.SignerBlobRef, 50)
		if err != nil {
			t.Fatalf("GetRecentPermanodes = %v", err)
		}
		got := []*search.Result{}
		for r := range ch {
			got = append(got, r)
		}
		want := []*search.Result{
			&search.Result{
				BlobRef:     pn,
				Signer:      id.SignerBlobRef,
				LastModTime: 1322443959,
			},
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("GetRecentPermanode results differ.\n got: %v\nwant: %v",
				search.Results(got), search.Results(want))
		}
	}

	// GetBlobMimeType
	{
		mime, size, err := id.Index.GetBlobMimeType(pn)
		if err != nil {
			t.Errorf("GetBlobMimeType(%q) = %v", pn, err)
		} else {
			if e := "application/json; camliType=permanode"; mime != e {
				t.Errorf("GetBlobMimeType(%q) mime = %q, want %q", pn, mime, e)
			}
			if size == 0 {
				t.Errorf("GetBlobMimeType(%q) size is zero", pn)
			}
		}
		_, _, err = id.Index.GetBlobMimeType(blobref.Parse("abc-123"))
		if err != os.ENOENT {
			t.Errorf("GetBlobMimeType(dummy blobref) = %v; want os.ENOENT", err)
		}
	}

	// GetOwnerClaims
	{
		claims, err := id.Index.GetOwnerClaims(pn, id.SignerBlobRef)
		if err != nil {
			t.Errorf("GetOwnerClaims = %v", err)
		} else {
			want := search.ClaimList([]*search.Claim{
				&search.Claim{
					BlobRef:   br1,
					Permanode: pn,
					Signer:    id.SignerBlobRef,
					Date:      time.NanosecondsToUTC(br1Time),
					Type:      "set-attribute",
					Attr:      "foo",
					Value:     "foo1",
				},
				&search.Claim{
					BlobRef:   br2,
					Permanode: pn,
					Signer:    id.SignerBlobRef,
					Date:      time.NanosecondsToUTC(br2Time),
					Type:      "set-attribute",
					Attr:      "foo",
					Value:     "foo2",
				},
				&search.Claim{
					BlobRef:   rootClaim,
					Permanode: pn,
					Signer:    id.SignerBlobRef,
					Date:      time.NanosecondsToUTC(rootClaimTime),
					Type:      "set-attribute",
					Attr:      "camliRoot",
					Value:     "rootval",
				},
			})
			if !reflect.DeepEqual(claims, want) {
				t.Errorf("GetOwnerClaims results differ.\n got: %v\nwant: %v",
					claims, want)
			}
		}
	}
}

func TestPathsOfSignerTarget(t *testing.T) {
	id := NewIndexDeps()
	pn := id.NewPermanode()
	t.Logf("uploaded permanode %q", pn)

	claim1 := id.SetAttribute(pn, "camliPath:somedir", "targ-123")
	claim2 := id.SetAttribute(pn, "camliPath:with|pipe", "targ-124")
	t.Logf("made path claims %q and %q", claim1, claim2)

	id.dumpIndex(t)

	type test struct {
		blobref string
		want    int
	}
	tests := []test{
		{"targ-123", 1},
		{"targ-124", 1},
		{"targ-125", 0},
	}
	for _, tt := range tests {
		signer := id.SignerBlobRef
		paths, err := id.Index.PathsOfSignerTarget(signer, blobref.Parse(tt.blobref))
		if err != nil {
			t.Fatalf("PathsOfSignerTarget(%q): %v", tt.blobref, err)
		}
		if len(paths) != tt.want {
			t.Fatalf("PathsOfSignerTarget(%q) got %d results; want %d",
				tt.blobref, len(paths), tt.want)
		}
		if tt.blobref == "targ-123" {
			p := paths[0]
			want := fmt.Sprintf(
				"Path{Claim: %s, 2011-11-28T01:32:37.000123456Z; Base: %s + Suffix \"somedir\" => Target targ-123}",
				claim1, pn)
			if g := p.String(); g != want {
				t.Errorf("claim wrong.\n got: %s\nwant: %s", g, want)
			}
		}
	}

	path, err := id.Index.PathLookup(id.SignerBlobRef, pn, "with|pipe", nil)
	if err != nil {
		t.Fatalf("PathLookup = %v", err)
	}
	if g, e := path.Target.String(), "targ-124"; g != e {
		t.Errorf("PathLookup = %q; want %q", g, e)
	}
}

func TestFiles(t *testing.T) {
	id := NewIndexDeps()
	fileRef, wholeRef := id.UploadFile("foo.html", "<html>I am an html file.</html>")
	t.Logf("uploaded fileref %q, wholeRef %q", fileRef, wholeRef)
	id.dumpIndex(t)

	// ExistingFileSchemas
	{
		key := fmt.Sprintf("wholetofile|%s|%s", wholeRef, fileRef)
		if g, e := id.Get(key), "1"; g != e {
			t.Fatalf("%q = %q, want %q", key, g, e)
		}

		refs, err := id.Index.ExistingFileSchemas(wholeRef)
		if err != nil {
			t.Fatalf("ExistingFileSchemas = %v", err)
		}
		want := []*blobref.BlobRef{fileRef}
		if !reflect.DeepEqual(refs, want) {
			t.Errorf("ExistingFileSchemas got = %#v, want %#v", refs, want)
		}
	}

	// FileInfo
	{
		key := fmt.Sprintf("fileinfo|%s", fileRef)
		if g, e := id.Get(key), "31|foo.html|text%2Fhtml"; g != e {
			t.Fatalf("%q = %q, want %q", key, g, e)
		}

		fi, err := id.Index.GetFileInfo(fileRef)
		if err != nil {
			t.Fatalf("GetFileInfo = %v", err)
		}
		if g, e := fi.Size, int64(31); g != e {
			t.Errorf("Size = %d, want %d", g, e)
		}
		if g, e := fi.FileName, "foo.html"; g != e {
			t.Errorf("FileName = %q, want %q", g, e)
		}
		if g, e := fi.MimeType, "text/html"; g != e {
			t.Errorf("MimeType = %q, want %q", g, e)
		}
	}
}

func TestReverseTimeString(t *testing.T) {
	in := "2011-11-27T01:23:45Z"
	got := reverseTimeString(in)
	want := "rt7988-88-72T98:76:54Z"
	if got != want {
		t.Fatalf("reverseTimeString = %q, want %q", got, want)
	}
	back := unreverseTimeString(got)
	if back != in {
		t.Fatalf("unreverseTimeString = %q, want %q", back, in)
	}
}
