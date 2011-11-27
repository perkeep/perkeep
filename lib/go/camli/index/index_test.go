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
	"testing"

	"camli/blobref"
	"camli/jsonsign"
	"camli/schema"
	"camli/test"
)

type IndexDeps struct {
	Index *Index

	// Following three needed for signing:
	PublicKeyFetcher *test.Fetcher
	EntityFetcher    jsonsign.EntityFetcher // fetching decrypted openpgp entities
	SignerBlobRef    *blobref.BlobRef
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
		PublicKeyFetcher: new(test.Fetcher),
		EntityFetcher: &jsonsign.CachingEntityFetcher{
			Fetcher: &jsonsign.FileEntityFetcher{File: secretRingFile},
		},
		SignerBlobRef: pubKey.BlobRef(),
	}
	// Add dev-camput's test key public key, keyid 26F5ABDA,
	// blobref sha1-ad87ca5c78bd0ce1195c46f7c98e6025abbaf007
	if id.SignerBlobRef.String() != "sha1-ad87ca5c78bd0ce1195c46f7c98e6025abbaf007" {
		panic("unexpected signer blobref")
	}
	id.PublicKeyFetcher.AddBlob(pubKey)
	return id
}

func TestIndexPopulation(t *testing.T) {
	id := NewIndexDeps()
	pn := id.NewPermanode()
	t.Logf("uploaded permanode %q", pn)
	it := id.Index.s.Find("")
	for it.Next() {
		t.Logf("  %q = %q", it.Key(), it.Value())
	}
	if err := it.Close(); err != nil {
		t.Fatalf("iterator close = %v", err)
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
