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

package search_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"perkeep.org/internal/httputil"
	"perkeep.org/internal/osutil"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/index"
	"perkeep.org/pkg/index/indextest"
	"perkeep.org/pkg/jsonsign"
	"perkeep.org/pkg/schema"
	. "perkeep.org/pkg/search"
	"perkeep.org/pkg/test"
)

type indexAndOwner struct {
	index *index.Index
	owner blob.Ref
}

func (io indexAndOwner) IndexOwner() blob.Ref {
	return io.owner
}

type handlerTest struct {
	// setup is responsible for populating the index before the
	// handler is invoked.
	setup func(t *testing.T) indexAndOwner

	name     string // test name
	query    string // the HTTP path + optional query suffix after "camli/search/"
	postBody string // if non-nil, a POST request

	want map[string]any
	// wantDescribed is a list of blobref strings that should've been
	// described in meta. If want is nil and this is non-zero length,
	// want is ignored.
	wantDescribed []string
}

var (
	owner    *index.Owner
	ownerRef *test.Blob
	signer   *schema.Signer
	// TODO(mpl): can lastModtime being a global ever become a race problem if tests are concurrent?
	lastModtime time.Time
)

func parseJSON(s string) map[string]any {
	m := make(map[string]any)
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

func init() {
	ownerRef = indextest.PubKey
	owner = index.NewOwner(indextest.KeyID, ownerRef.BlobRef())
	signer = testSigner()
	for _, v := range testBlobsContents {
		testBlobs[v] = &test.Blob{Contents: v}
	}
	perma123 := schema.NewPlannedPermanode("perma-123")
	perma123signed, err := perma123.SignAt(ctxbg, signer, test.ClockOrigin)
	if err != nil {
		panic(err)
	}
	testBlobs["perma-123"] = &test.Blob{Contents: perma123signed}
	handlerTests = initTests()
}

var (
	testBlobsContents = []string{
		"blobcontents1",
		"fakeref-123",
		"fakeref-232",
		"fakeref-789",
		"fakeref-01",
		"fakeref-02",
		"fakeref-03",
		"fakeref-04",
		"fakeref-05",
		"fakeref-06",
	}
	testBlobs = make(map[string]*test.Blob)
)

// testSigner returns the signer, as well as its armored public key, from
// pkg/jsonsign/testdata/test-secring.gpg
func testSigner() *schema.Signer {
	srcRoot, err := osutil.PkSourceRoot()
	if err != nil {
		panic(fmt.Sprintf("source root folder not found: %v", err))
	}
	ent, err := jsonsign.EntityFromSecring(indextest.KeyID, filepath.Join(srcRoot, "pkg", "jsonsign", "testdata", "test-secring.gpg"))
	if err != nil {
		panic(err)
	}
	sig, err := schema.NewSigner(owner.BlobRef(), strings.NewReader(ownerRef.Contents), ent)
	if err != nil {
		panic(err)
	}
	return sig
}

// fetcherIndex groups addBlob, addClaim, and addPermanode, that are all methods
// to write both to the Fetcher and the Index.
type fetcherIndex struct {
	tf  *test.Fetcher
	idx *index.Index
}

func (fi *fetcherIndex) addBlob(b *test.Blob) error {
	fi.tf.AddBlob(b)
	if _, err := fi.idx.ReceiveBlob(ctxbg, b.BlobRef(), b.Reader()); err != nil {
		return fmt.Errorf("ReceiveBlob(%v): %v", b.BlobRef(), err)
	}
	return nil
}

func (fi *fetcherIndex) addClaim(cl *schema.Builder) error {
	lastModtime = lastModtime.Add(time.Second).UTC()
	signedcl, err := cl.SignAt(ctxbg, signer, lastModtime)
	if err != nil {
		return err
	}
	return fi.addBlob(&test.Blob{Contents: signedcl})
}

func (fi *fetcherIndex) addPermanode(pnStr string, attrs ...string) error {
	lastModtime = lastModtime.Add(time.Second).UTC()
	pn := schema.NewPlannedPermanode(pnStr)
	pns, err := pn.SignAt(ctxbg, signer, lastModtime)
	if err != nil {
		return err
	}
	tpn := &test.Blob{Contents: pns}
	if err := fi.addBlob(tpn); err != nil {
		return err
	}
	for len(attrs) > 0 {
		k, v := attrs[0], attrs[1]
		attrs = attrs[2:]
		if err := fi.addClaim(schema.NewAddAttributeClaim(tpn.BlobRef(), k, v)); err != nil {
			return err
		}
	}
	return nil
}

func checkErr(t *testing.T, err error) {
	if err != nil {
		t.Fatal(err)
	}
}

// initial setup of perma123
// lastModtime is at test.ClockOrigin + 8s (last claim on perma123) on return.
func handlerDescribeTestSetup(t *testing.T) indexAndOwner {
	idx := index.NewMemoryIndex()
	tf := new(test.Fetcher)
	idx.InitBlobSource(tf)
	idx.KeyFetcher = tf
	fi := &fetcherIndex{
		tf:  tf,
		idx: idx,
	}

	checkErr(t, fi.addBlob(ownerRef))
	perma123 := testBlobs["perma-123"]
	fi.addBlob(perma123)
	fakeref232 := testBlobs["fakeref-232"]
	checkErr(t, fi.addBlob(fakeref232))

	lastModtime = test.ClockOrigin
	checkErr(t, fi.addClaim(schema.NewSetAttributeClaim(perma123.BlobRef(), "camliContent", fakeref232.BlobRef().String())))

	// Test deleting all attributes
	checkErr(t, fi.addClaim(schema.NewAddAttributeClaim(perma123.BlobRef(), "wont-be-present", "x")))
	checkErr(t, fi.addClaim(schema.NewAddAttributeClaim(perma123.BlobRef(), "wont-be-present", "y")))
	checkErr(t, fi.addClaim(schema.NewDelAttributeClaim(perma123.BlobRef(), "wont-be-present", "")))

	// Test deleting a specific attribute.
	checkErr(t, fi.addClaim(schema.NewAddAttributeClaim(perma123.BlobRef(), "only-delete-b", "a")))
	checkErr(t, fi.addClaim(schema.NewAddAttributeClaim(perma123.BlobRef(), "only-delete-b", "b")))
	checkErr(t, fi.addClaim(schema.NewAddAttributeClaim(perma123.BlobRef(), "only-delete-b", "c")))
	checkErr(t, fi.addClaim(schema.NewDelAttributeClaim(perma123.BlobRef(), "only-delete-b", "b")))

	return indexAndOwner{
		index: idx,
		owner: owner.BlobRef(),
	}
}

// extends handlerDescribeTestSetup but adds a camliContentImage to pn.
// lastModtime is at test.ClockOrigin + 9s on return.
func handlerDescribeTestSetupWithImage(t *testing.T) indexAndOwner {
	ixo := handlerDescribeTestSetup(t)
	idx := ixo.index
	tf := idx.KeyFetcher.(*test.Fetcher)
	fi := &fetcherIndex{
		tf:  tf,
		idx: idx,
	}
	perma123 := testBlobs["perma-123"]
	imageBlob := testBlobs["fakeref-789"]
	checkErr(t, fi.addBlob(imageBlob))
	lastModtime = test.ClockOrigin.Add(8 * time.Second).UTC()
	checkErr(t, fi.addClaim(schema.NewSetAttributeClaim(perma123.BlobRef(), "camliContentImage", imageBlob.BlobRef().String())))
	return indexAndOwner{
		index: idx,
		owner: owner.BlobRef(),
	}
}

// extends handlerDescribeTestSetup but adds various embedded references to other nodes.
// lastModtime is at test.ClockOrigin + 14s on return.
func handlerDescribeTestSetupWithEmbeddedRefs(t *testing.T) indexAndOwner {
	ixo := handlerDescribeTestSetup(t)
	idx := ixo.index
	tf := idx.KeyFetcher.(*test.Fetcher)
	fi := &fetcherIndex{
		tf:  tf,
		idx: idx,
	}

	perma123 := testBlobs["perma-123"]
	c1 := testBlobs["fakeref-01"]
	checkErr(t, fi.addBlob(c1))
	c2 := testBlobs["fakeref-02"]
	checkErr(t, fi.addBlob(c2))
	c3 := testBlobs["fakeref-03"]
	checkErr(t, fi.addBlob(c3))
	c4 := testBlobs["fakeref-04"]
	checkErr(t, fi.addBlob(c4))
	c5 := testBlobs["fakeref-05"]
	checkErr(t, fi.addBlob(c5))
	c6 := testBlobs["fakeref-06"]
	checkErr(t, fi.addBlob(c6))

	lastModtime = test.ClockOrigin.Add(8 * time.Second).UTC()
	checkErr(t, fi.addClaim(schema.NewSetAttributeClaim(perma123.BlobRef(), c1.BlobRef().String(), "foo")))
	checkErr(t, fi.addClaim(schema.NewSetAttributeClaim(perma123.BlobRef(), "foo,"+c2.BlobRef().String()+"=bar", "foo")))
	checkErr(t, fi.addClaim(schema.NewSetAttributeClaim(perma123.BlobRef(), "foo:"+c3.BlobRef().String()+"?bar,"+c4.BlobRef().String(), "foo")))
	checkErr(t, fi.addClaim(schema.NewSetAttributeClaim(perma123.BlobRef(), "foo", c5.BlobRef().String())))
	checkErr(t, fi.addClaim(schema.NewAddAttributeClaim(perma123.BlobRef(), "bar", "baz")))
	checkErr(t, fi.addClaim(schema.NewAddAttributeClaim(perma123.BlobRef(), "bar", "monkey\n"+c6.BlobRef().String())))

	return indexAndOwner{
		index: idx,
		owner: owner.BlobRef(),
	}
}

func tbRefStr(name string) string {
	tb, ok := testBlobs[name]
	if !ok {
		panic(name + " not found")
	}
	return tb.BlobRef().String()
}

func tbSize(name string) string {
	tb, ok := testBlobs[name]
	if !ok {
		panic(name + " not found")
	}
	return fmt.Sprintf("%d", tb.Size())
}

var handlerTests []handlerTest

func initTests() []handlerTest {
	return []handlerTest{
		{
			name: "describe-missing",
			setup: func(t *testing.T) indexAndOwner {
				return indexAndOwner{
					index: index.NewMemoryIndex(),
					owner: owner.BlobRef(),
				}
			},
			query: "describe?blobref=eabfakeref-0555",
			want: parseJSON(`{
			"meta": {
			}
		}`),
		},

		{
			name: "describe-jpeg-blob",
			setup: func(t *testing.T) indexAndOwner {
				idx := index.NewMemoryIndex()
				tb, ok := testBlobs["blobcontents1"]
				if !ok {
					panic("blobcontents1 not found")
				}
				if _, err := idx.ReceiveBlob(ctxbg, tb.BlobRef(), tb.Reader()); err != nil {
					panic(err)
				}
				return indexAndOwner{
					index: idx,
					owner: owner.BlobRef(),
				}
			},
			query: "describe?blobref=" + tbRefStr("blobcontents1"),
			want: parseJSON(`{
			"meta": {
				"` + tbRefStr("blobcontents1") + `": {
					"blobRef":  "` + tbRefStr("blobcontents1") + `",
					"size":     ` + tbSize("blobcontents1") + `
				}
			}
		}`),
		},

		{
			name:  "describe-permanode",
			setup: handlerDescribeTestSetup,
			query: "describe",
			postBody: `{
				"blobref": "` + tbRefStr("perma-123") + `",
				"rules": [
					{"attrs": ["camliContent"]}
				]
			}`,
			want: parseJSON(`{
			"meta": {
				"` + tbRefStr("fakeref-232") + `": {
					"blobRef":  "` + tbRefStr("fakeref-232") + `",
					"size":     ` + tbSize("fakeref-232") + `
				},
				"` + tbRefStr("perma-123") + `": {
					"blobRef":   "` + tbRefStr("perma-123") + `",
					"camliType": "permanode",
					"size":      ` + tbSize("perma-123") + `,
					"permanode": {
						"attr": {
							"camliContent": [ "` + tbRefStr("fakeref-232") + `" ],
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
				"blobref": "` + tbRefStr("perma-123") + `",
				"rules": [
					{"attrs": ["camliContent", "camliContentImage"]}
				]
			}`,
			want: parseJSON(`{
			"meta": {
				"` + tbRefStr("fakeref-232") + `": {
					"blobRef":  "` + tbRefStr("fakeref-232") + `",
					"size":     ` + tbSize("fakeref-232") + `
				},
				"` + tbRefStr("fakeref-789") + `": {
					"blobRef":  "` + tbRefStr("fakeref-789") + `",
					"size":     ` + tbSize("fakeref-789") + `
				},
				"` + tbRefStr("perma-123") + `": {
					"blobRef":   "` + tbRefStr("perma-123") + `",
					"camliType": "permanode",
					"size":      ` + tbSize("perma-123") + `,
					"permanode": {
						"attr": {
							"camliContent": [ "` + tbRefStr("fakeref-232") + `" ],
							"camliContentImage": [ "` + tbRefStr("fakeref-789") + `" ],
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
			query: "describe?blobref=" + tbRefStr("perma-123") + "&depth=2",
			want: parseJSON(`{
				"meta": {
					"` + tbRefStr("fakeref-01") + `": {
					  "blobRef": "` + tbRefStr("fakeref-01") + `",
					  "size": ` + tbSize("fakeref-01") + `
					},
					"` + tbRefStr("fakeref-02") + `": {
					  "blobRef": "` + tbRefStr("fakeref-02") + `",
					  "size": ` + tbSize("fakeref-02") + `
					},
					"` + tbRefStr("fakeref-03") + `": {
					  "blobRef": "` + tbRefStr("fakeref-03") + `",
					  "size": ` + tbSize("fakeref-03") + `
					},
					"` + tbRefStr("fakeref-04") + `": {
					  "blobRef": "` + tbRefStr("fakeref-04") + `",
					  "size": ` + tbSize("fakeref-04") + `
					},
					"` + tbRefStr("fakeref-05") + `": {
					  "blobRef": "` + tbRefStr("fakeref-05") + `",
					  "size": ` + tbSize("fakeref-05") + `
					},
					"` + tbRefStr("fakeref-06") + `": {
					  "blobRef": "` + tbRefStr("fakeref-06") + `",
					  "size": ` + tbSize("fakeref-06") + `
					},
					"` + tbRefStr("fakeref-232") + `": {
						"blobRef":  "` + tbRefStr("fakeref-232") + `",
						"size":     ` + tbSize("fakeref-232") + `
					},
					"` + tbRefStr("perma-123") + `": {
						"blobRef":   "` + tbRefStr("perma-123") + `",
						"camliType": "permanode",
						"size":      ` + tbSize("perma-123") + `,
						"permanode": {
							"attr": {
								"bar": [
									"baz",
									"monkey\n` + tbRefStr("fakeref-06") + `"
								],
								"` + tbRefStr("fakeref-01") + `": [
									"foo"
								],
								"camliContent": [
									"` + tbRefStr("fakeref-06") + `"
								],
								"foo": [
									"` + tbRefStr("fakeref-05") + `"
								],
								"foo,` + tbRefStr("fakeref-02") + `=bar": [
									"foo"
								],
								"foo:` + tbRefStr("fakeref-03") + `?bar,` + tbRefStr("fakeref-04") + `": [
									"foo"
								],
								"camliContent": [ "` + tbRefStr("fakeref-232") + `" ],
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
		    "blobref": "` + tbRefStr("perma-123") + `",
		    "at": "` + addToClockOrigin(3*time.Second) + `",
		    "rules": [
		       {"attrs": ["camliContent"]}
		    ]
		   }`,
			want: parseJSON(`{
				"meta": {
					"` + tbRefStr("fakeref-232") + `": {
						"blobRef":  "` + tbRefStr("fakeref-232") + `",
						"size":     ` + tbSize("fakeref-232") + `
					},
					"` + tbRefStr("perma-123") + `": {
						"blobRef":   "` + tbRefStr("perma-123") + `",
						"camliType": "permanode",
						"size":      ` + tbSize("perma-123") + `,
						"permanode": {
							"attr": {
								"camliContent": [ "` + tbRefStr("fakeref-232") + `" ],
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
			setup: func(t *testing.T) indexAndOwner {
				idx := index.NewMemoryIndex()
				tf := new(test.Fetcher)
				idx.InitBlobSource(tf)
				idx.KeyFetcher = tf
				fi := &fetcherIndex{
					tf:  tf,
					idx: idx,
				}

				checkErr(t, fi.addBlob(ownerRef))
				perma123 := testBlobs["perma-123"]
				checkErr(t, fi.addBlob(perma123))
				target := testBlobs["fakeref-123"]
				checkErr(t, fi.addBlob(target))
				lastModtime = test.ClockOrigin
				checkErr(t, fi.addClaim(schema.NewSetAttributeClaim(perma123.BlobRef(), "camliPath:foo", target.BlobRef().String())))
				return indexAndOwner{
					index: idx,
					owner: owner.BlobRef(),
				}
			},
			query: "describe",
			postBody: `{
				"blobref": "` + tbRefStr("perma-123") + `",
				"rules": [
					{"attrs": ["camliPath:*"]}
				]
		   }`,
			want: parseJSON(`{
			"meta": {
			"` + tbRefStr("fakeref-123") + `": {
			"blobRef": "` + tbRefStr("fakeref-123") + `",
			"size":  ` + tbSize("fakeref-123") + `
			},
			"` + tbRefStr("perma-123") + `": {
				"blobRef": "` + tbRefStr("perma-123") + `",
				"camliType": "permanode",
				"size": ` + tbSize("perma-123") + `,
				"permanode": {
				"attr": {
				"camliPath:foo": [
					"` + tbRefStr("fakeref-123") + `"
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
			setup: func(t *testing.T) indexAndOwner {
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
							{"blobref": "sha224-e42e49e034a0c34d959fe6d9a5b7074822bca4ce660792727c6a90a0",
							 "modtime": "2011-11-28T01:32:37.000123456Z",
							 "owner": "sha224-a794846212ff67acdd00c6b90eee492baf674d41da8a621d2e8042dd"}
						],
						"meta": {
							  "sha224-e42e49e034a0c34d959fe6d9a5b7074822bca4ce660792727c6a90a0": {
				 "blobRef": "sha224-e42e49e034a0c34d959fe6d9a5b7074822bca4ce660792727c6a90a0",
				 "camliType": "permanode",
						 "permanode": {
						   "attr": { "title": [ "Some title" ] },
							"modtime": "` + addToClockOrigin(1*time.Second) + `"
						 },
						 "size": 552
							 }
						 }
					   }`),
		},

		// Test recent permanode of a file
		{
			name: "recent-file",
			setup: func(t *testing.T) indexAndOwner {
				// Ignore the fakeindex and use the real (but in-memory) implementation,
				// using IndexDeps to populate it.
				idx := index.NewMemoryIndex()
				id := indextest.NewIndexDeps(idx)

				// Upload a basic image
				srcRoot, err := osutil.PkSourceRoot()
				if err != nil {
					panic(fmt.Sprintf("source root folder not found: %v", err))
				}
				uploadFile := func(file string, modTime time.Time) blob.Ref {
					fileName := filepath.Join(srcRoot, "pkg", "index", "indextest", "testdata", file)
					contents, err := os.ReadFile(fileName)
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
							{"blobref": "sha224-e42e49e034a0c34d959fe6d9a5b7074822bca4ce660792727c6a90a0",
							 "modtime": "2011-11-28T01:32:37.000123456Z",
							 "owner": "sha224-a794846212ff67acdd00c6b90eee492baf674d41da8a621d2e8042dd"}
						],
						"meta": {
							  "sha224-e42e49e034a0c34d959fe6d9a5b7074822bca4ce660792727c6a90a0": {
				 "blobRef": "sha224-e42e49e034a0c34d959fe6d9a5b7074822bca4ce660792727c6a90a0",
				 "camliType": "permanode",
						 "permanode": {
						"attr": {
						  "camliContent": [
							"sha224-37f88a4216d12c6907ea8422cf8389460e8c5c56b5e58ac1dbbcb430"
						  ]
						},
						"modtime": "` + addToClockOrigin(1*time.Second) + `"
					  },
						 "size": 552
							 },
					"sha224-37f88a4216d12c6907ea8422cf8389460e8c5c56b5e58ac1dbbcb430": {
					  "blobRef": "sha224-37f88a4216d12c6907ea8422cf8389460e8c5c56b5e58ac1dbbcb430",
					  "camliType": "file",
					  "size": 202,
					  "file": {
						"fileName": "dude.jpg",
						"size": 1932,
						"mimeType": "image/jpeg",
						"wholeRef": "sha224-ed1f1bfda8e23983dd49d98748a8867fd8d926adb6aa396041a860cb"
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
			setup: func(t *testing.T) indexAndOwner {
				SetTestHookBug121(func() {
					time.Sleep(2 * time.Second)
				})
				// Ignore the fakeindex and use the real (but in-memory) implementation,
				// using IndexDeps to populate it.
				idx := index.NewMemoryIndex()
				id := indextest.NewIndexDeps(idx)

				// Upload a basic image
				srcRoot, err := osutil.PkSourceRoot()
				if err != nil {
					panic(fmt.Sprintf("source root folder not found: %v", err))
				}
				uploadFile := func(file string, modTime time.Time) blob.Ref {
					fileName := filepath.Join(srcRoot, "pkg", "index", "indextest", "testdata", file)
					contents, err := os.ReadFile(fileName)
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
					  "blobref": "sha224-5a04f1cc2d464f4b7bfe206cf22808ae86d2572913fc603dfceb504c",
					  "modtime": "2011-11-28T01:32:38.000123456Z",
					  "owner": "sha224-a794846212ff67acdd00c6b90eee492baf674d41da8a621d2e8042dd"
					},
					{
					  "blobref": "sha224-e42e49e034a0c34d959fe6d9a5b7074822bca4ce660792727c6a90a0",
					  "modtime": "2011-11-28T01:32:37.000123456Z",
					  "owner": "sha224-a794846212ff67acdd00c6b90eee492baf674d41da8a621d2e8042dd"
					}
				  ],
				  "meta": {
					"sha224-5a04f1cc2d464f4b7bfe206cf22808ae86d2572913fc603dfceb504c": {
					  "blobRef": "sha224-5a04f1cc2d464f4b7bfe206cf22808ae86d2572913fc603dfceb504c",
					  "camliType": "permanode",
					  "size": 552,
					  "permanode": {
						"attr": {
						  "camliMember": [
							"sha224-e42e49e034a0c34d959fe6d9a5b7074822bca4ce660792727c6a90a0"
						  ]
						},
						"modtime": "` + addToClockOrigin(2*time.Second) + `"
					  }
					},
					"sha224-e42e49e034a0c34d959fe6d9a5b7074822bca4ce660792727c6a90a0": {
					  "blobRef": "sha224-e42e49e034a0c34d959fe6d9a5b7074822bca4ce660792727c6a90a0",
					  "camliType": "permanode",
					  "size": 552,
					  "permanode": {
						"attr": {
						  "camliContent": [
							"sha224-37f88a4216d12c6907ea8422cf8389460e8c5c56b5e58ac1dbbcb430"
						  ]
						},
						"modtime": "` + addToClockOrigin(1*time.Second) + `"
					  }
					},
					"sha224-37f88a4216d12c6907ea8422cf8389460e8c5c56b5e58ac1dbbcb430": {
					  "blobRef": "sha224-37f88a4216d12c6907ea8422cf8389460e8c5c56b5e58ac1dbbcb430",
					  "camliType": "file",
					  "size": 202,
					  "file": {
						"fileName": "dude.jpg",
						"size": 1932,
						"mimeType": "image/jpeg",
						"wholeRef": "sha224-ed1f1bfda8e23983dd49d98748a8867fd8d926adb6aa396041a860cb"
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
			setup: func(t *testing.T) indexAndOwner {
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
							{"blobref": "sha224-e42e49e034a0c34d959fe6d9a5b7074822bca4ce660792727c6a90a0",
							 "modtime": "2011-11-28T01:32:37.000123456Z",
							 "owner": "sha224-a794846212ff67acdd00c6b90eee492baf674d41da8a621d2e8042dd"}
						],
						"meta": {
						   "sha224-e42e49e034a0c34d959fe6d9a5b7074822bca4ce660792727c6a90a0": {
				 "blobRef": "sha224-e42e49e034a0c34d959fe6d9a5b7074822bca4ce660792727c6a90a0",
				 "camliType": "permanode",
						 "permanode": {
						   "attr": { "title": [ "Some title" ] },
							"modtime": "` + addToClockOrigin(1*time.Second) + `"
						 },
						 "size": 552
							}
						}
					   }`),
		},

		// edgeto handler: put a permanode (member) in two parent
		// permanodes, then delete the second and verify that edges
		// back from member only reveal the first parent.
		{
			name: "edge-to",
			setup: func(t *testing.T) indexAndOwner {
				// Ignore the fakeindex and use the real (but in-memory) implementation,
				// using IndexDeps to populate it.
				idx := index.NewMemoryIndex()
				id := indextest.NewIndexDeps(idx)

				parent1 := id.NewPlannedPermanode("pn1") // sha224-e42e49e034a0c34d959fe6d9a5b7074822bca4ce660792727c6a90a0
				parent2 := id.NewPlannedPermanode("pn2")
				member := id.NewPlannedPermanode("member") // always sha224-4dc5db784a471953512550ccb87eaa3b526013db66b9fc93aa4441b7

				id.AddAttribute(parent1, "camliMember", member.String())
				id.AddAttribute(parent2, "camliMember", member.String())
				id.DelAttribute(parent2, "camliMember", "")
				return indexAndOwner{idx, id.SignerBlobRef}
			},
			query: "edgesto?blobref=sha224-4dc5db784a471953512550ccb87eaa3b526013db66b9fc93aa4441b7",
			want: parseJSON(`{
					"toRef": "sha224-4dc5db784a471953512550ccb87eaa3b526013db66b9fc93aa4441b7",
					"edgesTo": [
						{"from": "sha224-e42e49e034a0c34d959fe6d9a5b7074822bca4ce660792727c6a90a0",
						"fromType": "permanode"}
						]
					}`),
		},
	}
}

func marshalJSON(v any) string {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		panic(err)
	}
	return string(b)
}

func jmap(v any) map[string]any {
	m := make(map[string]any)
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

	ixo := ht.setup(t)
	idx := ixo.index
	h := NewHandler(idx, owner)

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

// TestGetPermanodeLocationAllocs helps us making sure we keep
// Handler.getPermanodeLocation (or equivalent), allocation-free.
func TestGetPermanodeLocationAllocs(t *testing.T) {
	defer index.SetVerboseCorpusLogging(true)
	index.SetVerboseCorpusLogging(false)

	idx := index.NewMemoryIndex() // string key-value pairs in memory, as if they were on disk
	idd := indextest.NewIndexDeps(idx)
	h := NewHandler(idx, owner)
	corpus, err := idx.KeepInMemory()
	if err != nil {
		t.Fatal(err)
	}
	h.SetCorpus(corpus)

	pn1 := idd.NewPermanode()
	lat := 45.18
	long := 5.72
	idd.SetAttribute(pn1, "latitude", fmt.Sprintf("%f", lat))
	idd.SetAttribute(pn1, "longitude", fmt.Sprintf("%f", long))

	pnVenue := idd.NewPermanode()
	idd.SetAttribute(pnVenue, "camliNodeType", "foursquare.com:venue")
	idd.SetAttribute(pnVenue, "latitude", fmt.Sprintf("%f", lat))
	idd.SetAttribute(pnVenue, "longitude", fmt.Sprintf("%f", long))
	pnCheckin := idd.NewPermanode()
	idd.SetAttribute(pnCheckin, "camliNodeType", "foursquare.com:checkin")
	idd.SetAttribute(pnCheckin, "foursquareVenuePermanode", pnVenue.String())

	br, _ := idd.UploadFile("photo.jpg", exifFileContentLatLong(lat, long), time.Now())
	pnPhoto := idd.NewPermanode()
	idd.SetAttribute(pnPhoto, "camliContent", br.String())

	const (
		blobParseAlloc = 1 // blob.Parse uses one alloc

		// allocs permitted in different tests
		latLongAttr         = 0 // latitude/longitude attr lookup musn't alloc
		altLocRef           = blobParseAlloc
		camliContentFileLoc = blobParseAlloc
	)

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	for _, tt := range []struct {
		title    string
		pn       blob.Ref
		maxAlloc int
	}{
		{"explicit location from attrs", pn1, latLongAttr},
		{"referenced permanode location", pnCheckin, latLongAttr + altLocRef},
		{"location from exif photo", pnPhoto, latLongAttr + camliContentFileLoc},
	} {
		n := testing.AllocsPerRun(20, func() {
			loc, err := h.ExportGetPermanodeLocation(ctx, tt.pn, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			if loc.Latitude != lat {
				t.Fatalf("wrong latitude: got %v, wanted %v", loc.Latitude, lat)
			}
			if loc.Longitude != long {
				t.Fatalf("wrong longitude: got %v, wanted %v", loc.Longitude, long)
			}
		})
		t.Logf("%s: %v allocations (max %v)", tt.title, n, tt.maxAlloc)
		if int(n) != tt.maxAlloc {
			t.Errorf("LocationHandler.PermanodeLocation should not allocate more than %d", tt.maxAlloc)
		}
	}
}
