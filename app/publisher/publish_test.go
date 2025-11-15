/*
Copyright 2014 The Perkeep Authors

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
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"perkeep.org/internal/httputil"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/index"
	"perkeep.org/pkg/index/indextest"
	"perkeep.org/pkg/search"
)

type publishURLTest struct {
	path            string // input
	subject, subres string // expected
}

var publishURLTests []publishURLTest

func setupContent(rootName string) *indextest.IndexDeps {
	idx := index.NewMemoryIndex()
	idxd := indextest.NewIndexDeps(idx)

	// sha224-fbc46cf344988fbb75328e45d2b98936c22d967765a796d4866f07c5
	picNode := idxd.NewPlannedPermanode("picpn-1234")
	// sha224-ee98f50055efb1ac808c87888ed7fdcb3e9ad91c843953ba0b4edf89
	galRef := idxd.NewPlannedPermanode("gal-1234")
	// sha224-bc11ed943b436f9627e595003920bc8f940a47fc8cff4ef8378546b9
	rootRef := idxd.NewPlannedPermanode("root-abcd")
	// sha224-58bbb7bf2b6c05197b886c7408868dfc5f49fa9bec7302135ded83aa
	camp0 := idxd.NewPlannedPermanode("picpn-9876543210")
	// sha224-11c2cf95528d78fa727cdd253caf6ab7acd84c83a47e0e6395ff4708
	camp1 := idxd.NewPlannedPermanode("picpn-9876543211")
	// sha224-ed826bd5198e0dc536f385f8677da8f236e465a0d69704c16cb8afd6
	camp0f, _ := idxd.UploadFile("picfile-f00ff00f00a5.jpg", "picfile-f00ff00f00a5", time.Time{})
	// sha224-697b84a9b0ccb8705c3ae02c4fe659605e46255eefbd65328a5bcfdf
	camp1f, _ := idxd.UploadFile("picfile-f00ff00f00b6.jpg", "picfile-f00ff00f00b6", time.Time{})

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
			path:    "/pics/camping/-/h58bbb7bf2b",
			subject: camp0.String(),
		},

		// URL to a gallery -> picture permanode -> its file
		// (following two hops, returning HTML)
		{
			path:    "/pics/camping/-/h58bbb7bf2b/hed826bd519",
			subject: camp0f.String(),
		},

		// URL to a gallery -> picture permanode -> its file
		// (following two hops, returning the file download)
		{
			path:    "/pics/camping/-/h58bbb7bf2b/hed826bd519/=f/marshmallow.jpg",
			subject: camp0f.String(),
			subres:  "/=f/marshmallow.jpg",
		},

		// URL to a gallery -> picture permanode -> its file
		// (following two hops, returning the file, scaled as an image)
		{
			path:    "/pics/camping/-/h11c2cf9552/h697b84a9b0/=i/marshmallow.jpg?mw=200&mh=200",
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
	sh *search.Handler
}

func (fc *fakeClient) Query(ctx context.Context, req *search.SearchQuery) (*search.SearchResult, error) {
	return fc.sh.Query(ctx, req)
}

func (fc *fakeClient) Search(ctx context.Context, req *search.SearchQuery) (*search.SearchResult, error) {
	return fc.sh.Query(ctx, req)
}

func (fc *fakeClient) Describe(ctx context.Context, req *search.DescribeRequest) (*search.DescribeResponse, error) {
	return fc.sh.Describe(ctx, req)
}

func (fc *fakeClient) GetJSON(ctx context.Context, url string, data any) error {
	// no need to implement
	return nil
}

func (fc *fakeClient) Post(ctx context.Context, url string, bodyType string, body io.Reader) error {
	// no need to implement
	return nil
}

func (fc *fakeClient) Fetch(context.Context, blob.Ref) (blob io.ReadCloser, size uint32, err error) {
	return
}

func TestPublishURLs(t *testing.T) {
	rootName := "foo"
	idxd := setupContent(rootName)
	ownerRef := indextest.PubKey
	owner := index.NewOwner(indextest.KeyID, ownerRef.BlobRef())
	sh := search.NewHandler(idxd.Index, owner)
	corpus, err := idxd.Index.KeepInMemory()
	if err != nil {
		t.Fatalf("error slurping index to memory: %v", err)
	}
	sh.SetCorpus(corpus)
	fcl := &fakeClient{sh}
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
			Prefix: "/",
			Handler: http.HandlerFunc(func(_ http.ResponseWriter, req *http.Request) {
				// Because the app handler strips the prefix before passing it on to the app
				req.URL.Path = strings.TrimPrefix(req.URL.Path, "/pics/")
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

	ownerRef := indextest.PubKey
	owner := index.NewOwner(indextest.KeyID, ownerRef.BlobRef())
	sh := search.NewHandler(idxd.Index, owner)
	corpus, err := idxd.Index.KeepInMemory()
	if err != nil {
		t.Fatalf("error slurping index to memory: %v", err)
	}
	sh.SetCorpus(corpus)
	fcl := &fakeClient{sh}
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
			if err != nil {
				t.Errorf("subjectMembers: %v", err)
			}
			if len(members.Members) != 2 {
				t.Errorf("Expected two members in publish root (one camlipath, one camlimember), got %d", len(members.Members))
			}
		}),
	}
	pfxh.ServeHTTP(rw, req)
}
