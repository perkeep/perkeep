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

package main

import (
	"http"
	"http/httptest"
	"strings"
	"testing"

	"camli/blobref"
	"camli/search"
	"camli/test"
)

type publishURLTest struct {
	path string // input

	subject string // expected
}

var publishURLTests = []publishURLTest{
	{
		path:    "/pics/singlepic",
		subject: "picpn-123",
	},
	{
		path:    "/pics/camping",
		subject: "gal-123",
	},
	{
		path:    "/pics/camping/-/m9876543210",
		subject: "picpn-98765432100",
	},
}

func TestPublishURLs(t *testing.T) {
	owner := blobref.MustParse("owner-123")
	picNode := blobref.MustParse("picpn-123")
	galRef := blobref.MustParse("gal-123")
	rootRef := blobref.MustParse("root-abc")
	camp0 := blobref.MustParse("picpn-98765432100")
	camp1 := blobref.MustParse("picpn-98765432111")
	camp0f := blobref.MustParse("picfile-98765432f00")
	camp1f := blobref.MustParse("picfile-98765432f10")

	rootName := "foo"

	for ti, tt := range publishURLTests {
		idx := test.NewFakeIndex()
		idx.AddSignerAttrValue(owner, "camliRoot", rootName, rootRef)
		sh := search.NewHandler(idx, owner)
		ph := &PublishHandler{
			RootName: rootName,
			Search:   sh,
		}
		rw := httptest.NewRecorder()
		if !strings.HasPrefix(tt.path, "/pics/") {
			panic("expected /pics/ prefix on " + tt.path)
		}
		req, _ := http.NewRequest("GET", "http://foo.com"+tt.path, nil)
		req.Header.Set("X-PrefixHandler-PathBase", "/pics/")
		req.Header.Set("X-PrefixHandler-PathSuffix", tt.path[len("/pics/"):])

		idx.AddMeta(owner, "text/x-openpgp-public-key", 100)
		for _, br := range []*blobref.BlobRef{picNode, galRef, rootRef, camp0, camp1} {
			idx.AddMeta(br, "application/json; camliType=permanode", 100)
		}
		for _, br := range []*blobref.BlobRef{camp0f, camp1f} {
			idx.AddMeta(br, "application/json; camliType=file", 100)
		}

		idx.AddClaim(owner, rootRef, "set-attribute", "camliPath:singlepic", picNode.String())
		idx.AddClaim(owner, rootRef, "set-attribute", "camliPath:camping", galRef.String())
		idx.AddClaim(owner, galRef, "add-attribute", "camliMember", camp0.String())
		idx.AddClaim(owner, galRef, "add-attribute", "camliMember", camp1.String())
		idx.AddClaim(owner, camp0, "set-attribute", "camliContent", camp0f.String())
		idx.AddClaim(owner, camp1, "set-attribute", "camliContent", camp1f.String())
		pr := ph.NewRequest(rw, req)

		err := pr.findSubject()
		if err != nil {
			t.Errorf("test #%d, findSubject: %v", ti, err)
			continue
		}
		if pr.subject.String() != tt.subject {
			t.Errorf("test #%d, got subject %q, want %q", ti, pr.subject, tt.subject)
		}
	}
}
