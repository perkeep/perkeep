/*
Copyright 2011 The Camlistore Authors

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
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/httputil"
	"camlistore.org/pkg/index"
	"camlistore.org/pkg/index/indextest"
	"camlistore.org/pkg/osutil"
	. "camlistore.org/pkg/search"
	"camlistore.org/pkg/test"
)

// An indexOwnerer is something that knows who owns the index.
// It is implemented by indexAndOwner for use by TestHandler.
type indexOwnerer interface {
	IndexOwner() blob.Ref
}

type indexAndOwner struct {
	index.Interface
	owner blob.Ref
}

func (io indexAndOwner) IndexOwner() blob.Ref {
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
	setup func(fi *test.FakeIndex) index.Interface

	name     string // test name
	query    string // the HTTP path + optional query suffix after "camli/search/"
	postBody string // if non-nil, a POST request

	want map[string]interface{}
	// wantDescribed is a list of blobref strings that should've been
	// described in meta. If want is nil and this is non-zero length,
	// want is ignored.
	wantDescribed []string
}

var owner = blob.MustParse("abcown-123")

func parseJSON(s string) map[string]interface{} {
	m := make(map[string]interface{})
	err := json.Unmarshal([]byte(s), &m)
	if err != nil {
		panic(err)
	}
	return m
}

// addToClockOrigin returns the given Duration added
// to test.ClockOrigin, in UTC, and RFC3339Nano formatted.
func addToClockOrigin(d time.Duration) string {
	return test.ClockOrigin.Add(d).UTC().Format(time.RFC3339Nano)
}

func handlerDescribeTestSetup(fi *test.FakeIndex) index.Interface {
	pn := blob.MustParse("perma-123")
	fi.AddMeta(pn, "permanode", 123)
	fi.AddClaim(owner, pn, "set-attribute", "camliContent", "fakeref-232")
	fi.AddMeta(blob.MustParse("fakeref-232"), "", 878)

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
}

// extends handlerDescribeTestSetup but adds a camliContentImage to pn.
func handlerDescribeTestSetupWithImage(fi *test.FakeIndex) index.Interface {
	handlerDescribeTestSetup(fi)
	pn := blob.MustParse("perma-123")
	imageRef := blob.MustParse("fakeref-789")
	fi.AddMeta(imageRef, "", 789)
	fi.AddClaim(owner, pn, "set-attribute", "camliContentImage", imageRef.String())
	return fi
}

// extends handlerDescribeTestSetup but adds various embedded references to other nodes.
func handlerDescribeTestSetupWithEmbeddedRefs(fi *test.FakeIndex) index.Interface {
	handlerDescribeTestSetup(fi)
	pn := blob.MustParse("perma-123")
	c1 := blob.MustParse("fakeref-01")
	c2 := blob.MustParse("fakeref-02")
	c3 := blob.MustParse("fakeref-03")
	c4 := blob.MustParse("fakeref-04")
	c5 := blob.MustParse("fakeref-05")
	c6 := blob.MustParse("fakeref-06")
	fi.AddMeta(c1, "", 1)
	fi.AddMeta(c2, "", 2)
	fi.AddMeta(c3, "", 3)
	fi.AddMeta(c4, "", 4)
	fi.AddMeta(c5, "", 5)
	fi.AddMeta(c6, "", 6)
	fi.AddClaim(owner, pn, "set-attribute", c1.String(), "foo")
	fi.AddClaim(owner, pn, "set-attribute", "foo,"+c2.String()+"=bar", "foo")
	fi.AddClaim(owner, pn, "set-attribute", "foo:"+c3.String()+"?bar,"+c4.String(), "foo")
	fi.AddClaim(owner, pn, "set-attribute", "foo", c5.String())
	fi.AddClaim(owner, pn, "add-attribute", "bar", "baz")
	fi.AddClaim(owner, pn, "add-attribute", "bar", "monkey\n"+c6.String())
	return fi
}

var handlerTests = []handlerTest{
	{
		name:  "describe-missing",
		setup: func(fi *test.FakeIndex) index.Interface { return fi },
		query: "describe?blobref=eabfakeref-0555",
		want: parseJSON(`{
			"meta": {
			}
		}`),
	},

	{
		name: "describe-jpeg-blob",
		setup: func(fi *test.FakeIndex) index.Interface {
			fi.AddMeta(blob.MustParse("abfakeref-0555"), "", 999)
			return fi
		},
		query: "describe?blobref=abfakeref-0555",
		want: parseJSON(`{
			"meta": {
				"abfakeref-0555": {
					"blobRef":  "abfakeref-0555",
					"size":     999
				}
			}
		}`),
	},

	{
		name:  "describe-permanode",
		setup: handlerDescribeTestSetup,
		query: "describe",
		postBody: `{
 "blobref": "perma-123",
 "rules": [
    {"attrs": ["camliContent"]}
 ]
}`,
		want: parseJSON(`{
			"meta": {
				"fakeref-232": {
					"blobRef":  "fakeref-232",
					"size":     878
				},
				"perma-123": {
					"blobRef":   "perma-123",
					"camliType": "permanode",
					"size":      123,
					"permanode": {
						"attr": {
							"camliContent": [ "fakeref-232" ],
							"only-delete-b": [ "a", "c" ]
						},
						"modtime": "` + addToClockOrigin(8*time.Second) + `"
					}
				}
			}
		}`),
	},

	{
		name:  "describe-permanode-image",
		setup: handlerDescribeTestSetupWithImage,
		query: "describe",
		postBody: `{
 "blobref": "perma-123",
 "rules": [
    {"attrs": ["camliContent", "camliContentImage"]}
 ]
}`,
		want: parseJSON(`{
			"meta": {
				"fakeref-232": {
					"blobRef":  "fakeref-232",
					"size":     878
				},
				"fakeref-789": {
					"blobRef":  "fakeref-789",
					"size":     789
				},
				"perma-123": {
					"blobRef":   "perma-123",
					"camliType": "permanode",
					"size":      123,
					"permanode": {
						"attr": {
							"camliContent": [ "fakeref-232" ],
							"camliContentImage": [ "fakeref-789" ],
							"only-delete-b": [ "a", "c" ]
						},
						"modtime": "` + addToClockOrigin(9*time.Second) + `"
					}
				}
			}
		}`),
	},

	// TODO(bradfitz): we'll probably will want to delete or redo this
	// test when we remove depth=N support from describe.
	{
		name:  "describe-permanode-embedded-references",
		setup: handlerDescribeTestSetupWithEmbeddedRefs,
		query: "describe?blobref=perma-123&depth=2",
		want: parseJSON(`{
			"meta": {
				"fakeref-01": {
				  "blobRef": "fakeref-01",
				  "size": 1
				},
				"fakeref-02": {
				  "blobRef": "fakeref-02",
				  "size": 2
				},
				"fakeref-03": {
				  "blobRef": "fakeref-03",
				  "size": 3
				},
				"fakeref-04": {
				  "blobRef": "fakeref-04",
				  "size": 4
				},
				"fakeref-05": {
				  "blobRef": "fakeref-05",
				  "size": 5
				},
				"fakeref-06": {
				  "blobRef": "fakeref-06",
				  "size": 6
				},
				"fakeref-232": {
					"blobRef":  "fakeref-232",
					"size":     878
				},
				"perma-123": {
					"blobRef":   "perma-123",
					"camliType": "permanode",
					"size":      123,
					"permanode": {
						"attr": {
							"bar": [
								"baz",
								"monkey\nfakeref-06"
							],
							"fakeref-01": [
								"foo"
							],
							"camliContent": [
								"fakeref-232"
							],
							"foo": [
								"fakeref-05"
							],
							"foo,fakeref-02=bar": [
								"foo"
							],
							"foo:fakeref-03?bar,fakeref-04": [
								"foo"
							],
							"camliContent": [ "fakeref-232" ],
							"only-delete-b": [ "a", "c" ]
						},
						"modtime": "` + addToClockOrigin(14*time.Second) + `"
					}
				}
			}
		}`),
	},

	{
		name:  "describe-permanode-timetravel",
		setup: handlerDescribeTestSetup,
		query: "describe",
		postBody: `{
 "blobref": "perma-123",
 "at": "` + addToClockOrigin(3*time.Second) + `",
 "rules": [
    {"attrs": ["camliContent"]}
 ]
}`,
		want: parseJSON(`{
			"meta": {
				"fakeref-232": {
					"blobRef":  "fakeref-232",
					"size":     878
				},
				"perma-123": {
					"blobRef":   "perma-123",
					"camliType": "permanode",
					"size":      123,
					"permanode": {
						"attr": {
							"camliContent": [ "fakeref-232" ],
							"wont-be-present": [ "x", "y" ]
						},
						"modtime": "` + addToClockOrigin(3*time.Second) + `"
					}
				}
			}
		}`),
	},

	// test that describe follows camliPath:foo attributes
	{
		name: "describe-permanode-follows-camliPath",
		setup: func(fi *test.FakeIndex) index.Interface {
			pn := blob.MustParse("perma-123")
			fi.AddMeta(pn, "permanode", 123)
			fi.AddClaim(owner, pn, "set-attribute", "camliPath:foo", "fakeref-123")

			fi.AddMeta(blob.MustParse("fakeref-123"), "", 123)
			return fi
		},
		query: "describe",
		postBody: `{
 "blobref": "perma-123",
 "rules": [
    {"attrs": ["camliPath:*"]}
 ]
}`,
		want: parseJSON(`{
  "meta": {
	"fakeref-123": {
	  "blobRef": "fakeref-123",
	  "size": 123
	},
	"perma-123": {
	  "blobRef": "perma-123",
	  "camliType": "permanode",
	  "size": 123,
	  "permanode": {
		"attr": {
		  "camliPath:foo": [
			"fakeref-123"
		  ]
		},
		"modtime": "` + addToClockOrigin(1*time.Second) + `"
	  }
	}
  }
}`),
	},

	// Test recent permanodes
	{
		name: "recent-1",
		setup: func(*test.FakeIndex) index.Interface {
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
					 "modtime": "2011-11-28T01:32:37.000123456Z",
					 "owner": "sha1-ad87ca5c78bd0ce1195c46f7c98e6025abbaf007"}
				],
				"meta": {
					  "sha1-7ca7743e38854598680d94ef85348f2c48a44513": {
		 "blobRef": "sha1-7ca7743e38854598680d94ef85348f2c48a44513",
		 "camliType": "permanode",
				 "permanode": {
				   "attr": { "title": [ "Some title" ] },
					"modtime": "` + addToClockOrigin(1*time.Second) + `"
				 },
				 "size": 534
					 }
				 }
			   }`),
	},

	// Test recent permanode of a file
	{
		name: "recent-file",
		setup: func(*test.FakeIndex) index.Interface {
			// Ignore the fakeindex and use the real (but in-memory) implementation,
			// using IndexDeps to populate it.
			idx := index.NewMemoryIndex()
			id := indextest.NewIndexDeps(idx)

			// Upload a basic image
			camliRootPath, err := osutil.GoPackagePath("camlistore.org")
			if err != nil {
				panic("Package camlistore.org no found in $GOPATH or $GOPATH not defined")
			}
			uploadFile := func(file string, modTime time.Time) blob.Ref {
				fileName := filepath.Join(camliRootPath, "pkg", "index", "indextest", "testdata", file)
				contents, err := ioutil.ReadFile(fileName)
				if err != nil {
					panic(err)
				}
				br, _ := id.UploadFile(file, string(contents), modTime)
				return br
			}
			dudeFileRef := uploadFile("dude.jpg", time.Time{})

			pn := id.NewPlannedPermanode("pn1")
			id.SetAttribute(pn, "camliContent", dudeFileRef.String())
			return indexAndOwner{idx, id.SignerBlobRef}
		},
		query: "recent",
		want: parseJSON(`{
				"recent": [
					{"blobref": "sha1-7ca7743e38854598680d94ef85348f2c48a44513",
					 "modtime": "2011-11-28T01:32:37.000123456Z",
					 "owner": "sha1-ad87ca5c78bd0ce1195c46f7c98e6025abbaf007"}
				],
				"meta": {
					  "sha1-7ca7743e38854598680d94ef85348f2c48a44513": {
		 "blobRef": "sha1-7ca7743e38854598680d94ef85348f2c48a44513",
		 "camliType": "permanode",
				 "permanode": {
				"attr": {
				  "camliContent": [
					"sha1-e3f0ee86622dda4d7e8a4a4af51117fb79dbdbbb"
				  ]
				},
				"modtime": "` + addToClockOrigin(1*time.Second) + `"
			  },
				 "size": 534
					 },
			"sha1-e3f0ee86622dda4d7e8a4a4af51117fb79dbdbbb": {
			  "blobRef": "sha1-e3f0ee86622dda4d7e8a4a4af51117fb79dbdbbb",
			  "camliType": "file",
			  "size": 184,
			  "file": {
				"fileName": "dude.jpg",
				"size": 1932,
				"mimeType": "image/jpeg",
				"wholeRef": "sha1-142b504945338158e0149d4ed25a41a522a28e88"
			  },
			  "image": {
				"width": 50,
				"height": 100
			  }
			}
				 }
			   }`),
	},

	// Test recent permanode of a file, in a collection
	{
		name: "recent-file-collec",
		setup: func(*test.FakeIndex) index.Interface {
			SetTestHookBug121(func() {
				time.Sleep(2 * time.Second)
			})
			// Ignore the fakeindex and use the real (but in-memory) implementation,
			// using IndexDeps to populate it.
			idx := index.NewMemoryIndex()
			id := indextest.NewIndexDeps(idx)

			// Upload a basic image
			camliRootPath, err := osutil.GoPackagePath("camlistore.org")
			if err != nil {
				panic("Package camlistore.org no found in $GOPATH or $GOPATH not defined")
			}
			uploadFile := func(file string, modTime time.Time) blob.Ref {
				fileName := filepath.Join(camliRootPath, "pkg", "index", "indextest", "testdata", file)
				contents, err := ioutil.ReadFile(fileName)
				if err != nil {
					panic(err)
				}
				br, _ := id.UploadFile(file, string(contents), modTime)
				return br
			}
			dudeFileRef := uploadFile("dude.jpg", time.Time{})
			pn := id.NewPlannedPermanode("pn1")
			id.SetAttribute(pn, "camliContent", dudeFileRef.String())
			collec := id.NewPlannedPermanode("pn2")
			id.SetAttribute(collec, "camliMember", pn.String())
			return indexAndOwner{idx, id.SignerBlobRef}
		},
		query: "recent",
		want: parseJSON(`{
		  "recent": [
			{
			  "blobref": "sha1-3c8b5d36bd4182c6fe802984832f197786662ccf",
			  "modtime": "2011-11-28T01:32:38.000123456Z",
			  "owner": "sha1-ad87ca5c78bd0ce1195c46f7c98e6025abbaf007"
			},
			{
			  "blobref": "sha1-7ca7743e38854598680d94ef85348f2c48a44513",
			  "modtime": "2011-11-28T01:32:37.000123456Z",
			  "owner": "sha1-ad87ca5c78bd0ce1195c46f7c98e6025abbaf007"
			}
		  ],
		  "meta": {
			"sha1-3c8b5d36bd4182c6fe802984832f197786662ccf": {
			  "blobRef": "sha1-3c8b5d36bd4182c6fe802984832f197786662ccf",
			  "camliType": "permanode",
			  "size": 534,
			  "permanode": {
				"attr": {
				  "camliMember": [
					"sha1-7ca7743e38854598680d94ef85348f2c48a44513"
				  ]
				},
				"modtime": "` + addToClockOrigin(2*time.Second) + `"
			  }
			},
			"sha1-7ca7743e38854598680d94ef85348f2c48a44513": {
			  "blobRef": "sha1-7ca7743e38854598680d94ef85348f2c48a44513",
			  "camliType": "permanode",
			  "size": 534,
			  "permanode": {
				"attr": {
				  "camliContent": [
					"sha1-e3f0ee86622dda4d7e8a4a4af51117fb79dbdbbb"
				  ]
				},
				"modtime": "` + addToClockOrigin(1*time.Second) + `"
			  }
			},
			"sha1-e3f0ee86622dda4d7e8a4a4af51117fb79dbdbbb": {
			  "blobRef": "sha1-e3f0ee86622dda4d7e8a4a4af51117fb79dbdbbb",
			  "camliType": "file",
			  "size": 184,
			  "file": {
				"fileName": "dude.jpg",
				"size": 1932,
				"mimeType": "image/jpeg",
				"wholeRef": "sha1-142b504945338158e0149d4ed25a41a522a28e88"
			  },
			  "image": {
				"width": 50,
				"height": 100
			  }
			}
		  }
		}`),
	},

	// Test recent permanodes with thumbnails
	{
		name: "recent-thumbs",
		setup: func(*test.FakeIndex) index.Interface {
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
					 "modtime": "2011-11-28T01:32:37.000123456Z",
					 "owner": "sha1-ad87ca5c78bd0ce1195c46f7c98e6025abbaf007"}
				],
				"meta": {
				   "sha1-7ca7743e38854598680d94ef85348f2c48a44513": {
		 "blobRef": "sha1-7ca7743e38854598680d94ef85348f2c48a44513",
		 "camliType": "permanode",
				 "permanode": {
				   "attr": { "title": [ "Some title" ] },
					"modtime": "` + addToClockOrigin(1*time.Second) + `"
				 },
				 "size": 534
					}
				}
			   }`),
	},

	// edgeto handler: put a permanode (member) in two parent
	// permanodes, then delete the second and verify that edges
	// back from member only reveal the first parent.
	{
		name: "edge-to",
		setup: func(*test.FakeIndex) index.Interface {
			// Ignore the fakeindex and use the real (but in-memory) implementation,
			// using IndexDeps to populate it.
			idx := index.NewMemoryIndex()
			id := indextest.NewIndexDeps(idx)

			parent1 := id.NewPlannedPermanode("pn1") // sha1-7ca7743e38854598680d94ef85348f2c48a44513
			parent2 := id.NewPlannedPermanode("pn2")
			member := id.NewPlannedPermanode("member") // always sha1-9ca84f904a9bc59e6599a53f0a3927636a6dbcae
			id.AddAttribute(parent1, "camliMember", member.String())
			id.AddAttribute(parent2, "camliMember", member.String())
			id.DelAttribute(parent2, "camliMember", "")
			return indexAndOwner{idx, id.SignerBlobRef}
		},
		query: "edgesto?blobref=sha1-9ca84f904a9bc59e6599a53f0a3927636a6dbcae",
		want: parseJSON(`{
			"toRef": "sha1-9ca84f904a9bc59e6599a53f0a3927636a6dbcae",
			"edgesTo": [
				{"from": "sha1-7ca7743e38854598680d94ef85348f2c48a44513",
				"fromType": "permanode"}
				]
			}`),
	},
}

func marshalJSON(v interface{}) string {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		panic(err)
	}
	return string(b)
}

func jmap(v interface{}) map[string]interface{} {
	m := make(map[string]interface{})
	if err := json.NewDecoder(strings.NewReader(marshalJSON(v))).Decode(&m); err != nil {
		panic(err)
	}
	return m
}

func checkNoDups(sliceName string, tests []handlerTest) {
	seen := map[string]bool{}
	for _, tt := range tests {
		if seen[tt.name] {
			panic(fmt.Sprintf("duplicate handlerTest named %q in var %s", tt.name, sliceName))
		}
		seen[tt.name] = true
	}
}

func init() {
	checkNoDups("handlerTests", handlerTests)
}

func (ht handlerTest) test(t *testing.T) {
	SetTestHookBug121(func() {})

	fakeIndex := test.NewFakeIndex()
	idx := ht.setup(fakeIndex)

	indexOwner := owner
	if io, ok := idx.(indexOwnerer); ok {
		indexOwner = io.IndexOwner()
	}
	h := NewHandler(idx, indexOwner)

	var body io.Reader
	var method = "GET"
	if ht.postBody != "" {
		method = "POST"
		body = strings.NewReader(ht.postBody)
	}
	req, err := http.NewRequest(method, "/camli/search/"+ht.query, body)
	if err != nil {
		t.Fatalf("%s: bad query: %v", ht.name, err)
	}
	req.Header.Set(httputil.PathSuffixHeader, req.URL.Path[1:])

	rr := httptest.NewRecorder()
	rr.Body = new(bytes.Buffer)

	h.ServeHTTP(rr, req)
	got := rr.Body.Bytes()

	if len(ht.wantDescribed) > 0 {
		dr := new(DescribeResponse)
		if err := json.NewDecoder(bytes.NewReader(got)).Decode(dr); err != nil {
			t.Fatalf("On test %s: Non-JSON response: %s", ht.name, got)
		}
		var gotDesc []string
		for k := range dr.Meta {
			gotDesc = append(gotDesc, k)
		}
		sort.Strings(ht.wantDescribed)
		sort.Strings(gotDesc)
		if !reflect.DeepEqual(gotDesc, ht.wantDescribed) {
			t.Errorf("On test %s: described blobs:\n%v\nwant:\n%v\n",
				ht.name, gotDesc, ht.wantDescribed)
		}
		if ht.want == nil {
			return
		}
	}

	want, _ := json.MarshalIndent(ht.want, "", "  ")
	trim := bytes.TrimSpace

	if bytes.Equal(trim(got), trim(want)) {
		return
	}

	// Try with re-encoded got, since the JSON ordering doesn't matter
	// to the test,
	gotj := parseJSON(string(got))
	got2, _ := json.MarshalIndent(gotj, "", "  ")
	if bytes.Equal(got2, want) {
		return
	}
	diff := test.Diff(want, got2)

	t.Errorf("test %s:\nwant: %s\n got: %s\ndiff:\n%s", ht.name, want, got, diff)
}

func TestHandler(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in short mode")
		return
	}
	defer SetTestHookBug121(func() {})
	for _, ht := range handlerTests {
		ht.test(t)
	}
}
