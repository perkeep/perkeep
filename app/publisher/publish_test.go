/*
Copyright 2014 The Camlistore Authors

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
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	camliClient "camlistore.org/pkg/client"
	"camlistore.org/pkg/httputil"
	"camlistore.org/pkg/index"
	"camlistore.org/pkg/index/indextest"
	"camlistore.org/pkg/search"
)

type publishURLTest struct {
	path            string // input
	subject, subres string // expected
}

var publishURLTests []publishURLTest

func setupContent(rootName string) *indextest.IndexDeps {
	idx := index.NewMemoryIndex()
	idxd := indextest.NewIndexDeps(idx)

	picNode := idxd.NewPlannedPermanode("picpn-1234")                                             // sha1-f5e90fcc50a79caa8b22a4aa63ba92e436cab9ec
	galRef := idxd.NewPlannedPermanode("gal-1234")                                                // sha1-2bdf2053922c3dfa70b01a4827168fce1c1df691
	rootRef := idxd.NewPlannedPermanode("root-abcd")                                              // sha1-dbb3e5f28c7e01536d43ce194f3dd7b921b8460d
	camp0 := idxd.NewPlannedPermanode("picpn-9876543210")                                         // sha1-2d473e07ca760231dd82edeef4019d5b7d0ccb42
	camp1 := idxd.NewPlannedPermanode("picpn-9876543211")                                         // sha1-961b700536d5151fc1f3920955cc92767572a064
	camp0f, _ := idxd.UploadFile("picfile-f00ff00f00a5.jpg", "picfile-f00ff00f00a5", time.Time{}) // sha1-01dbcb193fc789033fb2d08ed22abe7105b48640
	camp1f, _ := idxd.UploadFile("picfile-f00ff00f00b6.jpg", "picfile-f00ff00f00b6", time.Time{}) // sha1-1213ec17a42cc51bdeb95ff91ac1b5fc5157740f

	idxd.SetAttribute(rootRef, "camliRoot", rootName)
	idxd.SetAttribute(rootRef, "camliPath:singlepic", picNode.String())
	idxd.SetAttribute(picNode, "title", "picnode without a pic?")
	idxd.SetAttribute(rootRef, "camliPath:camping", galRef.String())
	idxd.AddAttribute(galRef, "camliMember", camp0.String())
	idxd.AddAttribute(galRef, "camliMember", camp1.String())
	idxd.SetAttribute(camp0, "camliContent", camp0f.String())
	idxd.SetAttribute(camp1, "camliContent", camp1f.String())

	publishURLTests = []publishURLTest{
		// URL to a single picture permanode (returning its HTML wrapper page)
		{
			path:    "/pics/singlepic",
			subject: picNode.String(),
		},

		// URL to a gallery permanode (returning its HTML wrapper page)
		{
			path:    "/pics/camping",
			subject: galRef.String(),
		},

		// URL to a picture permanode within a gallery (following one hop, returning HTML)
		{
			path:    "/pics/camping/-/h2d473e07ca",
			subject: camp0.String(),
		},

		// URL to a gallery -> picture permanode -> its file
		// (following two hops, returning HTML)
		{
			path:    "/pics/camping/-/h2d473e07ca/h01dbcb193f",
			subject: camp0f.String(),
		},

		// URL to a gallery -> picture permanode -> its file
		// (following two hops, returning the file download)
		{
			path:    "/pics/camping/-/h2d473e07ca/h01dbcb193f/=f/marshmallow.jpg",
			subject: camp0f.String(),
			subres:  "/=f/marshmallow.jpg",
		},

		// URL to a gallery -> picture permanode -> its file
		// (following two hops, returning the file, scaled as an image)
		{
			path:    "/pics/camping/-/h961b700536/h1213ec17a4/=i/marshmallow.jpg?mw=200&mh=200",
			subject: camp1f.String(),
			subres:  "/=i/marshmallow.jpg",
		},

		// Path to a static file in the root.
		// TODO: ditch these and use content-addressable javascript + css, having
		// the server digest them on start, or rather part of fileembed. This is
		// a short-term hack to unblock Lindsey.
		{
			path:    "/pics/=s/pics.js",
			subject: "",
			subres:  "/=s/pics.js",
		},
	}

	return idxd
}

type fakeClient struct {
	*camliClient.Client // for blob.Fetcher
	sh                  *search.Handler
}

func (fc *fakeClient) Search(req *search.SearchQuery) (*search.SearchResult, error) {
	return fc.sh.Query(req)
}

func (fc *fakeClient) Describe(req *search.DescribeRequest) (*search.DescribeResponse, error) {
	return fc.sh.Describe(req)
}

func (fc *fakeClient) GetJSON(url string, data interface{}) error {
	// no need to implement
	return nil
}

func (fc *fakeClient) Post(url string, bodyType string, body io.Reader) error {
	// no need to implement
	return nil
}

func TestPublishURLs(t *testing.T) {
	rootName := "foo"
	idxd := setupContent(rootName)
	sh := search.NewHandler(idxd.Index, idxd.SignerBlobRef)
	corpus, err := idxd.Index.KeepInMemory()
	if err != nil {
		t.Fatalf("error slurping index to memory: %v", err)
	}
	sh.SetCorpus(corpus)
	cl := camliClient.New("http://whatever.fake")
	fcl := &fakeClient{cl, sh}
	ph := &publishHandler{
		rootName: rootName,
		cl:       fcl,
	}
	if err := ph.initRootNode(); err != nil {
		t.Fatalf("initRootNode: %v", err)
	}

	for ti, tt := range publishURLTests {
		rw := httptest.NewRecorder()
		if !strings.HasPrefix(tt.path, "/pics/") {
			panic("expected /pics/ prefix on " + tt.path)
		}
		req, _ := http.NewRequest("GET", "http://foo.com"+tt.path, nil)

		pfxh := &httputil.PrefixHandler{
			Prefix: "/pics/",
			Handler: http.HandlerFunc(func(_ http.ResponseWriter, req *http.Request) {
				pr, err := ph.NewRequest(rw, req)
				if err != nil {
					t.Fatalf("test #%d, NewRequest: %v", ti, err)
				}

				err = pr.findSubject()
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

func TestPublishMembers(t *testing.T) {
	rootName := "foo"
	idxd := setupContent(rootName)

	sh := search.NewHandler(idxd.Index, idxd.SignerBlobRef)
	corpus, err := idxd.Index.KeepInMemory()
	if err != nil {
		t.Fatalf("error slurping index to memory: %v", err)
	}
	sh.SetCorpus(corpus)
	cl := camliClient.New("http://whatever.fake")
	fcl := &fakeClient{cl, sh}
	ph := &publishHandler{
		rootName: rootName,
		cl:       fcl,
	}

	rw := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "http://foo.com/pics", nil)

	pfxh := &httputil.PrefixHandler{
		Prefix: "/pics/",
		Handler: http.HandlerFunc(func(_ http.ResponseWriter, req *http.Request) {
			pr, err := ph.NewRequest(rw, req)
			if err != nil {
				t.Fatalf("NewRequest: %v", err)
			}

			res, err := pr.ph.deepDescribe(pr.subject)
			if err != nil {
				t.Fatalf("deepDescribe: %v", err)
			}

			members, err := pr.subjectMembers(res.Meta)
			if len(members.Members) != 2 {
				t.Errorf("Expected two members in publish root (one camlipath, one camlimember), got %d", len(members.Members))
			}
		}),
	}
	pfxh.ServeHTTP(rw, req)
}
