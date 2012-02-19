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

package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"camlistore.org/pkg/blobref"
	"camlistore.org/pkg/httputil"
	"camlistore.org/pkg/search"
	"camlistore.org/pkg/test"
)

type publishURLTest struct {
	path            string // input
	subject, subres string // expected
}

var publishURLTests = []publishURLTest{
	// URL to a single picture permanoe (returning its HTML wrapper page)
	{
		path:    "/pics/singlepic",
		subject: "picpn-123",
	},

	// URL to a gallery permanode (returning its HTML wrapper page)
	{
		path:    "/pics/camping",
		subject: "gal-123",
	},

	// URL to a picture permanode within a gallery (following one hop, returning HTML)
	{
		path:    "/pics/camping/-/h9876543210",
		subject: "picpn-98765432100",
	},

	// URL to a gallery -> picture permanode -> its file
	// (following two hops, returning HTML)
	{
		path:    "/pics/camping/-/h9876543210/hf00f00f00a",
		subject: "picfile-f00f00f00a5",
	},

	// URL to a gallery -> picture permanode -> its file
	// (following two hops, returning the file download)
	{
		path:    "/pics/camping/-/h9876543210/hf00f00f00a/=f/marshmallow.jpg",
		subject: "picfile-f00f00f00a5",
		subres:  "/=f/marshmallow.jpg",
	},

	// URL to a gallery -> picture permanode -> its file
	// (following two hops, returning the file, scaled as an image)
	{
		path:    "/pics/camping/-/h9876543210/hf00f00f00a/=i/marshmallow.jpg?mw=200&mh=200",
		subject: "picfile-f00f00f00a5",
		subres:  "/=i/marshmallow.jpg",
	},

	// Path to a static file in the root.
	// TODO: ditch these and use content-addressable javascript + css, having
	// the server digest them on start, or rather part of fileembed. This is
	// a short-term hack to unblock Lindsey.
	{
		path:    "/pics/=s/camli.js",
		subject: "",
		subres:  "/=s/camli.js",
	},
}

func TestPublishURLs(t *testing.T) {
	owner := blobref.MustParse("owner-123")
	picNode := blobref.MustParse("picpn-123")
	galRef := blobref.MustParse("gal-123")
	rootRef := blobref.MustParse("root-abc")
	camp0 := blobref.MustParse("picpn-98765432100")
	camp1 := blobref.MustParse("picpn-98765432111")
	camp0f := blobref.MustParse("picfile-f00f00f00a5")
	camp1f := blobref.MustParse("picfile-f00f00f00b6")

	rootName := "foo"

	for ti, tt := range publishURLTests {
		idx := test.NewFakeIndex()
		idx.AddSignerAttrValue(owner, "camliRoot", rootName, rootRef)
		sh := search.NewHandler(idx, owner)
		ph := &PublishHandler{
			RootName: rootName,
			Search:   sh,
		}

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

		rw := httptest.NewRecorder()
		if !strings.HasPrefix(tt.path, "/pics/") {
			panic("expected /pics/ prefix on " + tt.path)
		}
		req, _ := http.NewRequest("GET", "http://foo.com"+tt.path, nil)

		pfxh := &httputil.PrefixHandler{
			Prefix: "/pics/",
			Handler: http.HandlerFunc(func(_ http.ResponseWriter, req *http.Request) {
				pr := ph.NewRequest(rw, req)

				err := pr.findSubject()
				if tt.subject != "" {
					if err != nil {
						t.Errorf("test #%d, findSubject: %v", ti, err)
						return
					}
					if pr.subject.String() != tt.subject {
						t.Errorf("test #%d, got subject %q, want %q", ti, pr.subject, tt.subject)
					}
				}
				if pr.subres != tt.subres {
					t.Errorf("test #%d, got subres %q, want %q", ti, pr.subres, tt.subres)
				}
			}),
		}
		pfxh.ServeHTTP(rw, req)
	}
}
