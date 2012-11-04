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

package indextest

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
	"log"

	"camlistore.org/pkg/blobref"
	"camlistore.org/pkg/index"
	"camlistore.org/pkg/jsonsign"
	"camlistore.org/pkg/schema"
	"camlistore.org/pkg/search"
	"camlistore.org/pkg/test"
)

// An IndexDeps is a helper for populating and querying an Index for tests.
type IndexDeps struct {
	Index *index.Index

	BlobSource *test.Fetcher

	// Following three needed for signing:
	PublicKeyFetcher *test.Fetcher
	EntityFetcher    jsonsign.EntityFetcher // fetching decrypted openpgp entities
	SignerBlobRef    *blobref.BlobRef

	now time.Time // fake clock, nanos since epoch

	Fataler // optional means of failing.
}

type Fataler interface {
	Fatalf(format string, args ...interface{})
}

type logFataler struct{}

func (logFataler) Fatalf(format string, args ...interface{}) {
	log.Fatalf(format, args...)
}

func (id *IndexDeps) Get(key string) string {
	v, _ := id.Index.Storage().Get(key)
	return v
}

func (id *IndexDeps) dumpIndex(t *testing.T) {
	t.Logf("Begin index dump:")
	it := id.Index.Storage().Find("")
	for it.Next() {
		t.Logf("  %q = %q", it.Key(), it.Value())
	}
	if err := it.Close(); err != nil {
		t.Fatalf("iterator close = %v", err)
	}
	t.Logf("End index dump.")
}

func (id *IndexDeps) uploadAndSignMap(m schema.Map) *blobref.BlobRef {
	m["camliSigner"] = id.SignerBlobRef
	unsigned, err := m.JSON()
	if err != nil {
		id.Fatalf("uploadAndSignMap: " + err.Error())
	}
	sr := &jsonsign.SignRequest{
		UnsignedJSON:  unsigned,
		Fetcher:       id.PublicKeyFetcher,
		EntityFetcher: id.EntityFetcher,
	}
	signed, err := sr.Sign()
	if err != nil {
		id.Fatalf("problem signing: " + err.Error())
	}
	tb := &test.Blob{Contents: signed}
	_, err = id.Index.ReceiveBlob(tb.BlobRef(), tb.Reader())
	if err != nil {
		id.Fatalf("problem indexing blob: %v\nblob was:\n%s", err, signed)
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
	id.now = id.now.Add(1 * time.Second)
	return schema.RFC3339FromTime(id.now)
}

func (id *IndexDeps) lastTime() time.Time {
	return id.now
}

func (id *IndexDeps) SetAttribute(permaNode *blobref.BlobRef, attr, value string) *blobref.BlobRef {
	m := schema.NewSetAttributeClaim(permaNode, attr, value)
	m["claimDate"] = id.advanceTime()
	return id.uploadAndSignMap(m)
}

func (id *IndexDeps) AddAttribute(permaNode *blobref.BlobRef, attr, value string) *blobref.BlobRef {
	m := schema.NewAddAttributeClaim(permaNode, attr, value)
	m["claimDate"] = id.advanceTime()
	return id.uploadAndSignMap(m)
}

func (id *IndexDeps) UploadFile(fileName string, contents string) (fileRef, wholeRef *blobref.BlobRef) {
	cb := &test.Blob{Contents: contents}
	id.BlobSource.AddBlob(cb)
	wholeRef = cb.BlobRef()
	_, err := id.Index.ReceiveBlob(wholeRef, cb.Reader())
	if err != nil {
		id.Fatalf("UploadFile.ReceiveBlob: %v", err)
	}

	m := schema.NewFileMap(fileName)
	schema.PopulateParts(m, int64(len(contents)), []schema.BytesPart{
		schema.BytesPart{
			Size:    uint64(len(contents)),
			BlobRef: wholeRef,
		}})
	fjson, err := m.JSON()
	if err != nil {
		id.Fatalf("UploadFile.JSON: %v", err)
	}
	fb := &test.Blob{Contents: fjson}
	id.BlobSource.AddBlob(fb)
	fileRef = fb.BlobRef()
	_, err = id.Index.ReceiveBlob(fileRef, fb.Reader())
	if err != nil {
		panic(err)
	}
	return
}

func osSplitChar() string {
	switch runtime.GOOS {
	case "windows":
		return ";"
	case "plan9":
		panic("unsupported")
	}
	return ":"
}

func findGoPathPackage(pkg string) string {
	gp := os.Getenv("GOPATH")
	if gp == "" {
		panic("no GOPATH set")
	}
	for _, p := range strings.Split(gp, osSplitChar()) {
		dir := filepath.Join(p, "src", pkg)
		if fi, err := os.Stat(dir); err == nil && fi.IsDir() {
			return dir
		}
	}
	panic(fmt.Sprintf("package %q not found in GOPATH(s) of %q", pkg, gp))
}

// NewIndexDeps returns an IndexDeps helper for populating and working
// with the provided index for tests.
func NewIndexDeps(index *index.Index) *IndexDeps {
	secretRingFile := filepath.Join(findGoPathPackage("camlistore.org"), "pkg", "jsonsign", "testdata", "test-secring.gpg")
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
		Index:            index,
		BlobSource:       new(test.Fetcher),
		PublicKeyFetcher: new(test.Fetcher),
		EntityFetcher: &jsonsign.CachingEntityFetcher{
			Fetcher: &jsonsign.FileEntityFetcher{File: secretRingFile},
		},
		SignerBlobRef: pubKey.BlobRef(),
		now:           time.Unix(1322443956, 123456),
		Fataler:       logFataler{},
	}
	// Add dev-camput's test key public key, keyid 26F5ABDA,
	// blobref sha1-ad87ca5c78bd0ce1195c46f7c98e6025abbaf007
	if g, w := id.SignerBlobRef.String(), "sha1-ad87ca5c78bd0ce1195c46f7c98e6025abbaf007"; g != w {
		id.Fatalf("unexpected signer blobref; got signer = %q; want %q", g, w)
	}
	id.PublicKeyFetcher.AddBlob(pubKey)
	id.Index.KeyFetcher = id.PublicKeyFetcher
	id.Index.BlobSource = id.BlobSource
	return id
}

func Index(t *testing.T, initIdx func() *index.Index) {
	id := NewIndexDeps(initIdx())
	id.Fataler = t
	pn := id.NewPermanode()
	t.Logf("uploaded permanode %q", pn)
	br1 := id.SetAttribute(pn, "foo", "foo1")
	br1Time := id.lastTime()
	t.Logf("set attribute %q", br1)
	br2 := id.SetAttribute(pn, "foo", "foo2")
	br2Time := id.lastTime()
	t.Logf("set attribute %q", br2)
	rootClaim := id.SetAttribute(pn, "camliRoot", "rootval")
	rootClaimTime := id.lastTime()
	t.Logf("set attribute %q", rootClaim)

	pnChild := id.NewPermanode()
	memberRef := id.AddAttribute(pn, "camliMember", pnChild.String())
	t.Logf("add-attribute claim %q points to member permanode %q", memberRef, pnChild)
	memberRefTime := id.lastTime()

	lastPermanodeMutation := id.lastTime()
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

	key = fmt.Sprintf("edgeback|%s|%s|%s", pnChild, pn, memberRef)
	if g, e := id.Get(key), "permanode|"; g != e {
		t.Fatalf("edgeback row %q = %q, want %q", key, g, e)
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
				LastModTime: lastPermanodeMutation.Unix(),
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
		if err != os.ErrNotExist {
			t.Errorf("GetBlobMimeType(dummy blobref) = %v; want os.ErrNotExist", err)
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
					Date:      br1Time.UTC(),
					Type:      "set-attribute",
					Attr:      "foo",
					Value:     "foo1",
				},
				&search.Claim{
					BlobRef:   br2,
					Permanode: pn,
					Signer:    id.SignerBlobRef,
					Date:      br2Time.UTC(),
					Type:      "set-attribute",
					Attr:      "foo",
					Value:     "foo2",
				},
				&search.Claim{
					BlobRef:   rootClaim,
					Permanode: pn,
					Signer:    id.SignerBlobRef,
					Date:      rootClaimTime.UTC(),
					Type:      "set-attribute",
					Attr:      "camliRoot",
					Value:     "rootval",
				},
				&search.Claim{
					BlobRef:   memberRef,
					Permanode: pn,
					Signer:    id.SignerBlobRef,
					Date:      memberRefTime.UTC(),
					Type:      "add-attribute",
					Attr:      "camliMember",
					Value:     pnChild.String(),
				},
			})
			if !reflect.DeepEqual(claims, want) {
				t.Errorf("GetOwnerClaims results differ.\n got: %v\nwant: %v",
					claims, want)
			}
		}
	}
}

func PathsOfSignerTarget(t *testing.T, initIdx func() *index.Index) {
	id := NewIndexDeps(initIdx())
	id.Fataler = t
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

	path, err := id.Index.PathLookup(id.SignerBlobRef, pn, "with|pipe", time.Now())
	if err != nil {
		t.Fatalf("PathLookup = %v", err)
	}
	if g, e := path.Target.String(), "targ-124"; g != e {
		t.Errorf("PathLookup = %q; want %q", g, e)
	}
}

func Files(t *testing.T, initIdx func() *index.Index) {
	id := NewIndexDeps(initIdx())
	id.Fataler = t
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
