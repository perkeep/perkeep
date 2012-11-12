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

package search_test

import (
	. "camlistore.org/pkg/search"

	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"camlistore.org/pkg/blobref"
	"camlistore.org/pkg/index"
	"camlistore.org/pkg/index/indextest"
	"camlistore.org/pkg/test"
)

// An indexOwnerer is something that knows who owns the index.
// It is implemented by indexAndOwner for use by TestHandler.
type indexOwnerer interface {
	IndexOwner() *blobref.BlobRef
}

type indexAndOwner struct {
	Index
	owner *blobref.BlobRef
}

func (io indexAndOwner) IndexOwner() *blobref.BlobRef {
	return io.owner
}

type handlerTest struct {
	// setup is responsible for populating the index before the
	// handler is invoked.
	//
	// A FakeIndex is constructed and provided to setup and is
	// generally then returned as the Index to use, but an
	// alternate Index may be returned instead, in which case the
	// FakeIndex is not used.
	setup func(fi *test.FakeIndex) Index

	query string // the HTTP path + optional query suffix after "camli/search/"

	want map[string]interface{}
}

var owner = blobref.MustParse("abcown-123")

func parseJSON(s string) map[string]interface{} {
	m := make(map[string]interface{})
	err := json.Unmarshal([]byte(s), &m)
	if err != nil {
		panic(err)
	}
	return m
}

var handlerTests = []handlerTest{
	{
		setup: func(fi *test.FakeIndex) Index { return fi },
		query: "describe?blobref=eabc-555",
		want:  map[string]interface{}{},
	},

	{
		setup: func(fi *test.FakeIndex) Index {
			fi.AddMeta(blobref.MustParse("abc-555"), "image/jpeg", 999)
			return fi
		},
		query: "describe?blobref=abc-555",
		want: map[string]interface{}{
			"abc-555": map[string]interface{}{
				"blobRef":  "abc-555",
				"mimeType": "image/jpeg",
				"size":     999,
			},
		},
	},

	{
		setup: func(fi *test.FakeIndex) Index {
			pn := blobref.MustParse("perma-123")
			fi.AddMeta(pn, "application/json; camliType=permanode", 123)
			fi.AddClaim(owner, pn, "set-attribute", "camliContent", "foo-232")
			fi.AddMeta(blobref.MustParse("foo-232"), "foo/bar", 878)

			// Test deleting all attributes
			fi.AddClaim(owner, pn, "add-attribute", "wont-be-present", "x")
			fi.AddClaim(owner, pn, "add-attribute", "wont-be-present", "y")
			fi.AddClaim(owner, pn, "del-attribute", "wont-be-present", "")

			// Test deleting a specific attribute.
			fi.AddClaim(owner, pn, "add-attribute", "only-delete-b", "a")
			fi.AddClaim(owner, pn, "add-attribute", "only-delete-b", "b")
			fi.AddClaim(owner, pn, "add-attribute", "only-delete-b", "c")
			fi.AddClaim(owner, pn, "del-attribute", "only-delete-b", "b")
			return fi
		},
		query: "describe?blobref=perma-123",
		want: map[string]interface{}{
			"foo-232": map[string]interface{}{
				"blobRef":  "foo-232",
				"mimeType": "foo/bar",
				"size":     878,
			},
			"perma-123": map[string]interface{}{
				"blobRef":   "perma-123",
				"mimeType":  "application/json; camliType=permanode",
				"camliType": "permanode",
				"size":      123,
				"permanode": map[string]interface{}{
					"attr": map[string]interface{}{
						"camliContent":  []string{"foo-232"},
						"only-delete-b": []string{"a", "c"},
					},
				},
			},
		},
	},

	// Test recent permanodes
	{
		setup: func(*test.FakeIndex) Index {
			// Ignore the fakeindex and use the real (but in-memory) implementation,
			// using IndexDeps to populate it.
			idx := index.NewMemoryIndex()
			id := indextest.NewIndexDeps(idx)

			pn := id.NewPlannedPermanode("pn1")
			id.SetAttribute(pn, "title", "Some title")
			return indexAndOwner{idx, id.SignerBlobRef}
		},
		query: "recent",
		want: parseJSON(`{
                "recent": [
                    {"blobref": "sha1-7ca7743e38854598680d94ef85348f2c48a44513",
                     "modtime": "2011-11-28T01:32:37Z",
                     "owner": "sha1-ad87ca5c78bd0ce1195c46f7c98e6025abbaf007"}
                ],
                "sha1-7ca7743e38854598680d94ef85348f2c48a44513": {
		 "blobRef": "sha1-7ca7743e38854598680d94ef85348f2c48a44513",
		 "camliType": "permanode",
                 "mimeType": "application/json; camliType=permanode",
                 "permanode": {
                   "attr": { "title": [ "Some title" ] }
                 },
                 "size": 534
                }
               }`),
	},

	// Test recent permanodes with thumbnails
	{
		setup: func(*test.FakeIndex) Index {
			// Ignore the fakeindex and use the real (but in-memory) implementation,
			// using IndexDeps to populate it.
			idx := index.NewMemoryIndex()
			id := indextest.NewIndexDeps(idx)

			pn := id.NewPlannedPermanode("pn1")
			id.SetAttribute(pn, "title", "Some title")
			return indexAndOwner{idx, id.SignerBlobRef}
		},
		query: "recent?thumbnails=100",
		want: parseJSON(`{
                "recent": [
                    {"blobref": "sha1-7ca7743e38854598680d94ef85348f2c48a44513",
                     "modtime": "2011-11-28T01:32:37Z",
                     "owner": "sha1-ad87ca5c78bd0ce1195c46f7c98e6025abbaf007"}
                ],
                "sha1-7ca7743e38854598680d94ef85348f2c48a44513": {
		 "blobRef": "sha1-7ca7743e38854598680d94ef85348f2c48a44513",
		 "camliType": "permanode",
                 "mimeType": "application/json; camliType=permanode",
                 "permanode": {
                   "attr": { "title": [ "Some title" ] }
                 },
                 "size": 534,
                 "thumbnailHeight": 100,
                 "thumbnailSrc": "node.png",
                 "thumbnailWidth": 100
                }
               }`),
	},

	// edgeto handler: put a permanode (member) in two parent
	// permanodes, then delete the second and verify that edges
	// back from member only reveal the first parent.
	{
		setup: func(*test.FakeIndex) Index {
			// Ignore the fakeindex and use the real (but in-memory) implementation,
			// using IndexDeps to populate it.
			idx := index.NewMemoryIndex()
			id := indextest.NewIndexDeps(idx)

			parent1 := id.NewPlannedPermanode("pn1") // sha1-7ca7743e38854598680d94ef85348f2c48a44513
			parent2 := id.NewPlannedPermanode("pn2")
			member := id.NewPlannedPermanode("member") // always sha1-9ca84f904a9bc59e6599a53f0a3927636a6dbcae
			id.AddAttribute(parent1, "camliMember", member.String())
			id.AddAttribute(parent2, "camliMember", member.String())
			id.DelAttribute(parent2, "camliMember")
			return indexAndOwner{idx, id.SignerBlobRef}
		},
		query: "edgesto?blobref=sha1-9ca84f904a9bc59e6599a53f0a3927636a6dbcae",
		want: parseJSON(`{"sha1-9ca84f904a9bc59e6599a53f0a3927636a6dbcae": {
                   "edgesTo": [
                      {"from": "sha1-7ca7743e38854598680d94ef85348f2c48a44513",
                       "fromType": "permanode"}
                   ]
                }}`),
	},
}

func TestHandler(t *testing.T) {
	for testn, tt := range handlerTests {
		fakeIndex := test.NewFakeIndex()
		idx := tt.setup(fakeIndex)

		indexOwner := owner
		if io, ok := idx.(indexOwnerer); ok {
			indexOwner = io.IndexOwner()
		}
		h := NewHandler(idx, indexOwner)

		req, err := http.NewRequest("GET", "/camli/search/"+tt.query, nil)
		if err != nil {
			t.Fatalf("%d: bad query: %v", testn, err)
		}
		req.Header.Set("X-PrefixHandler-PathSuffix", req.URL.Path[1:])

		rr := httptest.NewRecorder()
		rr.Body = new(bytes.Buffer)

		h.ServeHTTP(rr, req)

		got := rr.Body.Bytes()
		want, _ := json.MarshalIndent(tt.want, "", "  ")
		want = append(want, '\n')
		if !bytes.Equal(got, want) {
			t.Errorf("test %d:\nwant: %s\n got: %s", testn, want, got)
		}
	}
}
