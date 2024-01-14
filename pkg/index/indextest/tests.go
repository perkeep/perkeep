/*
Copyright 2011 The Perkeep Authors

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

// Package indextest contains the unit tests for the indexer so they
// can be re-used for each specific implementation of the index
// Storage interface.
package indextest // import "perkeep.org/pkg/index/indextest"

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"perkeep.org/internal/osutil"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/index"
	"perkeep.org/pkg/jsonsign"
	"perkeep.org/pkg/schema"
	"perkeep.org/pkg/sorted"
	"perkeep.org/pkg/test"
	"perkeep.org/pkg/types/camtypes"
)

var ctxbg = context.Background()

// An IndexDeps is a helper for populating and querying an Index for tests.
type IndexDeps struct {
	Index *index.Index

	BlobSource *test.Fetcher

	// Following three needed for signing:
	PublicKeyFetcher *test.Fetcher
	EntityFetcher    jsonsign.EntityFetcher // fetching decrypted openpgp entities
	SignerBlobRef    blob.Ref

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

func (id *IndexDeps) Set(key, value string) error {
	return id.Index.Storage().Set(key, value)
}

func (id *IndexDeps) DumpIndex(t *testing.T) {
	t.Logf("Begin index dump:")
	it := id.Index.Storage().Find("", "")
	for it.Next() {
		t.Logf("  %q = %q", it.Key(), it.Value())
	}
	if err := it.Close(); err != nil {
		t.Fatalf("iterator close = %v", err)
	}
	t.Logf("End index dump.")
}

func (id *IndexDeps) Sign(m *schema.Builder) *test.Blob {
	m.SetSigner(id.SignerBlobRef)
	unsigned, err := m.JSON()
	if err != nil {
		id.Fatalf("uploadAndSignMap: " + err.Error())
	}
	sr := &jsonsign.SignRequest{
		UnsignedJSON:  unsigned,
		Fetcher:       id.PublicKeyFetcher,
		EntityFetcher: id.EntityFetcher,
		SignatureTime: id.now,
	}
	signed, err := sr.Sign(ctxbg)
	if err != nil {
		id.Fatalf("problem signing: " + err.Error())
	}
	tb := &test.Blob{Contents: signed}
	return tb
}

func (id *IndexDeps) Upload(tb *test.Blob) blob.Ref {
	_, err := id.BlobSource.ReceiveBlob(ctxbg, tb.BlobRef(), tb.Reader())
	if err != nil {
		id.Fatalf("public uploading signed blob to blob source, pre-indexing: %v, %v", tb.BlobRef(), err)
	}
	_, err = id.Index.ReceiveBlob(ctxbg, tb.BlobRef(), tb.Reader())
	if err != nil {
		id.Fatalf("problem indexing blob: %v\nblob was:\n%s", err, tb.Contents)
	}
	return tb.BlobRef()
}

func (id *IndexDeps) uploadAndSign(m *schema.Builder) blob.Ref {
	return id.Upload(id.Sign(m))
}

// NewPermanode creates (& signs) a new permanode and adds it
// to the index, returning its blobref.
func (id *IndexDeps) NewPermanode() blob.Ref {
	unsigned := schema.NewUnsignedPermanode()
	return id.uploadAndSign(unsigned)
}

// NewPlannedPermanode creates (& signs) a new planned permanode and adds it
// to the index, returning its blobref.
func (id *IndexDeps) NewPlannedPermanode(key string) blob.Ref {
	unsigned := schema.NewPlannedPermanode(key)
	return id.uploadAndSign(unsigned)
}

func (id *IndexDeps) advanceTime() time.Time {
	id.now = id.now.Add(1 * time.Second)
	return id.now
}

// LastTime returns the time of the most recent mutation (claim).
func (id *IndexDeps) LastTime() time.Time {
	return id.now
}

func (id *IndexDeps) SetAttribute(permaNode blob.Ref, attr, value string) blob.Ref {
	m := schema.NewSetAttributeClaim(permaNode, attr, value)
	m.SetClaimDate(id.advanceTime())
	return id.uploadAndSign(m)
}

func (id *IndexDeps) SetAttribute_NoTimeMove(permaNode blob.Ref, attr, value string) blob.Ref {
	m := schema.NewSetAttributeClaim(permaNode, attr, value)
	m.SetClaimDate(id.LastTime())
	return id.uploadAndSign(m)
}

func (id *IndexDeps) AddAttribute(permaNode blob.Ref, attr, value string) blob.Ref {
	m := schema.NewAddAttributeClaim(permaNode, attr, value)
	m.SetClaimDate(id.advanceTime())
	return id.uploadAndSign(m)
}

func (id *IndexDeps) DelAttribute(permaNode blob.Ref, attr, value string) blob.Ref {
	m := schema.NewDelAttributeClaim(permaNode, attr, value)
	m.SetClaimDate(id.advanceTime())
	return id.uploadAndSign(m)
}

func (id *IndexDeps) Delete(target blob.Ref) blob.Ref {
	m := schema.NewDeleteClaim(target)
	m.SetClaimDate(id.advanceTime())
	return id.uploadAndSign(m)
}

var noTime = time.Time{}

func (id *IndexDeps) UploadString(v string) blob.Ref {
	cb := &test.Blob{Contents: v}
	id.BlobSource.AddBlob(cb)
	br := cb.BlobRef()
	_, err := id.Index.ReceiveBlob(ctxbg, br, cb.Reader())
	if err != nil {
		id.Fatalf("UploadString: %v", err)
	}
	return br
}

// If modTime is zero, it's not used.
func (id *IndexDeps) UploadFile(fileName string, contents string, modTime time.Time) (fileRef, wholeRef blob.Ref) {
	wholeRef = id.UploadString(contents)

	m := schema.NewFileMap(fileName)
	m.PopulateParts(int64(len(contents)), []schema.BytesPart{
		{
			Size:    uint64(len(contents)),
			BlobRef: wholeRef,
		}})
	if !modTime.IsZero() {
		m.SetModTime(modTime)
	}
	fjson, err := m.JSON()
	if err != nil {
		id.Fatalf("UploadFile.JSON: %v", err)
	}
	fb := &test.Blob{Contents: fjson}
	id.BlobSource.AddBlob(fb)
	fileRef = fb.BlobRef()
	_, err = id.Index.ReceiveBlob(ctxbg, fileRef, fb.Reader())
	if err != nil {
		panic(err)
	}
	return
}

// If modTime is zero, it's not used.
func (id *IndexDeps) UploadDir(dirName string, children []blob.Ref, modTime time.Time) blob.Ref {
	// static-set entries blob
	ss := schema.NewStaticSet()
	ss.SetStaticSetMembers(children)
	ssjson := ss.Blob().JSON()
	ssb := &test.Blob{Contents: ssjson}
	id.BlobSource.AddBlob(ssb)
	_, err := id.Index.ReceiveBlob(ctxbg, ssb.BlobRef(), ssb.Reader())
	if err != nil {
		id.Fatalf("UploadDir.ReceiveBlob: %v", err)
	}

	// directory blob
	bb := schema.NewDirMap(dirName)
	bb.PopulateDirectoryMap(ssb.BlobRef())
	if !modTime.IsZero() {
		bb.SetModTime(modTime)
	}
	dirjson, err := bb.JSON()
	if err != nil {
		id.Fatalf("UploadDir.JSON: %v", err)
	}
	dirb := &test.Blob{Contents: dirjson}
	id.BlobSource.AddBlob(dirb)
	_, err = id.Index.ReceiveBlob(ctxbg, dirb.BlobRef(), dirb.Reader())
	if err != nil {
		id.Fatalf("UploadDir.ReceiveBlob: %v", err)
	}
	return dirb.BlobRef()
}

var (
	PubKey = &test.Blob{Contents: `-----BEGIN PGP PUBLIC KEY BLOCK-----

xsBNBEzgoVsBCAC/56aEJ9BNIGV9FVP+WzenTAkg12k86YqlwJVAB/VwdMlyXxvi
bCT1RVRfnYxscs14LLfcMWF3zMucw16mLlJCBSLvbZ0jn4h+/8vK5WuAdjw2YzLs
WtBcjWn3lV6tb4RJz5gtD/o1w8VWxwAnAVIWZntKAWmkcChCRgdUeWso76+plxE5
aRYBJqdT1mctGqNEISd/WYPMgwnWXQsVi3x4z1dYu2tD9uO1dkAff12z1kyZQIBQ
rexKYRRRh9IKAayD4kgS0wdlULjBU98aeEaMz1ckuB46DX3lAYqmmTEL/Rl9cOI0
Enpn/oOOfYFa5h0AFndZd1blMvruXfdAobjVABEBAAE=
=28/7
-----END PGP PUBLIC KEY BLOCK-----`}
	KeyID = "2931A67C26F5ABDA"
)

// NewIndexDeps returns an IndexDeps helper for populating and working
// with the provided index for tests.
func NewIndexDeps(index *index.Index) *IndexDeps {
	srcRoot, err := osutil.PkSourceRoot()
	if err != nil {
		log.Fatalf("source root folder not found: %v", err)
	}
	secretRingFile := filepath.Join(srcRoot, "pkg", "jsonsign", "testdata", "test-secring.gpg")

	id := &IndexDeps{
		Index:            index,
		BlobSource:       new(test.Fetcher),
		PublicKeyFetcher: new(test.Fetcher),
		EntityFetcher: &jsonsign.CachingEntityFetcher{
			Fetcher: &jsonsign.FileEntityFetcher{File: secretRingFile},
		},
		SignerBlobRef: PubKey.BlobRef(),
		now:           test.ClockOrigin,
		Fataler:       logFataler{},
	}
	// Add dev client test key public key, keyid 26F5ABDA,
	// blobref sha224-a794846212ff67acdd00c6b90eee492baf674d41da8a621d2e8042dd
	if g, w := id.SignerBlobRef.String(), "sha224-a794846212ff67acdd00c6b90eee492baf674d41da8a621d2e8042dd"; g != w {
		id.Fatalf("unexpected signer blobref; got signer = %q; want %q", g, w)
	}
	id.PublicKeyFetcher.AddBlob(PubKey)
	id.Index.KeyFetcher = id.PublicKeyFetcher
	id.Index.InitBlobSource(id.BlobSource)
	return id
}

func Index(t *testing.T, initIdx func() *index.Index) {
	ctx := context.Background()
	oldLocal := time.Local
	time.Local = time.UTC
	defer func() { time.Local = oldLocal }()

	id := NewIndexDeps(initIdx())
	id.Fataler = t
	defer id.DumpIndex(t)
	pn := id.NewPermanode()
	t.Logf("uploaded permanode %q", pn)
	br1 := id.SetAttribute(pn, "tag", "foo1")
	br1Time := id.LastTime()
	t.Logf("set attribute %q", br1)
	br2 := id.SetAttribute(pn, "tag", "foo2")
	br2Time := id.LastTime()
	t.Logf("set attribute %q", br2)
	rootClaim := id.SetAttribute(pn, "camliRoot", "rootval")
	rootClaimTime := id.LastTime()
	t.Logf("set attribute %q", rootClaim)

	pnChild := id.NewPermanode()
	id.SetAttribute(pnChild, "unindexed", "lost in time and space")
	br3 := id.SetAttribute(pnChild, "tag", "bar")
	br3Time := id.LastTime()
	t.Logf("set attribute %q", br3)
	memberRef := id.AddAttribute(pn, "camliMember", pnChild.String())
	t.Logf("add-attribute claim %q points to member permanode %q", memberRef, pnChild)
	memberRefTime := id.LastTime()

	// TODO(bradfitz): add EXIF tests here, once that stuff is ready.
	if false {
		srcRoot, err := osutil.PkSourceRoot()
		if err != nil {
			t.Fatalf("source root folder not found: %v", err)
		}
		for i := 1; i <= 8; i++ {
			fileBase := fmt.Sprintf("f%d-exif.jpg", i)
			fileName := filepath.Join(srcRoot, "pkg", "images", "testdata", fileBase)
			contents, err := os.ReadFile(fileName)
			if err != nil {
				t.Fatal(err)
			}
			id.UploadFile(fileBase, string(contents), noTime)
		}
	}

	// Upload some files.
	var jpegFileRef, exifFileRef, exifWholeRef, badExifWholeRef, nanExifWholeRef, mediaFileRef, mediaWholeRef, heicEXIFWholeRef blob.Ref
	{
		srcRoot, err := osutil.PkSourceRoot()
		if err != nil {
			t.Fatalf("source root folder not found: %v", err)
		}
		uploadFile := func(file string, modTime time.Time) (fileRef, wholeRef blob.Ref) {
			fileName := filepath.Join(srcRoot, "pkg", "index", "indextest", "testdata", file)
			contents, err := os.ReadFile(fileName)
			if err != nil {
				t.Fatal(err)
			}
			fileRef, wholeRef = id.UploadFile(file, string(contents), modTime)
			return
		}
		jpegFileRef, _ = uploadFile("dude.jpg", noTime)
		exifFileRef, exifWholeRef = uploadFile("dude-exif.jpg", time.Unix(1361248796, 0))
		_, badExifWholeRef = uploadFile("bad-exif.jpg", time.Unix(1361248796, 0))
		_, nanExifWholeRef = uploadFile("nan-exif.jpg", time.Unix(1361248796, 0))
		mediaFileRef, mediaWholeRef = uploadFile("0s.mp3", noTime)
		_, heicEXIFWholeRef = uploadFile("black-seattle-truncated.heic", time.Unix(1361248796, 0))
	}

	// Upload the dir containing the previous files.
	imagesDirRef := id.UploadDir(
		"testdata",
		[]blob.Ref{jpegFileRef, exifFileRef, mediaFileRef},
		time.Now(),
	)

	lastPermanodeMutation := id.LastTime()

	key := "signerkeyid:sha224-a794846212ff67acdd00c6b90eee492baf674d41da8a621d2e8042dd"
	if g, e := id.Get(key), "2931A67C26F5ABDA"; g != e {
		t.Fatalf("%q = %q, want %q", key, g, e)
	}

	key = "imagesize|" + jpegFileRef.String()
	if g, e := id.Get(key), "50|100"; g != e {
		t.Errorf("JPEG dude.jpg key %q = %q; want %q", key, g, e)
	}

	key = "filetimes|" + jpegFileRef.String()
	if g, e := id.Get(key), ""; g != e {
		t.Errorf("JPEG dude.jpg key %q = %q; want %q", key, g, e)
	}

	key = "filetimes|" + exifFileRef.String()
	if g, e := id.Get(key), "2013-02-18T01%3A11%3A20Z%2C2013-02-19T04%3A39%3A56Z"; g != e {
		t.Errorf("EXIF dude-exif.jpg key %q = %q; want %q", key, g, e)
	}

	key = "exifgps|" + exifWholeRef.String()
	// Test that small values aren't printed as scientific notation.
	if g, e := id.Get(key), "-0.0000010|-120.0000000"; g != e {
		t.Errorf("EXIF dude-exif.jpg key %q = %q; want %q", key, g, e)
	}

	// Check that indexer ignores exif lat/fields that are out of bounds.
	key = "exifgps|" + badExifWholeRef.String()
	if g, e := id.Get(key), ""; g != e {
		t.Errorf("EXIF bad-exif.jpg key %q = %q; want %q", key, g, e)
	}

	// Check that indexer ignores NaN exif lat/long.
	key = "exifgps|" + nanExifWholeRef.String()
	if g, e := id.Get(key), ""; g != e {
		t.Errorf("EXIF nan-exif.jpg key %q = %q; want %q", key, g, e)
	}

	// Check that we can read EXIF from HEIC files too
	key = "exifgps|" + heicEXIFWholeRef.String()
	if g, e := id.Get(key), "47.6496056|-122.3512806"; g != e {
		t.Errorf("EXIF black-seattle-truncated.heic key %q = %q; want %q", key, g, e)
	}

	key = "have:" + pn.String()
	pnSizeStr := strings.TrimSuffix(id.Get(key), "|indexed")
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

	mediaTests := []struct {
		prop, exp string
	}{
		{"title", "Zero Seconds"},
		{"artist", "Test Artist"},
		{"album", "Test Album"},
		{"genre", "(20)Alternative"},
		{"musicbrainzalbumid", "00000000-0000-0000-0000-000000000000"},
		{"year", "1992"},
		{"track", "1"},
		{"disc", "2"},
		{"mediaref", "sha224-c4ebd5b557419a68ba6e0af716deeb9196e71e0dca65a7f805e3f723"},
		{"durationms", "26"},
	}
	for _, tt := range mediaTests {
		key = fmt.Sprintf("mediatag|%s|%s", mediaWholeRef.String(), tt.prop)
		if g, _ := url.QueryUnescape(id.Get(key)); g != tt.exp {
			t.Errorf("0s.mp3 key %q = %q; want %q", key, g, tt.exp)
		}
	}

	// PermanodeOfSignerAttrValue
	{
		gotPN, err := id.Index.PermanodeOfSignerAttrValue(ctx, id.SignerBlobRef, "camliRoot", "rootval")
		if err != nil {
			t.Fatalf("id.Index.PermanodeOfSignerAttrValue = %v", err)
		}
		if gotPN.String() != pn.String() {
			t.Errorf("id.Index.PermanodeOfSignerAttrValue = %q, want %q", gotPN, pn)
		}
		_, err = id.Index.PermanodeOfSignerAttrValue(ctx, id.SignerBlobRef, "camliRoot", "MISSING")
		if err == nil {
			t.Errorf("expected an error from PermanodeOfSignerAttrValue on missing value")
		}
	}

	// SearchPermanodesWithAttr - match attr type "tag" and value "foo1"
	{
		ch := make(chan blob.Ref, 10)
		req := &camtypes.PermanodeByAttrRequest{
			Signer:    id.SignerBlobRef,
			Attribute: "tag",
			Query:     "foo1",
		}
		err := id.Index.SearchPermanodesWithAttr(ctx, ch, req)
		if err != nil {
			t.Fatalf("SearchPermanodesWithAttr = %v", err)
		}
		var got []blob.Ref
		for r := range ch {
			got = append(got, r)
		}
		want := []blob.Ref{pn}
		if len(got) < 1 || got[0].String() != want[0].String() {
			t.Errorf("id.Index.SearchPermanodesWithAttr gives %q, want %q", got, want)
		}
	}

	// SearchPermanodesWithAttr - match all with attr type "tag"
	{
		ch := make(chan blob.Ref, 10)
		req := &camtypes.PermanodeByAttrRequest{
			Signer:    id.SignerBlobRef,
			Attribute: "tag",
		}
		err := id.Index.SearchPermanodesWithAttr(ctx, ch, req)
		if err != nil {
			t.Fatalf("SearchPermanodesWithAttr = %v", err)
		}
		var got []blob.Ref
		for r := range ch {
			got = append(got, r)
		}
		want := []blob.Ref{pn, pnChild}
		if len(got) != len(want) {
			t.Errorf("SearchPermanodesWithAttr results differ.\n got: %q\nwant: %q",
				got, want)
		}
		for _, w := range want {
			found := false
			for _, g := range got {
				if g.String() == w.String() {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("SearchPermanodesWithAttr: %v was not found.\n", w)
			}
		}
	}

	// SearchPermanodesWithAttr - match none with attr type "tag=nosuchtag"
	{
		ch := make(chan blob.Ref, 10)
		req := &camtypes.PermanodeByAttrRequest{
			Signer:    id.SignerBlobRef,
			Attribute: "tag",
			Query:     "nosuchtag",
		}
		err := id.Index.SearchPermanodesWithAttr(ctx, ch, req)
		if err != nil {
			t.Fatalf("SearchPermanodesWithAttr = %v", err)
		}
		var got []blob.Ref
		for r := range ch {
			got = append(got, r)
		}
		want := []blob.Ref{}
		if len(got) != len(want) {
			t.Errorf("SearchPermanodesWithAttr results differ.\n got: %q\nwant: %q",
				got, want)
		}
	}
	// SearchPermanodesWithAttr - error for unindexed attr
	{
		ch := make(chan blob.Ref, 10)
		req := &camtypes.PermanodeByAttrRequest{
			Signer:    id.SignerBlobRef,
			Attribute: "unindexed",
		}
		err := id.Index.SearchPermanodesWithAttr(ctx, ch, req)
		if err == nil {
			t.Fatalf("SearchPermanodesWithAttr with unindexed attribute should return an error")
		}
	}

	// Delete value "pony" of type "title" (which does not actually exist) for pn
	br4 := id.DelAttribute(pn, "title", "pony")
	br4Time := id.LastTime()
	// and verify it is not found when searching by attr
	{
		ch := make(chan blob.Ref, 10)
		req := &camtypes.PermanodeByAttrRequest{
			Signer:    id.SignerBlobRef,
			Attribute: "title",
			Query:     "pony",
		}
		err := id.Index.SearchPermanodesWithAttr(ctx, ch, req)
		if err != nil {
			t.Fatalf("SearchPermanodesWithAttr = %v", err)
		}
		var got []blob.Ref
		for r := range ch {
			got = append(got, r)
		}
		want := []blob.Ref{}
		if len(got) != len(want) {
			t.Errorf("SearchPermanodesWithAttr results differ.\n got: %q\nwant: %q",
				got, want)
		}
	}

	// GetRecentPermanodes
	{
		verify := func(prefix string, want []camtypes.RecentPermanode, before time.Time) {
			ch := make(chan camtypes.RecentPermanode, 10) // expect 2 results, but maybe more if buggy.
			err := id.Index.GetRecentPermanodes(ctx, ch, id.SignerBlobRef, 50, before)
			if err != nil {
				t.Fatalf("[%s] GetRecentPermanodes = %v", prefix, err)
			}
			got := []camtypes.RecentPermanode{}
			for r := range ch {
				got = append(got, r)
			}
			if len(got) != len(want) {
				t.Errorf("[%s] GetRecentPermanode results differ.\n got: %v\nwant: %v",
					prefix, searchResults(got), searchResults(want))
			}
			for _, w := range want {
				found := false
				for _, g := range got {
					if g.Equal(w) {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("[%s] GetRecentPermanode: %v was not found.\n got: %v\nwant: %v",
						prefix, w, searchResults(got), searchResults(want))
				}
			}
		}

		want := []camtypes.RecentPermanode{
			{
				Permanode:   pn,
				Signer:      id.SignerBlobRef,
				LastModTime: br4Time,
			},
			{
				Permanode:   pnChild,
				Signer:      id.SignerBlobRef,
				LastModTime: br3Time,
			},
		}

		before := time.Time{}
		verify("Zero before", want, before)

		before = lastPermanodeMutation
		t.Log("lastPermanodeMutation", lastPermanodeMutation,
			lastPermanodeMutation.Unix())
		verify("Non-zero before", want[1:], before)
	}
	// GetDirMembers
	{
		ch := make(chan blob.Ref, 10) // expect 2 results
		err := id.Index.GetDirMembers(ctx, imagesDirRef, ch, 50)
		if err != nil {
			t.Fatalf("GetDirMembers = %v", err)
		}
		got := []blob.Ref{}
		for r := range ch {
			got = append(got, r)
		}
		want := []blob.Ref{jpegFileRef, exifFileRef, mediaFileRef}
		if len(got) != len(want) {
			t.Errorf("GetDirMembers results differ.\n got: %v\nwant: %v",
				got, want)
		}
		for _, w := range want {
			found := false
			for _, g := range got {
				if w == g {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("GetDirMembers: %v was not found.", w)
			}
		}
	}

	// GetBlobMeta
	{
		meta, err := id.Index.GetBlobMeta(ctx, pn)
		if err != nil {
			t.Errorf("GetBlobMeta(%q) = %v", pn, err)
		} else {
			if e := schema.TypePermanode; meta.CamliType != e {
				t.Errorf("GetBlobMeta(%q) mime = %q, want %q", pn, meta.CamliType, e)
			}
			if meta.Size == 0 {
				t.Errorf("GetBlobMeta(%q) size is zero", pn)
			}
		}
		_, err = id.Index.GetBlobMeta(ctx, blob.ParseOrZero("abc-123"))
		if err != os.ErrNotExist {
			t.Errorf("GetBlobMeta(dummy blobref) = %v; want os.ErrNotExist", err)
		}
	}

	// AppendClaims
	{
		claims, err := id.Index.AppendClaims(ctx, nil, pn, KeyID, "")
		if err != nil {
			t.Errorf("AppendClaims = %v", err)
		} else {
			want := []camtypes.Claim{
				{
					BlobRef:   br1,
					Permanode: pn,
					Signer:    id.SignerBlobRef,
					Date:      br1Time.UTC(),
					Type:      "set-attribute",
					Attr:      "tag",
					Value:     "foo1",
				},
				{
					BlobRef:   br2,
					Permanode: pn,
					Signer:    id.SignerBlobRef,
					Date:      br2Time.UTC(),
					Type:      "set-attribute",
					Attr:      "tag",
					Value:     "foo2",
				},
				{
					BlobRef:   rootClaim,
					Permanode: pn,
					Signer:    id.SignerBlobRef,
					Date:      rootClaimTime.UTC(),
					Type:      "set-attribute",
					Attr:      "camliRoot",
					Value:     "rootval",
				},
				{
					BlobRef:   memberRef,
					Permanode: pn,
					Signer:    id.SignerBlobRef,
					Date:      memberRefTime.UTC(),
					Type:      "add-attribute",
					Attr:      "camliMember",
					Value:     pnChild.String(),
				},
				{
					BlobRef:   br4,
					Permanode: pn,
					Signer:    id.SignerBlobRef,
					Date:      br4Time.UTC(),
					Type:      "del-attribute",
					Attr:      "title",
					Value:     "pony",
				},
			}
			if !reflect.DeepEqual(claims, want) {
				t.Errorf("AppendClaims results differ.\n got: %v\nwant: %v",
					claims, want)
			}
		}
	}
}

func PathsOfSignerTarget(t *testing.T, initIdx func() *index.Index) {
	ctx := context.Background()
	id := NewIndexDeps(initIdx())
	id.Fataler = t
	defer id.DumpIndex(t)
	signer := id.SignerBlobRef
	pn := id.NewPermanode()
	t.Logf("uploaded permanode %q", pn)

	claim1 := id.SetAttribute(pn, "camliPath:somedir", "targ-123")
	claim1Time := id.LastTime().UTC()
	claim2 := id.SetAttribute(pn, "camliPath:with|pipe", "targ-124")
	claim2Time := id.LastTime().UTC()
	t.Logf("made path claims %q and %q", claim1, claim2)

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
		paths, err := id.Index.PathsOfSignerTarget(ctx, signer, blob.ParseOrZero(tt.blobref))
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
				"Path{Claim: %s, %v; Base: %s + Suffix \"somedir\" => Target targ-123}",
				claim1, claim1Time, pn)
			if g := p.String(); g != want {
				t.Errorf("claim wrong.\n got: %s\nwant: %s", g, want)
			}
		}
	}
	tests = []test{
		{"somedir", 1},
		{"with|pipe", 1},
		{"void", 0},
	}
	for _, tt := range tests {
		paths, err := id.Index.PathsLookup(ctx, id.SignerBlobRef, pn, tt.blobref)
		if err != nil {
			t.Fatalf("PathsLookup(%q): %v", tt.blobref, err)
		}
		if len(paths) != tt.want {
			t.Fatalf("PathsLookup(%q) got %d results; want %d",
				tt.blobref, len(paths), tt.want)
		}
		if tt.blobref == "with|pipe" {
			p := paths[0]
			want := fmt.Sprintf(
				"Path{Claim: %s, %s; Base: %s + Suffix \"with|pipe\" => Target targ-124}",
				claim2, claim2Time, pn)
			if g := p.String(); g != want {
				t.Errorf("claim wrong.\n got: %s\nwant: %s", g, want)
			}
		}
	}

	// now test deletions
	// Delete an existing value
	claim3 := id.Delete(claim2)
	t.Logf("claim %q deletes path claim %q", claim3, claim2)
	tests = []test{
		{"targ-123", 1},
		{"targ-124", 0},
		{"targ-125", 0},
	}
	for _, tt := range tests {
		signer := id.SignerBlobRef
		paths, err := id.Index.PathsOfSignerTarget(ctx, signer, blob.ParseOrZero(tt.blobref))
		if err != nil {
			t.Fatalf("PathsOfSignerTarget(%q): %v", tt.blobref, err)
		}
		if len(paths) != tt.want {
			t.Fatalf("PathsOfSignerTarget(%q) got %d results; want %d",
				tt.blobref, len(paths), tt.want)
		}
	}
	tests = []test{
		{"somedir", 1},
		{"with|pipe", 0},
		{"void", 0},
	}
	for _, tt := range tests {
		paths, err := id.Index.PathsLookup(ctx, id.SignerBlobRef, pn, tt.blobref)
		if err != nil {
			t.Fatalf("PathsLookup(%q): %v", tt.blobref, err)
		}
		if len(paths) != tt.want {
			t.Fatalf("PathsLookup(%q) got %d results; want %d",
				tt.blobref, len(paths), tt.want)
		}
	}

	// recreate second path, and test if the previous deletion of it
	// is indeed ignored.
	claim4 := id.Delete(claim3)
	t.Logf("delete claim %q deletes claim %q, which should undelete %q", claim4, claim3, claim2)
	tests = []test{
		{"targ-123", 1},
		{"targ-124", 1},
		{"targ-125", 0},
	}
	for _, tt := range tests {
		signer := id.SignerBlobRef
		paths, err := id.Index.PathsOfSignerTarget(ctx, signer, blob.ParseOrZero(tt.blobref))
		if err != nil {
			t.Fatalf("PathsOfSignerTarget(%q): %v", tt.blobref, err)
		}
		if len(paths) != tt.want {
			t.Fatalf("PathsOfSignerTarget(%q) got %d results; want %d",
				tt.blobref, len(paths), tt.want)
		}
		// and check the modtime too
		if tt.blobref == "targ-124" {
			p := paths[0]
			want := fmt.Sprintf(
				"Path{Claim: %s, %v; Base: %s + Suffix \"with|pipe\" => Target targ-124}",
				claim2, claim2Time, pn)
			if g := p.String(); g != want {
				t.Errorf("claim wrong.\n got: %s\nwant: %s", g, want)
			}
		}
	}
	tests = []test{
		{"somedir", 1},
		{"with|pipe", 1},
		{"void", 0},
	}
	for _, tt := range tests {
		paths, err := id.Index.PathsLookup(ctx, id.SignerBlobRef, pn, tt.blobref)
		if err != nil {
			t.Fatalf("PathsLookup(%q): %v", tt.blobref, err)
		}
		if len(paths) != tt.want {
			t.Fatalf("PathsLookup(%q) got %d results; want %d",
				tt.blobref, len(paths), tt.want)
		}
		// and check that modtime is now claim4Time
		if tt.blobref == "with|pipe" {
			p := paths[0]
			want := fmt.Sprintf(
				"Path{Claim: %s, %s; Base: %s + Suffix \"with|pipe\" => Target targ-124}",
				claim2, claim2Time, pn)
			if g := p.String(); g != want {
				t.Errorf("claim wrong.\n got: %s\nwant: %s", g, want)
			}
		}
	}
}

func Files(t *testing.T, initIdx func() *index.Index) {
	ctx := context.Background()
	id := NewIndexDeps(initIdx())
	id.Fataler = t
	fileTime := time.Unix(1361250375, 0)
	fileRef, wholeRef := id.UploadFile("foo.html", "<html>I am an html file.</html>", fileTime)
	t.Logf("uploaded fileref %q, wholeRef %q", fileRef, wholeRef)
	id.DumpIndex(t)

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
		want := index.WholeRefToFile{wholeRef.String(): []blob.Ref{fileRef}}
		if !reflect.DeepEqual(refs, want) {
			t.Errorf("ExistingFileSchemas got = %#v, want %#v", refs, want)
		}
	}

	// FileInfo
	{
		key := fmt.Sprintf("fileinfo|%s", fileRef)
		if g, e := id.Get(key), "31|foo.html|text%2Fhtml|sha224-35ef6689bf1b7981fb44a033ddd704ff28440779a11d927f2322bac7"; g != e {
			t.Fatalf("%q = %q, want %q", key, g, e)
		}

		fi, err := id.Index.GetFileInfo(ctx, fileRef)
		if err != nil {
			t.Fatalf("GetFileInfo = %v", err)
		}
		if got, want := fi.Size, int64(31); got != want {
			t.Errorf("Size = %d, want %d", got, want)
		}
		if got, want := fi.FileName, "foo.html"; got != want {
			t.Errorf("FileName = %q, want %q", got, want)
		}
		if got, want := fi.MIMEType, "text/html"; got != want {
			t.Errorf("MIMEType = %q, want %q", got, want)
		}
		if got, want := fi.Time, fileTime; !got.Time().Equal(want) {
			t.Errorf("Time = %v; want %v", got, want)
		}
		if got, want := fi.WholeRef, blob.MustParse("sha224-35ef6689bf1b7981fb44a033ddd704ff28440779a11d927f2322bac7"); got != want {
			t.Errorf("WholeRef = %v; want %v", got, want)
		}
	}
}

func EdgesTo(t *testing.T, initIdx func() *index.Index) {
	idx := initIdx()
	id := NewIndexDeps(idx)
	id.Fataler = t
	defer id.DumpIndex(t)

	// pn1 ---member---> pn2
	pn1 := id.NewPermanode()
	pn2 := id.NewPermanode()
	claim1 := id.AddAttribute(pn1, "camliMember", pn2.String())

	t.Logf("edge %s --> %s", pn1, pn2)

	// Look for pn1
	{
		edges, err := idx.EdgesTo(pn2, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(edges) != 1 {
			t.Fatalf("num edges = %d; want 1", len(edges))
		}
		wantEdge := &camtypes.Edge{
			From:     pn1,
			To:       pn2,
			FromType: "permanode",
		}
		if got, want := edges[0].String(), wantEdge.String(); got != want {
			t.Errorf("Wrong edge.\n GOT: %v\nWANT: %v", got, want)
		}
	}

	// Delete claim -> break edge relationship.
	del1 := id.Delete(claim1)
	t.Logf("del claim %q deletes claim %q, breaks link between p1 and p2", del1, claim1)
	// test that we can't find anymore pn1 from pn2
	{
		edges, err := idx.EdgesTo(pn2, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(edges) != 0 {
			t.Fatalf("num edges = %d; want 0", len(edges))
		}
	}

	// Undelete, should restore the link.
	del2 := id.Delete(del1)
	t.Logf("del claim %q deletes del claim %q, restores link between p1 and p2", del2, del1)
	{
		edges, err := idx.EdgesTo(pn2, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(edges) != 1 {
			t.Fatalf("num edges = %d; want 1", len(edges))
		}
		wantEdge := &camtypes.Edge{
			From:     pn1,
			To:       pn2,
			FromType: "permanode",
		}
		if got, want := edges[0].String(), wantEdge.String(); got != want {
			t.Errorf("Wrong edge.\n GOT: %v\nWANT: %v", got, want)
		}
	}
}

func Delete(t *testing.T, initIdx func() *index.Index) {
	ctx := context.Background()
	idx := initIdx()
	id := NewIndexDeps(idx)
	id.Fataler = t
	defer id.DumpIndex(t)
	pn1 := id.NewPermanode()
	t.Logf("uploaded permanode %q", pn1)
	cl1 := id.SetAttribute(pn1, "tag", "foo1")
	cl1Time := id.LastTime()
	t.Logf("set attribute %q", cl1)

	// delete pn1
	delpn1 := id.Delete(pn1)
	t.Logf("del claim %q deletes %q", delpn1, pn1)
	deleted := idx.IsDeleted(pn1)
	if !deleted {
		t.Fatal("pn1 should be deleted")
	}

	// and try to find it with SearchPermanodesWithAttr (which should not work)
	{
		ch := make(chan blob.Ref, 10)
		req := &camtypes.PermanodeByAttrRequest{
			Signer:    id.SignerBlobRef,
			Attribute: "tag",
			Query:     "foo1"}
		err := id.Index.SearchPermanodesWithAttr(ctx, ch, req)
		if err != nil {
			t.Fatalf("SearchPermanodesWithAttr = %v", err)
		}
		var got []blob.Ref
		for r := range ch {
			got = append(got, r)
		}
		want := []blob.Ref{}
		if len(got) != len(want) {
			t.Errorf("id.Index.SearchPermanodesWithAttr gives %q, want %q", got, want)
		}
	}

	// delete pn1 again with another claim
	delpn1bis := id.Delete(pn1)
	t.Logf("del claim %q deletes %q a second time", delpn1bis, pn1)
	deleted = idx.IsDeleted(pn1)
	if !deleted {
		t.Fatal("pn1 should be deleted")
	}

	// verify that deleting delpn1 is not enough to make pn1 undeleted
	del2 := id.Delete(delpn1)
	t.Logf("delete claim %q deletes %q, which should not yet revive %q", del2, delpn1, pn1)
	deleted = idx.IsDeleted(pn1)
	if !deleted {
		t.Fatal("pn1 should not yet be undeleted")
	}
	// we should not yet be able to find it again with SearchPermanodesWithAttr
	{
		ch := make(chan blob.Ref, 10)
		req := &camtypes.PermanodeByAttrRequest{
			Signer:    id.SignerBlobRef,
			Attribute: "tag",
			Query:     "foo1"}
		err := id.Index.SearchPermanodesWithAttr(ctx, ch, req)
		if err != nil {
			t.Fatalf("SearchPermanodesWithAttr = %v", err)
		}
		var got []blob.Ref
		for r := range ch {
			got = append(got, r)
		}
		want := []blob.Ref{}
		if len(got) != len(want) {
			t.Errorf("id.Index.SearchPermanodesWithAttr gives %q, want %q", got, want)
		}
	}

	// delete delpn1bis as well -> should undelete pn1
	del2bis := id.Delete(delpn1bis)
	t.Logf("delete claim %q deletes %q, which should revive %q", del2bis, delpn1bis, pn1)
	deleted = idx.IsDeleted(pn1)
	if deleted {
		t.Fatal("pn1 should be undeleted")
	}
	// we should now be able to find it again with SearchPermanodesWithAttr
	{
		ch := make(chan blob.Ref, 10)
		req := &camtypes.PermanodeByAttrRequest{
			Signer:    id.SignerBlobRef,
			Attribute: "tag",
			Query:     "foo1"}
		err := id.Index.SearchPermanodesWithAttr(ctx, ch, req)
		if err != nil {
			t.Fatalf("SearchPermanodesWithAttr = %v", err)
		}
		var got []blob.Ref
		for r := range ch {
			got = append(got, r)
		}
		want := []blob.Ref{pn1}
		if len(got) < 1 || got[0].String() != want[0].String() {
			t.Errorf("id.Index.SearchPermanodesWithAttr gives %q, want %q", got, want)
		}
	}

	// Delete cl1
	del3 := id.Delete(cl1)
	t.Logf("del claim %q deletes claim %q", del3, cl1)
	deleted = idx.IsDeleted(cl1)
	if !deleted {
		t.Fatal("cl1 should be deleted")
	}
	// we should not find anything with SearchPermanodesWithAttr
	{
		ch := make(chan blob.Ref, 10)
		req := &camtypes.PermanodeByAttrRequest{
			Signer:    id.SignerBlobRef,
			Attribute: "tag",
			Query:     "foo1"}
		err := id.Index.SearchPermanodesWithAttr(ctx, ch, req)
		if err != nil {
			t.Fatalf("SearchPermanodesWithAttr = %v", err)
		}
		var got []blob.Ref
		for r := range ch {
			got = append(got, r)
		}
		want := []blob.Ref{}
		if len(got) != len(want) {
			t.Errorf("id.Index.SearchPermanodesWithAttr gives %q, want %q", got, want)
		}
	}
	// and now check that AppendClaims finds nothing for pn
	{
		claims, err := id.Index.AppendClaims(ctx, nil, pn1, KeyID, "")
		if err != nil {
			t.Errorf("AppendClaims = %v", err)
		} else {
			want := []camtypes.Claim{}
			if len(claims) != len(want) {
				t.Errorf("id.Index.AppendClaims gives %q, want %q", claims, want)
			}
		}
	}

	// undelete cl1
	del4 := id.Delete(del3)
	t.Logf("del claim %q deletes del claim %q, which should undelete %q", del4, del3, cl1)
	// We should now be able to find it again with both methods
	{
		ch := make(chan blob.Ref, 10)
		req := &camtypes.PermanodeByAttrRequest{
			Signer:    id.SignerBlobRef,
			Attribute: "tag",
			Query:     "foo1"}
		err := id.Index.SearchPermanodesWithAttr(ctx, ch, req)
		if err != nil {
			t.Fatalf("SearchPermanodesWithAttr = %v", err)
		}
		var got []blob.Ref
		for r := range ch {
			got = append(got, r)
		}
		want := []blob.Ref{pn1}
		if len(got) < 1 || got[0].String() != want[0].String() {
			t.Errorf("id.Index.SearchPermanodesWithAttr gives %q, want %q", got, want)
		}
	}
	// and check that AppendClaims finds cl1, with the right modtime too
	{
		claims, err := id.Index.AppendClaims(ctx, nil, pn1, KeyID, "")
		if err != nil {
			t.Errorf("AppendClaims = %v", err)
		} else {
			want := []camtypes.Claim{
				{
					BlobRef:   cl1,
					Permanode: pn1,
					Signer:    id.SignerBlobRef,
					Date:      cl1Time.UTC(),
					Type:      "set-attribute",
					Attr:      "tag",
					Value:     "foo1",
				},
			}
			if !reflect.DeepEqual(claims, want) {
				t.Errorf("GetOwnerClaims results differ.\n got: %v\nwant: %v",
					claims, want)
			}
		}
	}
}

type searchResults []camtypes.RecentPermanode

func (s searchResults) String() string {
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "[%d search results: ", len(s))
	for _, r := range s {
		fmt.Fprintf(&buf, "{BlobRef: %s, Signer: %s, LastModTime: %d}",
			r.Permanode, r.Signer, r.LastModTime.Unix())
	}
	buf.WriteString("]")
	return buf.String()
}

func Reindex(t *testing.T, initIdx func() *index.Index) {
	idx := initIdx()
	id := NewIndexDeps(idx)
	id.Fataler = t
	defer id.DumpIndex(t)

	pn1 := id.NewPlannedPermanode("foo1")
	t.Logf("uploaded permanode %q", pn1)

	// delete pn1
	delpn1 := id.Delete(pn1)
	t.Logf("del claim %q deletes %q", delpn1, pn1)
	if deleted := idx.IsDeleted(pn1); !deleted {
		t.Fatal("pn1 should be deleted")
	}

	if err := id.Index.Reindex(); err != nil {
		t.Fatalf("reindexing failed: %v", err)
	}

	if deleted := idx.IsDeleted(pn1); !deleted {
		t.Fatal("pn1 should be deleted after reindexing")
	}
}

type enumArgs struct {
	ctx   context.Context
	dest  chan blob.SizedRef
	after string
	limit int
}

func checkEnumerate(idx *index.Index, want []blob.SizedRef, args *enumArgs) error {
	if args == nil {
		args = &enumArgs{}
	}
	if args.ctx == nil {
		args.ctx = context.TODO()
	}
	if args.dest == nil {
		args.dest = make(chan blob.SizedRef)
	}
	if args.limit == 0 {
		args.limit = 5000
	}
	errCh := make(chan error)
	go func() {
		errCh <- idx.EnumerateBlobs(args.ctx, args.dest, args.after, args.limit)
	}()
	for k, sbr := range want {
		got, ok := <-args.dest
		if !ok {
			return fmt.Errorf("could not enumerate blob %d", k)
		}
		if got != sbr {
			return fmt.Errorf("enumeration %d: got %v, wanted %v", k, got, sbr)
		}
	}
	_, ok := <-args.dest
	if ok {
		return errors.New("chan was not closed after enumeration")
	}
	return <-errCh
}

func checkStat(idx *index.Index, want []blob.SizedRef) error {
	pos := make(map[blob.Ref]int) // wanted ref => its position in want
	need := make(map[blob.Ref]bool)
	for i, sb := range want {
		pos[sb.Ref] = i
		need[sb.Ref] = true
	}

	input := make([]blob.Ref, len(want))
	for _, sbr := range want {
		input = append(input, sbr.Ref)
	}
	err := idx.StatBlobs(context.Background(), input, func(sb blob.SizedRef) error {
		if !sb.Valid() {
			return errors.New("StatBlobs func called with invalid/zero blob.SizedRef")
		}
		wantPos, ok := pos[sb.Ref]
		if !ok {
			return fmt.Errorf("StatBlobs func called with unrequested ref %v (size %d)", sb.Ref, sb.Size)
		}
		if !need[sb.Ref] {
			return fmt.Errorf("StatBlobs func called with ref %v multiple times", sb.Ref)
		}
		delete(need, sb.Ref)
		w := want[wantPos]
		if sb != w {
			return fmt.Errorf("StatBlobs returned %v; want %v", sb, w)
		}
		return nil
	})
	if err != nil {
		return err
	}
	for br := range need {
		return fmt.Errorf("didn't get stat result for %v", br)
	}
	return nil
}

func EnumStat(t *testing.T, initIdx func() *index.Index) {
	idx := initIdx()
	id := NewIndexDeps(idx)
	id.Fataler = t

	type step func() error

	// so we can refer to the added permanodes without using hardcoded blobRefs
	added := make(map[string]blob.Ref)

	stepAdd := func(contents string) step { // add the blob
		return func() error {
			pn := id.NewPlannedPermanode(contents)
			t.Logf("uploaded permanode %q", pn)
			added[contents] = pn
			return nil
		}
	}

	stepEnumCheck := func(want []blob.SizedRef, args *enumArgs) step { // check the blob
		return func() error {
			if err := checkEnumerate(idx, want, args); err != nil {
				return err
			}
			return nil
		}
	}

	missingBlob := blob.MustParse("sha224-00000000000000000000000000000000000000000000000000000000")
	stepDelete := func(toDelete blob.Ref) step {
		return func() error {
			del := id.Delete(missingBlob)
			t.Logf("added del claim %v to delete %v", del, toDelete)
			return nil
		}
	}

	stepStatCheck := func(want []blob.SizedRef) step {
		return func() error {
			if err := checkStat(idx, want); err != nil {
				return err
			}
			return nil
		}
	}

	for _, v := range []string{
		"foo",
		"barr",
		"bazzz",
	} {
		stepAdd(v)()
	}
	foo := blob.SizedRef{ // sha224-c4c574a741e1728662095a720f8a98158706d9fe4fc5d565cca77f61
		Ref:  blob.MustParse(added["foo"].String()),
		Size: 552,
	}
	bar := blob.SizedRef{ // sha224-8254816368fb26b5b0f58b312c39e537ed93cbfe04db4f14e7559f93;
		Ref:  blob.MustParse(added["barr"].String()),
		Size: 553,
	}
	baz := blob.SizedRef{ // sha224-fee45fffbf949219eb4db6f393dd206a5371709fbf093218e6f272ec;
		Ref:  blob.MustParse(added["bazzz"].String()),
		Size: 554,
	}
	delMissing := blob.SizedRef{ // sha224-7fcb38ca16606814881678d30874f1b59953bc522570be8645da1d14
		Ref:  blob.MustParse("sha224-7fcb38ca16606814881678d30874f1b59953bc522570be8645da1d14"),
		Size: 685,
	}

	if err := stepEnumCheck([]blob.SizedRef{bar, foo, baz}, nil)(); err != nil {
		t.Fatalf("first enum, testing order: %v", err)
	}

	// Now again, but skipping bar's blob
	if err := stepEnumCheck([]blob.SizedRef{foo, baz},
		&enumArgs{
			after: added["barr"].String(),
		},
	)(); err != nil {
		t.Fatalf("second enum, testing skipping with after: %v", err)
	}

	// Now add a delete claim with a missing dep, which should add an "have" row in the old format,
	// i.e. without the "|indexed" suffix. So we can test if we're still compatible with old rows.
	stepDelete(missingBlob)()
	if err := stepEnumCheck([]blob.SizedRef{delMissing, bar, foo, baz}, nil)(); err != nil {
		t.Fatalf("third enum, testing old \"have\" row compat: %v", err)
	}

	if err := stepStatCheck([]blob.SizedRef{delMissing, foo, bar, baz})(); err != nil {
		t.Fatalf("stat check: %v", err)
	}
}

// MustNew wraps index.New and fails with a Fatal error on t if New
// returns an error.
func MustNew(t *testing.T, s sorted.KeyValue) *index.Index {
	ix, err := index.New(s)
	if err != nil {
		t.Fatalf("Error creating index: %v", err)
	}
	return ix
}
