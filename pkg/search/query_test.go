/*
Copyright 2013 The Perkeep Authors

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
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"image"
	"image/jpeg"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"go4.org/types"
	"perkeep.org/internal/geocode"
	"perkeep.org/internal/osutil"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/index"
	"perkeep.org/pkg/index/indextest"
	. "perkeep.org/pkg/search"
	"perkeep.org/pkg/test"
	"perkeep.org/pkg/types/camtypes"
)

var ctxbg = context.Background()

// indexType is one of the three ways we test the query handler code.
type indexType int

var queryType = flag.String("querytype", "", "Empty for all query types, else 'classic', 'scan', or 'build'")

const (
	indexClassic     indexType = iota // sorted key/value pairs from index.Storage
	indexCorpusScan                   // *Corpus scanned from key/value pairs on start
	indexCorpusBuild                  // empty *Corpus, built iteratively as blob received.
)

var (
	allIndexTypes  = []indexType{indexClassic, indexCorpusScan, indexCorpusBuild}
	memIndexTypes  = []indexType{indexCorpusScan, indexCorpusBuild}
	corpusTypeOnly = []indexType{indexCorpusScan}
)

func (i indexType) String() string {
	switch i {
	case indexClassic:
		return "classic"
	case indexCorpusScan:
		return "scan"
	case indexCorpusBuild:
		return "build"
	default:
		return fmt.Sprintf("unknown-index-type-%d", i)
	}
}

type queryTest struct {
	t               testing.TB
	id              *indextest.IndexDeps
	itype           indexType
	candidateSource string

	handlerOnce sync.Once
	newHandler  func() *Handler
	handler     *Handler // initialized with newHandler

	// set by wantRes if the query was successful, so we can examine some extra
	// query's results after wantRes is called. nil otherwise.
	res *SearchResult
}

func (qt *queryTest) Handler() *Handler {
	qt.handlerOnce.Do(func() { qt.handler = qt.newHandler() })
	return qt.handler
}

func testQuery(t testing.TB, fn func(*queryTest)) {
	testQueryTypes(t, allIndexTypes, fn)
}

func testQueryTypes(t testing.TB, types []indexType, fn func(*queryTest)) {
	defer test.TLog(t)()
	for _, it := range types {
		if *queryType == "" || *queryType == it.String() {
			t.Logf("Testing: --querytype=%s ...", it)
			testQueryType(t, fn, it)
		}
	}
}

func testQueryType(t testing.TB, fn func(*queryTest), itype indexType) {
	defer index.SetVerboseCorpusLogging(true)
	index.SetVerboseCorpusLogging(false)

	idx := index.NewMemoryIndex() // string key-value pairs in memory, as if they were on disk
	var err error
	var corpus *index.Corpus
	if itype == indexCorpusBuild {
		if corpus, err = idx.KeepInMemory(); err != nil {
			t.Fatal(err)
		}
	}
	qt := &queryTest{
		t:     t,
		id:    indextest.NewIndexDeps(idx),
		itype: itype,
	}
	qt.id.Fataler = t
	qt.newHandler = func() *Handler {
		h := NewHandler(idx, owner)
		if itype == indexCorpusScan {
			if corpus, err = idx.KeepInMemory(); err != nil {
				t.Fatal(err)
			}
			idx.PreventStorageAccessForTesting()
		}
		if corpus != nil {
			h.SetCorpus(corpus)
		}
		return h
	}
	fn(qt)
}

func (qt *queryTest) wantRes(req *SearchQuery, wanted ...blob.Ref) {
	if qt.itype == indexClassic {
		req.Sort = Unsorted
	}
	if qt.candidateSource != "" {
		ExportSetCandidateSourceHook(func(pickedCandidate string) {
			if pickedCandidate != qt.candidateSource {
				qt.t.Fatalf("unexpected candidateSource: got %v, want %v", pickedCandidate, qt.candidateSource)
			}
		})
	}
	res, err := qt.Handler().Query(ctxbg, req)
	if err != nil {
		qt.t.Fatal(err)
	}
	qt.res = res

	need := make(map[blob.Ref]bool)
	for _, br := range wanted {
		need[br] = true
	}
	for _, bi := range res.Blobs {
		if !need[bi.Blob] {
			qt.t.Errorf("unexpected search result: %v", bi.Blob)
		} else {
			delete(need, bi.Blob)
		}
	}
	for br := range need {
		qt.t.Errorf("missing from search result: %v", br)
	}
}

func TestQuery(t *testing.T) {
	testQuery(t, func(qt *queryTest) {
		fileRef, wholeRef := qt.id.UploadFile("file.txt", "the content", time.Unix(1382073153, 0))

		sq := &SearchQuery{
			Constraint: &Constraint{
				Anything: true,
			},
			Limit: 0,
			Sort:  UnspecifiedSort,
		}
		qt.wantRes(sq, fileRef, wholeRef)
	})
}

func TestQueryCamliType(t *testing.T) {
	testQuery(t, func(qt *queryTest) {
		fileRef, _ := qt.id.UploadFile("file.txt", "foo", time.Unix(1382073153, 0))
		sq := &SearchQuery{
			Constraint: &Constraint{
				CamliType: "file",
			},
		}
		qt.wantRes(sq, fileRef)
	})
}

func TestQueryAnyCamliType(t *testing.T) {
	testQuery(t, func(qt *queryTest) {
		fileRef, _ := qt.id.UploadFile("file.txt", "foo", time.Unix(1382073153, 0))

		sq := &SearchQuery{
			Constraint: &Constraint{
				AnyCamliType: true,
			},
		}
		qt.wantRes(sq, fileRef)
	})
}

func TestQueryBlobSize(t *testing.T) {
	testQuery(t, func(qt *queryTest) {
		_, smallFileRef := qt.id.UploadFile("file.txt", strings.Repeat("x", 5<<10), time.Unix(1382073153, 0))
		qt.id.UploadFile("file.txt", strings.Repeat("x", 20<<10), time.Unix(1382073153, 0))

		sq := &SearchQuery{
			Constraint: &Constraint{
				BlobSize: &IntConstraint{
					Min: 4 << 10,
					Max: 6 << 10,
				},
			},
		}
		qt.wantRes(sq, smallFileRef)
	})
}

func TestQueryBlobRefPrefix(t *testing.T) {
	testQuery(t, func(qt *queryTest) {
		id := qt.id

		// foo is sha224-0808f64e60d58979fcb676c96ec938270dea42445aeefcd3a4e6f8db
		id.UploadFile("file.txt", "foo", time.Unix(1382073153, 0))
		// bar is sha224-07daf010de7f7f0d8d76a76eb8d1eb40182c8d1e7a3877a6686c9bf0
		id.UploadFile("file.txt", "bar", time.Unix(1382073153, 0))

		sq := &SearchQuery{
			Constraint: &Constraint{
				BlobRefPrefix: "sha224-0",
			},
		}
		sres, err := qt.Handler().Query(ctxbg, sq)
		if err != nil {
			t.Fatal(err)
		}
		if len(sres.Blobs) < 2 {
			t.Errorf("expected at least 2 matches; got %d", len(sres.Blobs))
		}
		for _, res := range sres.Blobs {
			brStr := res.Blob.String()
			if !strings.HasPrefix(brStr, "sha224-0") {
				t.Errorf("matched blob %s didn't begin with sha224-0", brStr)
			}
		}
	})
}

func TestQueryTwoConstraints(t *testing.T) {
	testQuery(t, func(qt *queryTest) {
		id := qt.id
		a := id.UploadString("a") // sha224-abd37534c7d9a2efb9465de931cd7055ffdb8879563ae98078d6d6d5
		id.UploadString("b")      // sha224-c681e18b81edaf2b66dd22376734dba5992e362bc3f91ab225854c17
		id.UploadString("c4")     // sha224-a71e32daa65b29de49a1497d23d47e663534a4cde5e329eda508d6f5

		sq := &SearchQuery{
			Constraint: &Constraint{
				BlobRefPrefix: "sha224-a", // matches a and c4
				BlobSize: &IntConstraint{ // matches a and b
					Min: 1,
					Max: 1,
				},
			},
		}
		qt.wantRes(sq, a)
	})
}

func TestQueryLogicalOr(t *testing.T) {
	testQuery(t, func(qt *queryTest) {
		id := qt.id

		// foo is sha224-0808f64e60d58979fcb676c96ec938270dea42445aeefcd3a4e6f8db
		_, foo := id.UploadFile("file.txt", "foo", time.Unix(1382073153, 0))
		// bar is sha224-07daf010de7f7f0d8d76a76eb8d1eb40182c8d1e7a3877a6686c9bf0
		_, bar := id.UploadFile("file.txt", "bar", time.Unix(1382073153, 0))

		sq := &SearchQuery{
			Constraint: &Constraint{
				Logical: &LogicalConstraint{
					Op: "or",
					A: &Constraint{
						BlobRefPrefix: "sha224-08",
					},
					B: &Constraint{
						BlobRefPrefix: "sha224-07",
					},
				},
			},
		}
		qt.wantRes(sq, foo, bar)
	})
}

func TestQueryLogicalAnd(t *testing.T) {
	testQuery(t, func(qt *queryTest) {
		id := qt.id

		// foo is sha224-0808f64e60d58979fcb676c96ec938270dea42445aeefcd3a4e6f8db
		_, foo := id.UploadFile("file.txt", "foo", time.Unix(1382073153, 0))
		// bar.. is sha224-81d6d49dac1a465f90b0225461a90cc11a5a8675b4457762d52760aa
		id.UploadFile("file.txt", "bar..", time.Unix(1382073153, 0))

		sq := &SearchQuery{
			Constraint: &Constraint{
				Logical: &LogicalConstraint{
					Op: "and",
					A: &Constraint{
						BlobRefPrefix: "sha224-08",
					},
					B: &Constraint{
						BlobSize: &IntConstraint{
							Max: int64(len("foo")), // excludes "bar.."
						},
					},
				},
			},
		}
		qt.wantRes(sq, foo)
	})
}

func TestQueryLogicalXor(t *testing.T) {
	testQuery(t, func(qt *queryTest) {
		id := qt.id

		// foo is sha224-0808f64e60d58979fcb676c96ec938270dea42445aeefcd3a4e6f8db
		_, foo := id.UploadFile("file.txt", "foo", time.Unix(1382073153, 0))
		// bar is sha224-07daf010de7f7f0d8d76a76eb8d1eb40182c8d1e7a3877a6686c9bf0
		id.UploadFile("file.txt", "bar", time.Unix(1382073153, 0))

		sq := &SearchQuery{
			Constraint: &Constraint{
				Logical: &LogicalConstraint{
					Op: "xor",
					A: &Constraint{
						BlobRefPrefix: "sha224-0",
					},
					B: &Constraint{
						BlobRefPrefix: "sha224-07da",
					},
				},
			},
		}
		qt.wantRes(sq, foo)
	})
}

func TestQueryLogicalNot(t *testing.T) {
	testQuery(t, func(qt *queryTest) {
		id := qt.id

		// foo is sha224-0808f64e60d58979fcb676c96ec938270dea42445aeefcd3a4e6f8db
		_, foo := id.UploadFile("file.txt", "foo", time.Unix(1382073153, 0))
		// bar is sha224-07daf010de7f7f0d8d76a76eb8d1eb40182c8d1e7a3877a6686c9bf0
		_, bar := id.UploadFile("file.txt", "bar", time.Unix(1382073153, 0))

		sq := &SearchQuery{
			Constraint: &Constraint{
				Logical: &LogicalConstraint{
					Op: "not",
					A: &Constraint{
						CamliType: "file",
					},
				},
			},
		}
		qt.wantRes(sq, foo, bar)
	})
}

func TestQueryPermanodeAttrExact(t *testing.T) {
	testQuery(t, func(qt *queryTest) {
		id := qt.id

		p1 := id.NewPlannedPermanode("1")
		p2 := id.NewPlannedPermanode("2")
		id.SetAttribute(p1, "someAttr", "value1")
		id.SetAttribute(p2, "someAttr", "value2")

		sq := &SearchQuery{
			Constraint: &Constraint{
				Permanode: &PermanodeConstraint{
					Attr:  "someAttr",
					Value: "value1",
				},
			},
		}
		qt.wantRes(sq, p1)
	})
}

func TestQueryPermanodeAttrMatches(t *testing.T) {
	testQuery(t, func(qt *queryTest) {
		id := qt.id

		p1 := id.NewPlannedPermanode("1")
		p2 := id.NewPlannedPermanode("2")
		p3 := id.NewPlannedPermanode("3")
		id.SetAttribute(p1, "someAttr", "value1")
		id.SetAttribute(p2, "someAttr", "value2")
		id.SetAttribute(p3, "someAttr", "NOT starting with value")

		sq := &SearchQuery{
			Constraint: &Constraint{
				Permanode: &PermanodeConstraint{
					Attr: "someAttr",
					ValueMatches: &StringConstraint{
						HasPrefix: "value",
					},
				},
			},
		}
		qt.wantRes(sq, p1, p2)
	})
}

func TestQueryPermanodeAttrNumValue(t *testing.T) {
	testQuery(t, func(qt *queryTest) {
		id := qt.id

		// TODO(bradfitz): if we set an empty attribute value here and try to search
		// by NumValue IntConstraint Min = 1, it fails only in classic (no corpus) mode.
		// Something there must be skipping empty values.
		p1 := id.NewPlannedPermanode("1")
		id.AddAttribute(p1, "x", "1")
		id.AddAttribute(p1, "x", "2")
		p2 := id.NewPlannedPermanode("2")
		id.AddAttribute(p2, "x", "1")
		id.AddAttribute(p2, "x", "2")
		id.AddAttribute(p2, "x", "3")

		sq := &SearchQuery{
			Constraint: &Constraint{
				Permanode: &PermanodeConstraint{
					Attr: "x",
					NumValue: &IntConstraint{
						Min: 3,
					},
				},
			},
		}
		qt.wantRes(sq, p2)
	})
}

// Tests that NumValue queries with ZeroMax return permanodes without any values.
func TestQueryPermanodeAttrNumValueZeroMax(t *testing.T) {
	testQuery(t, func(qt *queryTest) {
		id := qt.id

		p1 := id.NewPlannedPermanode("1")
		id.AddAttribute(p1, "x", "1")
		p2 := id.NewPlannedPermanode("2")
		id.AddAttribute(p2, "y", "1") // Permanodes without any attributes are ignored.

		sq := &SearchQuery{
			Constraint: &Constraint{
				Permanode: &PermanodeConstraint{
					Attr: "x",
					NumValue: &IntConstraint{
						ZeroMax: true,
					},
				},
			},
		}
		qt.wantRes(sq, p2)
	})
}

// find a permanode (p2) that has a property being a blobref pointing
// to a sub-query
func TestQueryPermanodeAttrValueInSet(t *testing.T) {
	testQuery(t, func(qt *queryTest) {
		id := qt.id

		p1 := id.NewPlannedPermanode("1")
		id.SetAttribute(p1, "bar", "baz")
		p2 := id.NewPlannedPermanode("2")
		id.SetAttribute(p2, "foo", p1.String())

		sq := &SearchQuery{
			Constraint: &Constraint{
				Permanode: &PermanodeConstraint{
					Attr: "foo",
					ValueInSet: &Constraint{
						Permanode: &PermanodeConstraint{
							Attr:  "bar",
							Value: "baz",
						},
					},
				},
			},
		}
		qt.wantRes(sq, p2)
	})
}

// Tests PermanodeConstraint.ValueMatchesInt.
func TestQueryPermanodeValueMatchesInt(t *testing.T) {
	testQuery(t, func(qt *queryTest) {
		id := qt.id

		p1 := id.NewPlannedPermanode("1")
		p2 := id.NewPlannedPermanode("2")
		p3 := id.NewPlannedPermanode("3")
		p4 := id.NewPlannedPermanode("4")
		p5 := id.NewPlannedPermanode("5")
		id.SetAttribute(p1, "x", "-5")
		id.SetAttribute(p2, "x", "0")
		id.SetAttribute(p3, "x", "2")
		id.SetAttribute(p4, "x", "10.0")
		id.SetAttribute(p5, "x", "abc")

		sq := &SearchQuery{
			Constraint: &Constraint{
				Permanode: &PermanodeConstraint{
					Attr: "x",
					ValueMatchesInt: &IntConstraint{
						Min: -2,
					},
				},
			},
		}
		qt.wantRes(sq, p2, p3)
	})
}

// Tests PermanodeConstraint.ValueMatchesFloat.
func TestQueryPermanodeValueMatchesFloat(t *testing.T) {
	testQuery(t, func(qt *queryTest) {
		id := qt.id

		p1 := id.NewPlannedPermanode("1")
		p2 := id.NewPlannedPermanode("2")
		p3 := id.NewPlannedPermanode("3")
		p4 := id.NewPlannedPermanode("4")
		id.SetAttribute(p1, "x", "2.5")
		id.SetAttribute(p2, "x", "5.7")
		id.SetAttribute(p3, "x", "10")
		id.SetAttribute(p4, "x", "abc")

		sq := &SearchQuery{
			Constraint: &Constraint{
				Permanode: &PermanodeConstraint{
					Attr: "x",
					ValueMatchesFloat: &FloatConstraint{
						Max: 6.0,
					},
				},
			},
		}
		qt.wantRes(sq, p1, p2)
	})
}

func TestQueryPermanodeLocation(t *testing.T) {
	testQuery(t, func(qt *queryTest) {
		id := qt.id

		p1 := id.NewPlannedPermanode("1")
		p2 := id.NewPlannedPermanode("2")
		p3 := id.NewPlannedPermanode("3")
		id.SetAttribute(p1, "latitude", "51.5")
		id.SetAttribute(p1, "longitude", "0")
		id.SetAttribute(p2, "latitude", "51.5")
		id.SetAttribute(p3, "longitude", "0")

		p4 := id.NewPlannedPermanode("checkin")
		p5 := id.NewPlannedPermanode("venue")
		id.SetAttribute(p4, "camliNodeType", "foursquare.com:checkin")
		id.SetAttribute(p4, "foursquareVenuePermanode", p5.String())
		id.SetAttribute(p5, "latitude", "1.0")
		id.SetAttribute(p5, "longitude", "2.0")

		// Upload a basic image
		srcRoot, err := osutil.PkSourceRoot()
		if err != nil {
			panic(fmt.Sprintf("source root folder not found: %v", err))
		}
		uploadFile := func(file string, modTime time.Time) blob.Ref {
			fileName := filepath.Join(srcRoot, "pkg", "search", "testdata", file)
			contents, err := os.ReadFile(fileName)
			if err != nil {
				panic(err)
			}
			br, _ := id.UploadFile(file, string(contents), modTime)
			return br
		}
		fileRef := uploadFile("dude-gps.jpg", time.Time{})

		p6 := id.NewPlannedPermanode("photo")
		id.SetAttribute(p6, "camliContent", fileRef.String())

		sq := &SearchQuery{
			Constraint: &Constraint{
				Permanode: &PermanodeConstraint{
					Location: &LocationConstraint{
						Any: true,
					},
				},
			},
		}
		qt.wantRes(sq, p1, p4, p5, p6)
	})
}

func TestQueryFileLocation(t *testing.T) {
	testQueryTypes(t, memIndexTypes, func(qt *queryTest) {
		id := qt.id

		// Upload a basic image
		srcRoot, err := osutil.PkSourceRoot()
		if err != nil {
			panic(fmt.Sprintf("source root folder not found: %v", err))
		}
		uploadFile := func(file string, modTime time.Time) blob.Ref {
			fileName := filepath.Join(srcRoot, "pkg", "search", "testdata", file)
			contents, err := os.ReadFile(fileName)
			if err != nil {
				panic(err)
			}
			br, _ := id.UploadFile(file, string(contents), modTime)
			return br
		}
		fileRef := uploadFile("dude-gps.jpg", time.Time{})

		p6 := id.NewPlannedPermanode("photo")
		id.SetAttribute(p6, "camliContent", fileRef.String())

		sq := &SearchQuery{
			Constraint: &Constraint{
				File: &FileConstraint{
					Location: &LocationConstraint{
						Any: true,
					},
				},
			},
		}

		qt.wantRes(sq, fileRef)
		if qt.res == nil {
			t.Fatal("No results struct")
		}
		if qt.res.LocationArea == nil {
			t.Fatal("No location area in results")
		}
		want := camtypes.LocationBounds{
			North: 42.45,
			South: 42.45,
			West:  18.76,
			East:  18.76,
		}
		if *qt.res.LocationArea != want {
			t.Fatalf("Wrong location area expansion: wanted %#v, got %#v", want, *qt.res.LocationArea)
		}

		ExportSetExpandLocationHook(true)
		qt.wantRes(sq)
		if qt.res == nil {
			t.Fatal("No results struct")
		}
		if qt.res.LocationArea != nil {
			t.Fatalf("Location area should not have been expanded")
		}
		ExportSetExpandLocationHook(false)

	})
}

// find permanodes matching a certain file query
func TestQueryFileConstraint(t *testing.T) {
	testQuery(t, func(qt *queryTest) {
		id := qt.id
		fileRef, _ := id.UploadFile("some-stuff.txt", "hello", time.Unix(123, 0))
		qt.t.Logf("fileRef = %q", fileRef)
		p1 := id.NewPlannedPermanode("1")
		id.SetAttribute(p1, "camliContent", fileRef.String())

		fileRef2, _ := id.UploadFile("other-file", "hellooooo", time.Unix(456, 0))
		qt.t.Logf("fileRef2 = %q", fileRef2)
		p2 := id.NewPlannedPermanode("2")
		id.SetAttribute(p2, "camliContent", fileRef2.String())

		sq := &SearchQuery{
			Constraint: &Constraint{
				Permanode: &PermanodeConstraint{
					Attr: "camliContent",
					ValueInSet: &Constraint{
						File: &FileConstraint{
							FileName: &StringConstraint{
								Contains: "-stuff",
							},
							FileSize: &IntConstraint{
								Max: 5,
							},
						},
					},
				},
			},
		}
		qt.wantRes(sq, p1)
	})
}

// find a directory with a name
func TestQueryDirConstraint(t *testing.T) {
	testQuery(t, func(qt *queryTest) {
		id := qt.id
		dirRef := id.UploadDir("somedir", []blob.Ref{}, time.Unix(789, 0))
		qt.t.Logf("dirRef = %q", dirRef)
		p1 := id.NewPlannedPermanode("1")
		id.SetAttribute(p1, "camliContent", dirRef.String())

		fileRef3, _ := id.UploadFile("other-file", "hellooooo", time.Unix(101112, 0))
		qt.t.Logf("fileRef3 = %q", fileRef3)
		p2 := id.NewPlannedPermanode("2")
		id.SetAttribute(p2, "camliContent", fileRef3.String())

		sq := &SearchQuery{
			Constraint: &Constraint{
				Dir: &DirConstraint{
					FileName: &StringConstraint{
						Contains: "somedir",
					},
				},
			},
		}
		qt.wantRes(sq, dirRef)
	})
}

// find permanode with a dir that contains a certain file
func TestQueryDirWithFileConstraint(t *testing.T) {
	testQuery(t, func(qt *queryTest) {
		id := qt.id
		fileRef1, _ := id.UploadFile("some-stuff.txt", "hello", time.Unix(123, 0))
		qt.t.Logf("fileRef1 = %q", fileRef1)
		fileRef2, _ := id.UploadFile("more-stuff.txt", "world", time.Unix(456, 0))
		qt.t.Logf("fileRef2 = %q", fileRef2)
		dirRef := id.UploadDir("somedir", []blob.Ref{fileRef1, fileRef2}, time.Unix(789, 0))
		qt.t.Logf("dirRef = %q", dirRef)
		p1 := id.NewPlannedPermanode("1")
		id.SetAttribute(p1, "camliContent", dirRef.String())

		fileRef3, _ := id.UploadFile("other-file", "hellooooo", time.Unix(101112, 0))
		qt.t.Logf("fileRef3 = %q", fileRef3)
		p2 := id.NewPlannedPermanode("2")
		id.SetAttribute(p2, "camliContent", fileRef3.String())

		sq := &SearchQuery{
			Constraint: &Constraint{
				Permanode: &PermanodeConstraint{
					Attr: "camliContent",
					ValueInSet: &Constraint{
						Dir: &DirConstraint{
							Contains: &Constraint{File: &FileConstraint{
								FileName: &StringConstraint{
									Contains: "some-stuff.txt",
								},
							}},
						},
					},
				},
			},
		}
		qt.wantRes(sq, p1)
	})
}

// find a dir that doesn't contain any text files
func TestQueryDirWithLogicalNotConstraint(t *testing.T) {
	testQuery(t, func(qt *queryTest) {
		id := qt.id
		fileRef1, _ := id.UploadFile("some-stuff.txt", "hello", time.Unix(123, 0))
		qt.t.Logf("fileRef1 = %q", fileRef1)
		fileRef2, _ := id.UploadFile("more-stuff.txt", "world", time.Unix(456, 0))
		qt.t.Logf("fileRef2 = %q", fileRef2)
		dirRef1 := id.UploadDir("somedir", []blob.Ref{fileRef1, fileRef2}, time.Unix(789, 0))
		qt.t.Logf("dirRef1 = %q", dirRef1)
		p1 := id.NewPlannedPermanode("1")
		id.SetAttribute(p1, "camliContent", dirRef1.String())

		fileRef3, _ := id.UploadFile("other-file", "hellooooo", time.Unix(101112, 0))
		qt.t.Logf("fileRef3 = %q", fileRef3)
		dirRef2 := id.UploadDir("anotherdir", []blob.Ref{fileRef3}, time.Unix(181112, 0))
		qt.t.Logf("dirRef2 = %q", dirRef2)
		p2 := id.NewPlannedPermanode("2")
		id.SetAttribute(p2, "camliContent", dirRef2.String())

		sq := &SearchQuery{
			Constraint: &Constraint{
				Dir: &DirConstraint{
					Contains: &Constraint{
						Logical: &LogicalConstraint{
							A: &Constraint{
								File: &FileConstraint{
									FileName: &StringConstraint{
										HasSuffix: ".txt",
									},
								},
							},
							Op: "not",
						},
					},
				},
			},
		}
		qt.wantRes(sq, dirRef2)
	})
}

// find permanode with a dir that contains a certain file or dir
func TestQueryDirWithFileOrDirConstraint(t *testing.T) {
	testQuery(t, func(qt *queryTest) {
		id := qt.id
		fileRef1, _ := id.UploadFile("some-stuff.txt", "hello", time.Unix(123, 0))
		qt.t.Logf("fileRef1 = %q", fileRef1)
		childDirRef := id.UploadDir("childdir", []blob.Ref{}, time.Unix(457, 0))
		qt.t.Logf("childDirRef = %q", childDirRef)
		dirRef := id.UploadDir("somedir", []blob.Ref{fileRef1, childDirRef}, time.Unix(789, 0))
		qt.t.Logf("dirRef = %q", dirRef)
		p1 := id.NewPlannedPermanode("1")
		id.SetAttribute(p1, "camliContent", dirRef.String())

		fileRef3, _ := id.UploadFile("other-file", "hellooooo", time.Unix(101112, 0))
		qt.t.Logf("fileRef3 = %q", fileRef3)
		p2 := id.NewPlannedPermanode("2")
		id.SetAttribute(p2, "camliContent", fileRef3.String())

		sq := &SearchQuery{
			Constraint: &Constraint{
				Permanode: &PermanodeConstraint{
					Attr: "camliContent",
					ValueInSet: &Constraint{
						Dir: &DirConstraint{
							Contains: &Constraint{Logical: &LogicalConstraint{
								A: &Constraint{File: &FileConstraint{
									FileName: &StringConstraint{
										Equals: "foobar",
									},
								}},
								B: &Constraint{Dir: &DirConstraint{
									FileName: &StringConstraint{
										Equals: "childdir",
									},
								}},
								Op: "or",
							}},
						},
					},
				},
			},
		}
		qt.wantRes(sq, p1)
	})
}

// find children of a directory, by name.
// in practice, one can also get the children with the proper describe rules,
// but doing so has some limitations that a direct search query has not.
func TestQueryDirChildrenByNameConstraint(t *testing.T) {
	testQueryTypes(t, memIndexTypes, func(qt *queryTest) {
		id := qt.id
		fileRef1, _ := id.UploadFile("some-stuff.txt", "hello", time.Unix(123, 0))
		qt.t.Logf("fileRef1 = %q", fileRef1)
		fileRef2, _ := id.UploadFile("more-stuff.txt", "world", time.Unix(456, 0))
		qt.t.Logf("fileRef2 = %q", fileRef2)
		childDirRef := id.UploadDir("childdir", []blob.Ref{}, time.Unix(457, 0))
		qt.t.Logf("childDirRef = %q", childDirRef)
		dirRef := id.UploadDir("somedir", []blob.Ref{fileRef1, fileRef2, childDirRef}, time.Unix(789, 0))
		qt.t.Logf("dirRef = %q", dirRef)
		p1 := id.NewPlannedPermanode("1")
		id.SetAttribute(p1, "camliContent", dirRef.String())

		fileRef3, _ := id.UploadFile("other-file", "hellooooo", time.Unix(101112, 0))
		qt.t.Logf("fileRef3 = %q", fileRef3)
		p2 := id.NewPlannedPermanode("2")
		id.SetAttribute(p2, "camliContent", fileRef3.String())

		sq := &SearchQuery{
			Constraint: &Constraint{
				Logical: &LogicalConstraint{
					A: &Constraint{File: &FileConstraint{
						ParentDir: &DirConstraint{
							FileName: &StringConstraint{
								Equals: "somedir",
							},
						},
					}},
					B: &Constraint{Dir: &DirConstraint{
						ParentDir: &DirConstraint{
							FileName: &StringConstraint{
								Equals: "somedir",
							},
						},
					}},
					Op: "or",
				},
			},
		}
		qt.wantRes(sq, fileRef1, fileRef2, childDirRef)
	})
}

// find children of a directory, by blobref.
func TestQueryDirChildrenByRefConstraint(t *testing.T) {
	testQueryTypes(t, memIndexTypes, func(qt *queryTest) {
		id := qt.id
		fileRef1, _ := id.UploadFile("some-stuff.txt", "hello", time.Unix(123, 0))
		qt.t.Logf("fileRef1 = %q", fileRef1)
		fileRef2, _ := id.UploadFile("more-stuff.txt", "world", time.Unix(456, 0))
		qt.t.Logf("fileRef2 = %q", fileRef2)
		childDirRef := id.UploadDir("childdir", []blob.Ref{}, time.Unix(457, 0))
		qt.t.Logf("childDirRef = %q", childDirRef)
		dirRef := id.UploadDir("somedir", []blob.Ref{fileRef1, fileRef2, childDirRef}, time.Unix(789, 0))
		qt.t.Logf("dirRef = %q", dirRef)
		p1 := id.NewPlannedPermanode("1")
		id.SetAttribute(p1, "camliContent", dirRef.String())

		fileRef3, _ := id.UploadFile("other-file", "hellooooo", time.Unix(101112, 0))
		qt.t.Logf("fileRef3 = %q", fileRef3)
		p2 := id.NewPlannedPermanode("2")
		id.SetAttribute(p2, "camliContent", fileRef3.String())

		sq := &SearchQuery{
			Constraint: &Constraint{
				Logical: &LogicalConstraint{
					A: &Constraint{File: &FileConstraint{
						ParentDir: &DirConstraint{
							BlobRefPrefix: dirRef.String(),
						},
					}},
					B: &Constraint{Dir: &DirConstraint{
						ParentDir: &DirConstraint{
							BlobRefPrefix: dirRef.String(),
						},
					}},
					Op: "or",
				},
			},
		}
		qt.wantRes(sq, fileRef1, fileRef2, childDirRef)
	})
}

// find out if a file is amongst a dir's progeny (grand-children)
func TestQueryDirProgeny(t *testing.T) {
	testQuery(t, func(qt *queryTest) {
		id := qt.id
		grandchild1, _ := id.UploadFile("grandchild1.txt", "hello", time.Unix(123, 0))
		qt.t.Logf("grandchild1 = %q", grandchild1)
		grandchild2, _ := id.UploadFile("grandchild2.txt", "world", time.Unix(456, 0))
		qt.t.Logf("grandchild2 = %q", grandchild2)
		parentdir := id.UploadDir("parentdir", []blob.Ref{grandchild1, grandchild2}, time.Unix(789, 0))
		qt.t.Logf("parentdir = %q", parentdir)
		grandparentdir := id.UploadDir("grandparentdir", []blob.Ref{parentdir}, time.Unix(101112, 0))
		qt.t.Logf("grandparentdir = %q", grandparentdir)
		p1 := id.NewPlannedPermanode("1")
		id.SetAttribute(p1, "camliContent", grandparentdir.String())

		p3 := id.NewPlannedPermanode("3")
		id.SetAttribute(p3, "camliContent", parentdir.String())

		// adding an unrelated directory, to make sure we do _not_ find it as well
		fileRef3, _ := id.UploadFile("other-file", "hellooooo", time.Unix(131415, 0))
		qt.t.Logf("fileRef3 = %q", fileRef3)
		otherdir := id.UploadDir("otherdir", []blob.Ref{fileRef3}, time.Unix(161718, 0))
		qt.t.Logf("otherdir = %q", otherdir)
		p2 := id.NewPlannedPermanode("2")
		id.SetAttribute(p2, "camliContent", otherdir.String())

		sq := &SearchQuery{
			Constraint: &Constraint{
				Permanode: &PermanodeConstraint{
					Attr: "camliContent",
					ValueInSet: &Constraint{
						Dir: &DirConstraint{
							RecursiveContains: &Constraint{File: &FileConstraint{
								FileName: &StringConstraint{
									Contains: "grandchild1.txt",
								},
							}},
						},
					},
				},
			},
		}
		qt.wantRes(sq, p1, p3)

		// make sure that "Contains" only finds the direct parent, and not the grand-parent as well.
		// also this time, skip the permanode layer.
		sq = &SearchQuery{
			Constraint: &Constraint{
				Dir: &DirConstraint{
					Contains: &Constraint{
						BlobRefPrefix: grandchild1.String(),
					},
				},
			},
		}
		qt.wantRes(sq, parentdir)
	})
}

func TestQueryFileConstraint_WholeRef(t *testing.T) {
	testQueryTypes(t, memIndexTypes, func(qt *queryTest) {
		id := qt.id
		fileRef, _ := id.UploadFile("some-stuff.txt", "hello", time.Unix(123, 0))
		qt.t.Logf("fileRef = %q", fileRef)
		p1 := id.NewPlannedPermanode("1")
		id.SetAttribute(p1, "camliContent", fileRef.String())

		fileRef2, _ := id.UploadFile("other-file", "hellooooo", time.Unix(456, 0))
		qt.t.Logf("fileRef2 = %q", fileRef2)
		p2 := id.NewPlannedPermanode("2")
		id.SetAttribute(p2, "camliContent", fileRef2.String())

		sq := &SearchQuery{
			Constraint: &Constraint{
				Permanode: &PermanodeConstraint{
					Attr: "camliContent",
					ValueInSet: &Constraint{
						File: &FileConstraint{
							WholeRef: blob.RefFromString("hello"),
						},
					},
				},
			},
		}
		qt.wantRes(sq, p1)
	})
}

func TestQueryPermanodeModtime(t *testing.T) {
	testQuery(t, func(qt *queryTest) {
		id := qt.id

		// indextest advances time one second per operation:
		p1 := id.NewPlannedPermanode("1")
		p2 := id.NewPlannedPermanode("2")
		p3 := id.NewPlannedPermanode("3")
		id.SetAttribute(p1, "someAttr", "value1") // 2011-11-28 01:32:37.000123456 +0000 UTC 1322443957
		id.SetAttribute(p2, "someAttr", "value2") // 2011-11-28 01:32:38.000123456 +0000 UTC 1322443958
		id.SetAttribute(p3, "someAttr", "value3") // 2011-11-28 01:32:39.000123456 +0000 UTC 1322443959

		sq := &SearchQuery{
			Constraint: &Constraint{
				Permanode: &PermanodeConstraint{
					ModTime: &TimeConstraint{
						After:  types.Time3339(time.Unix(1322443957, 456789)),
						Before: types.Time3339(time.Unix(1322443959, 0)),
					},
				},
			},
		}
		qt.wantRes(sq, p2)
	})
}

// This really belongs in pkg/index for the index-vs-corpus tests, but
// it's easier here for now.
// TODO: make all the indextest/tests.go
// also test the three memory build modes that testQuery does.
func TestDecodeFileInfo(t *testing.T) {
	ctx := context.Background()
	testQuery(t, func(qt *queryTest) {
		id := qt.id
		fileRef, wholeRef := id.UploadFile("file.gif", "GIF87afoo", time.Unix(456, 0))
		res, err := qt.Handler().Describe(ctx, &DescribeRequest{
			BlobRef: fileRef,
		})
		if err != nil {
			qt.t.Error(err)
			return
		}
		db := res.Meta[fileRef.String()]
		if db == nil {
			qt.t.Error("DescribedBlob missing")
			return
		}
		if db.File == nil {
			qt.t.Error("DescribedBlob.File is nil")
			return
		}
		if db.File.MIMEType != "image/gif" {
			qt.t.Errorf("DescribedBlob.File = %+v; mime type is not image/gif", db.File)
			return
		}
		if db.File.WholeRef != wholeRef {
			qt.t.Errorf("DescribedBlob.WholeRef: got %v, wanted %v", wholeRef, db.File.WholeRef)
			return
		}
	})
}

func TestQueryFileCandidateSource(t *testing.T) {
	testQueryTypes(t, memIndexTypes, func(qt *queryTest) {
		id := qt.id
		fileRef, _ := id.UploadFile("some-stuff.txt", "hello", time.Unix(123, 0))
		qt.t.Logf("fileRef = %q", fileRef)
		p1 := id.NewPlannedPermanode("1")
		id.SetAttribute(p1, "camliContent", fileRef.String())

		sq := &SearchQuery{
			Constraint: &Constraint{
				File: &FileConstraint{
					WholeRef: blob.RefFromString("hello"),
				},
			},
		}
		qt.candidateSource = "corpus_file_meta"
		qt.wantRes(sq, fileRef)
	})
}

func TestQueryRecentPermanodes_UnspecifiedSort(t *testing.T) {
	testQueryRecentPermanodes(t, UnspecifiedSort, "corpus_permanode_created")
}

func TestQueryRecentPermanodes_LastModifiedDesc(t *testing.T) {
	testQueryRecentPermanodes(t, LastModifiedDesc, "corpus_permanode_lastmod")
}

func TestQueryRecentPermanodes_CreatedDesc(t *testing.T) {
	testQueryRecentPermanodes(t, CreatedDesc, "corpus_permanode_created")
}

func testQueryRecentPermanodes(t *testing.T, sortType SortType, source string) {
	testQueryTypes(t, memIndexTypes, func(qt *queryTest) {
		id := qt.id

		p1 := id.NewPlannedPermanode("1")
		id.SetAttribute(p1, "foo", "p1")
		p2 := id.NewPlannedPermanode("2")
		id.SetAttribute(p2, "foo", "p2")
		p3 := id.NewPlannedPermanode("3")
		id.SetAttribute(p3, "foo", "p3")

		var usedSource string
		ExportSetCandidateSourceHook(func(s string) {
			usedSource = s
		})

		req := &SearchQuery{
			Constraint: &Constraint{
				Permanode: &PermanodeConstraint{},
			},
			Limit:    2,
			Sort:     sortType,
			Describe: &DescribeRequest{},
		}
		handler := qt.Handler()
		res, err := handler.Query(ctxbg, req)
		if err != nil {
			qt.t.Fatal(err)
		}
		if usedSource != source {
			t.Errorf("used candidate source strategy %q; want %v", usedSource, source)
		}
		wantBlobs := []*SearchResultBlob{
			{Blob: p3},
			{Blob: p2},
		}
		if !reflect.DeepEqual(res.Blobs, wantBlobs) {
			gotj, wantj := prettyJSON(res.Blobs), prettyJSON(wantBlobs)
			t.Errorf("Got blobs:\n%s\nWant:\n%s\n", gotj, wantj)
		}
		if got := len(res.Describe.Meta); got != 2 {
			t.Errorf("got %d described blobs; want 2", got)
		}

		// And test whether continue (for infinite scroll) works:
		{
			if got, want := res.Continue, "pn:1322443958000123456:sha224-e1bd2812721791c0d087778220fa307e059da6501a1a4fd7a9f34703"; got != want {
				t.Fatalf("Continue token = %q; want %q", got, want)
			}
			req := &SearchQuery{
				Constraint: &Constraint{
					Permanode: &PermanodeConstraint{},
				},
				Limit:    2,
				Sort:     sortType,
				Continue: res.Continue,
			}
			res, err := handler.Query(ctxbg, req)
			if err != nil {
				qt.t.Fatal(err)
			}
			wantBlobs := []*SearchResultBlob{{Blob: p1}}
			if !reflect.DeepEqual(res.Blobs, wantBlobs) {
				gotj, wantj := prettyJSON(res.Blobs), prettyJSON(wantBlobs)
				t.Errorf("After scroll, got blobs:\n%s\nWant:\n%s\n", gotj, wantj)
			}
		}
	})
}

func TestQueryRecentPermanodes_Continue_UnspecifiedSort(t *testing.T) {
	testQueryRecentPermanodes_Continue(t, UnspecifiedSort)
}

func TestQueryRecentPermanodes_Continue_LastModifiedDesc(t *testing.T) {
	testQueryRecentPermanodes_Continue(t, LastModifiedDesc)
}

func TestQueryRecentPermanodes_Continue_CreatedDesc(t *testing.T) {
	testQueryRecentPermanodes_Continue(t, CreatedDesc)
}

// Tests the continue token on recent permanodes, notably when the
// page limit truncates in the middle of a bunch of permanodes with the
// same modtime.
func testQueryRecentPermanodes_Continue(t *testing.T, sortType SortType) {
	testQueryTypes(t, memIndexTypes, func(qt *queryTest) {
		id := qt.id

		var blobs []blob.Ref
		for i := 1; i <= 4; i++ {
			pn := id.NewPlannedPermanode(fmt.Sprint(i))
			blobs = append(blobs, pn)
			t.Logf("permanode %d is %v", i, pn)
			id.SetAttribute_NoTimeMove(pn, "foo", "bar")
		}
		sort.Sort(blob.ByRef(blobs))
		for i, br := range blobs {
			t.Logf("Sorted %d = %v", i, br)
		}
		handler := qt.Handler()

		contToken := ""
		tests := [][]blob.Ref{
			{blobs[3], blobs[2]},
			{blobs[1], blobs[0]},
			{},
		}

		for i, wantBlobs := range tests {
			req := &SearchQuery{
				Constraint: &Constraint{
					Permanode: &PermanodeConstraint{},
				},
				Limit:    2,
				Sort:     sortType,
				Continue: contToken,
			}
			res, err := handler.Query(ctxbg, req)
			if err != nil {
				qt.t.Fatalf("Error on query %d: %v", i+1, err)
			}
			t.Logf("Query %d/%d: continue = %q", i+1, len(tests), res.Continue)
			for i, sb := range res.Blobs {
				t.Logf("  res[%d]: %v", i, sb.Blob)
			}

			var want []*SearchResultBlob
			for _, br := range wantBlobs {
				want = append(want, &SearchResultBlob{Blob: br})
			}
			if !reflect.DeepEqual(res.Blobs, want) {
				gotj, wantj := prettyJSON(res.Blobs), prettyJSON(want)
				t.Fatalf("Query %d: Got blobs:\n%s\nWant:\n%s\n", i+1, gotj, wantj)
			}
			contToken = res.Continue
			haveToken := contToken != ""
			wantHaveToken := (i + 1) < len(tests)
			if haveToken != wantHaveToken {
				t.Fatalf("Query %d: token = %q; want token = %v", i+1, contToken, wantHaveToken)
			}
		}
	})
}

func TestQueryRecentPermanodes_ContinueEndMidPage_UnspecifiedSort(t *testing.T) {
	testQueryRecentPermanodes_ContinueEndMidPage(t, UnspecifiedSort)
}

func TestQueryRecentPermanodes_ContinueEndMidPage_LastModifiedDesc(t *testing.T) {
	testQueryRecentPermanodes_ContinueEndMidPage(t, LastModifiedDesc)
}

func TestQueryRecentPermanodes_ContinueEndMidPage_CreatedDesc(t *testing.T) {
	testQueryRecentPermanodes_ContinueEndMidPage(t, CreatedDesc)
}

// Tests continue token hitting the end mid-page.
func testQueryRecentPermanodes_ContinueEndMidPage(t *testing.T, sortType SortType) {
	testQueryTypes(t, memIndexTypes, func(qt *queryTest) {
		id := qt.id

		var blobs []blob.Ref
		for i := 1; i <= 3; i++ {
			pn := id.NewPlannedPermanode(fmt.Sprint(i))
			blobs = append(blobs, pn)
			t.Logf("permanode %d is %v", i, pn)
			id.SetAttribute_NoTimeMove(pn, "foo", "bar")
		}
		sort.Sort(blob.ByRef(blobs))
		for i, br := range blobs {
			t.Logf("Sorted %d = %v", i, br)
		}
		handler := qt.Handler()

		contToken := ""
		tests := [][]blob.Ref{
			{blobs[2], blobs[1]},
			{blobs[0]},
		}

		for i, wantBlobs := range tests {
			req := &SearchQuery{
				Constraint: &Constraint{
					Permanode: &PermanodeConstraint{},
				},
				Limit:    2,
				Sort:     sortType,
				Continue: contToken,
			}
			res, err := handler.Query(ctxbg, req)
			if err != nil {
				qt.t.Fatalf("Error on query %d: %v", i+1, err)
			}
			t.Logf("Query %d/%d: continue = %q", i+1, len(tests), res.Continue)
			for i, sb := range res.Blobs {
				t.Logf("  res[%d]: %v", i, sb.Blob)
			}

			var want []*SearchResultBlob
			for _, br := range wantBlobs {
				want = append(want, &SearchResultBlob{Blob: br})
			}
			if !reflect.DeepEqual(res.Blobs, want) {
				gotj, wantj := prettyJSON(res.Blobs), prettyJSON(want)
				t.Fatalf("Query %d: Got blobs:\n%s\nWant:\n%s\n", i+1, gotj, wantj)
			}
			contToken = res.Continue
			haveToken := contToken != ""
			wantHaveToken := (i + 1) < len(tests)
			if haveToken != wantHaveToken {
				t.Fatalf("Query %d: token = %q; want token = %v", i+1, contToken, wantHaveToken)
			}
		}
	})
}

// Tests PermanodeConstraint.ValueAll
func TestQueryPermanodeValueAll(t *testing.T) {
	testQuery(t, func(qt *queryTest) {
		id := qt.id

		p1 := id.NewPlannedPermanode("1")
		p2 := id.NewPlannedPermanode("2")
		id.SetAttribute(p1, "attr", "foo")
		id.SetAttribute(p1, "attr", "barrrrr")
		id.SetAttribute(p2, "attr", "foo")
		id.SetAttribute(p2, "attr", "bar")

		sq := &SearchQuery{
			Constraint: &Constraint{
				Permanode: &PermanodeConstraint{
					Attr:     "attr",
					ValueAll: true,
					ValueMatches: &StringConstraint{
						ByteLength: &IntConstraint{
							Min: 3,
							Max: 3,
						},
					},
				},
			},
		}
		qt.wantRes(sq, p2)
	})
}

// Tests PermanodeConstraint.ValueMatches.CaseInsensitive.
func TestQueryPermanodeValueMatchesCaseInsensitive(t *testing.T) {
	testQuery(t, func(qt *queryTest) {
		id := qt.id

		p1 := id.NewPlannedPermanode("1")
		p2 := id.NewPlannedPermanode("2")

		id.SetAttribute(p1, "x", "Foo")
		id.SetAttribute(p2, "x", "start")

		sq := &SearchQuery{
			Constraint: &Constraint{
				Logical: &LogicalConstraint{
					Op: "or",

					A: &Constraint{
						Permanode: &PermanodeConstraint{
							Attr: "x",
							ValueMatches: &StringConstraint{
								Equals:          "foo",
								CaseInsensitive: true,
							},
						},
					},

					B: &Constraint{
						Permanode: &PermanodeConstraint{
							Attr: "x",
							ValueMatches: &StringConstraint{
								Contains:        "TAR",
								CaseInsensitive: true,
							},
						},
					},
				},
			},
		}
		qt.wantRes(sq, p1, p2)
	})
}

func TestQueryChildren(t *testing.T) {
	testQueryTypes(t, memIndexTypes, func(qt *queryTest) {
		id := qt.id

		pdir := id.NewPlannedPermanode("some_dir")
		p1 := id.NewPlannedPermanode("1")
		p2 := id.NewPlannedPermanode("2")
		p3 := id.NewPlannedPermanode("3")

		id.AddAttribute(pdir, "camliMember", p1.String())
		id.AddAttribute(pdir, "camliPath:foo", p2.String())
		id.AddAttribute(pdir, "other", p3.String())

		// Make p1, p2, and p3 actually exist. (permanodes without attributes are dead)
		id.AddAttribute(p1, "x", "x")
		id.AddAttribute(p2, "x", "x")
		id.AddAttribute(p3, "x", "x")

		sq := &SearchQuery{
			Constraint: &Constraint{
				Permanode: &PermanodeConstraint{
					Relation: &RelationConstraint{
						Relation: "parent",
						Any: &Constraint{
							BlobRefPrefix: pdir.String(),
						},
					},
				},
			},
		}
		qt.wantRes(sq, p1, p2)
	})
}

func TestQueryParent(t *testing.T) {
	testQueryTypes(t, memIndexTypes, func(qt *queryTest) {
		id := qt.id

		pdir1 := id.NewPlannedPermanode("some_dir_1")
		pdir2 := id.NewPlannedPermanode("some_dir_2")
		p1 := id.NewPlannedPermanode("1")
		p2 := id.NewPlannedPermanode("2")
		p3 := id.NewPlannedPermanode("3")

		id.AddAttribute(pdir1, "camliMember", p1.String())
		id.AddAttribute(pdir1, "camliPath:foo", p2.String())
		id.AddAttribute(pdir1, "other", p3.String())

		id.AddAttribute(pdir2, "camliPath:bar", p1.String())

		// Make p1, p2, and p3 actually exist. (permanodes without attributes are dead)
		id.AddAttribute(p1, "x", "x")
		id.AddAttribute(p2, "x", "x")
		id.AddAttribute(p3, "x", "x")

		sq := &SearchQuery{
			Constraint: &Constraint{
				Permanode: &PermanodeConstraint{
					Relation: &RelationConstraint{
						Relation: "child",
						Any: &Constraint{
							BlobRefPrefix: p1.String(),
						},
					},
				},
			},
		}
		qt.wantRes(sq, pdir1, pdir2)

		sq = &SearchQuery{
			Constraint: &Constraint{
				Permanode: &PermanodeConstraint{
					Relation: &RelationConstraint{
						Relation: "child",
						Any: &Constraint{
							BlobRefPrefix: p2.String(),
						},
					},
				},
			},
		}
		qt.wantRes(sq, pdir1)

		sq = &SearchQuery{
			Constraint: &Constraint{
				Permanode: &PermanodeConstraint{
					Relation: &RelationConstraint{
						Relation: "child",
						Any: &Constraint{
							BlobRefPrefix: p3.String(),
						},
					},
				},
			},
		}
		qt.wantRes(sq)
	})
}

// tests the algorithm for the Around parameter, when the source of blobs is
// unsorted, i.e. when the blobs get sorted right after the constraint has been
// matched, and right before Around is applied.
func testAroundUnsortedSource(limit, pos int, t *testing.T) {
	testQueryTypes(t, []indexType{indexClassic}, func(qt *queryTest) {
		id := qt.id

		var sorted []string
		unsorted := make(map[string]blob.Ref)

		addToSorted := func(i int) {
			p := id.NewPlannedPermanode(fmt.Sprintf("%d", i))
			unsorted[p.String()] = p
			sorted = append(sorted, p.String())
		}
		for i := range 10 {
			addToSorted(i)
		}
		sort.Strings(sorted)

		// Predict the results
		var want []blob.Ref
		var around blob.Ref
		lowLimit := max(pos-limit/2, 0)
		highLimit := min(lowLimit+limit, len(sorted))
		// Make the permanodes actually exist.
		for k, v := range sorted {
			pn := unsorted[v]
			id.AddAttribute(pn, "x", "x")
			if k == pos {
				around = pn
			}
			if k >= lowLimit && k < highLimit {
				want = append(want, pn)
			}
		}

		sq := &SearchQuery{
			Constraint: &Constraint{
				Permanode: &PermanodeConstraint{},
			},
			Limit:  limit,
			Around: around,
			Sort:   BlobRefAsc,
		}
		qt.wantRes(sq, want...)
	})

}

func TestQueryAroundCenter(t *testing.T) {
	testAroundUnsortedSource(4, 4, t)
}

func TestQueryAroundNear(t *testing.T) {
	testAroundUnsortedSource(5, 9, t)
}

func TestQueryAroundFar(t *testing.T) {
	testAroundUnsortedSource(3, 4, t)
}

// 13 permanodes are created. 1 of them the parent, 11 are children
// (== results), 1 is unrelated to the parent.
// limit is the limit on the number of results.
// pos is the position of the around permanode.
// note: pos is in the permanode creation order, but keep in mind
// they're enumerated in the opposite order.
func testAroundChildren(limit, pos int, t *testing.T) {
	testQueryTypes(t, memIndexTypes, func(qt *queryTest) {
		id := qt.id

		pdir := id.NewPlannedPermanode("some_dir")
		p0 := id.NewPlannedPermanode("0")
		p1 := id.NewPlannedPermanode("1")
		p2 := id.NewPlannedPermanode("2")
		p3 := id.NewPlannedPermanode("3")
		p4 := id.NewPlannedPermanode("4")
		p5 := id.NewPlannedPermanode("5")
		p6 := id.NewPlannedPermanode("6")
		p7 := id.NewPlannedPermanode("7")
		p8 := id.NewPlannedPermanode("8")
		p9 := id.NewPlannedPermanode("9")
		p10 := id.NewPlannedPermanode("10")
		p11 := id.NewPlannedPermanode("11")

		id.AddAttribute(pdir, "camliMember", p0.String())
		id.AddAttribute(pdir, "camliMember", p1.String())
		id.AddAttribute(pdir, "camliPath:foo", p2.String())
		const noMatchIndex = 3
		id.AddAttribute(pdir, "other", p3.String())
		id.AddAttribute(pdir, "camliPath:bar", p4.String())
		id.AddAttribute(pdir, "camliMember", p5.String())
		id.AddAttribute(pdir, "camliMember", p6.String())
		id.AddAttribute(pdir, "camliMember", p7.String())
		id.AddAttribute(pdir, "camliMember", p8.String())
		id.AddAttribute(pdir, "camliMember", p9.String())
		id.AddAttribute(pdir, "camliMember", p10.String())
		id.AddAttribute(pdir, "camliMember", p11.String())

		// Predict the results
		var around blob.Ref
		lowLimit := pos - limit/2
		if lowLimit <= noMatchIndex {
			// Because 3 is not included in the results
			lowLimit--
		}
		if lowLimit < 0 {
			lowLimit = 0
		}
		highLimit := lowLimit + limit
		if highLimit >= noMatchIndex {
			// Because noMatchIndex is not included in the results
			highLimit++
		}
		var want []blob.Ref
		// Make the permanodes actually exist. (permanodes without attributes are dead)
		for k, v := range []blob.Ref{p0, p1, p2, p3, p4, p5, p6, p7, p8, p9, p10, p11} {
			id.AddAttribute(v, "x", "x")
			if k == pos {
				around = v
			}
			if k != noMatchIndex && k >= lowLimit && k < highLimit {
				want = append(want, v)
			}
		}
		// invert the order because the results are appended in reverse creation order
		// because that's how we enumerate.
		revWant := make([]blob.Ref, len(want))
		for k, v := range want {
			revWant[len(want)-1-k] = v
		}

		sq := &SearchQuery{
			Constraint: &Constraint{
				Permanode: &PermanodeConstraint{
					Relation: &RelationConstraint{
						Relation: "parent",
						Any: &Constraint{
							BlobRefPrefix: pdir.String(),
						},
					},
				},
			},
			Limit:  limit,
			Around: around,
		}
		qt.wantRes(sq, revWant...)
	})

}

// TODO(mpl): more tests. at least the 0 results case.

// Around will be found in the first buffered window of results,
// because it's a position that fits within the limit.
// So it doesn't exercice the part of the algorithm that discards
// the would-be results that are not within the "around zone".
func TestQueryChildrenAroundNear(t *testing.T) {
	testAroundChildren(5, 9, t)
}

// pos is near the end of the results enumeration and the limit is small
// so this test should go through the part of the algorithm that discards
// results not within the "around zone".
func TestQueryChildrenAroundFar(t *testing.T) {
	testAroundChildren(3, 4, t)
}

// permanodes tagged "foo" or those in sets where the parent
// permanode set itself is tagged "foo".
func TestQueryPermanodeTaggedViaParent(t *testing.T) {
	t.Skip("TODO: finish implementing")

	testQuery(t, func(qt *queryTest) {
		id := qt.id

		ptagged := id.NewPlannedPermanode("tagged_photo")
		pindirect := id.NewPlannedPermanode("via_parent")
		pset := id.NewPlannedPermanode("set")
		pboth := id.NewPlannedPermanode("both") // funny directly and via its parent
		pnotfunny := id.NewPlannedPermanode("not_funny")

		id.SetAttribute(ptagged, "tag", "funny")
		id.SetAttribute(pset, "tag", "funny")
		id.SetAttribute(pboth, "tag", "funny")
		id.AddAttribute(pset, "camliMember", pindirect.String())
		id.AddAttribute(pset, "camliMember", pboth.String())
		id.SetAttribute(pnotfunny, "tag", "boring")

		sq := &SearchQuery{
			Constraint: &Constraint{
				Logical: &LogicalConstraint{
					Op: "or",

					// Those tagged funny directly:
					A: &Constraint{
						Permanode: &PermanodeConstraint{
							Attr:  "tag",
							Value: "funny",
						},
					},

					// Those tagged funny indirectly:
					B: &Constraint{
						Permanode: &PermanodeConstraint{
							Relation: &RelationConstraint{
								Relation: "ancestor",
								Any: &Constraint{
									Permanode: &PermanodeConstraint{
										Attr:  "tag",
										Value: "funny",
									},
								},
							},
						},
					},
				},
			},
		}
		qt.wantRes(sq, ptagged, pset, pboth, pindirect)
	})
}

func TestLimitDoesntDeadlock_UnspecifiedSort(t *testing.T) {
	testLimitDoesntDeadlock(t, UnspecifiedSort)
}

func TestLimitDoesntDeadlock_LastModifiedDesc(t *testing.T) {
	testLimitDoesntDeadlock(t, LastModifiedDesc)
}

func TestLimitDoesntDeadlock_CreatedDesc(t *testing.T) {
	testLimitDoesntDeadlock(t, CreatedDesc)
}

func testLimitDoesntDeadlock(t *testing.T, sortType SortType) {
	testQueryTypes(t, memIndexTypes, func(qt *queryTest) {
		id := qt.id

		const limit = 2
		for i := 0; i < ExportBufferedConst()+limit+1; i++ {
			pn := id.NewPlannedPermanode(fmt.Sprint(i))
			id.SetAttribute(pn, "foo", "bar")
		}

		req := &SearchQuery{
			Constraint: &Constraint{
				Permanode: &PermanodeConstraint{},
			},
			Limit:    limit,
			Sort:     sortType,
			Describe: &DescribeRequest{},
		}
		h := qt.Handler()
		gotRes := make(chan bool, 1)
		go func() {
			_, err := h.Query(ctxbg, req)
			if err != nil {
				qt.t.Error(err)
			}
			gotRes <- true
		}()
		select {
		case <-gotRes:
		case <-time.After(5 * time.Second):
			t.Error("timeout; deadlock?")
		}
	})
}

func prettyJSON(v any) string {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		panic(err)
	}
	return string(b)
}

func TestPlannedQuery(t *testing.T) {
	tests := []struct {
		in, want *SearchQuery
	}{
		{
			in: &SearchQuery{
				Constraint: &Constraint{
					Permanode: &PermanodeConstraint{},
				},
			},
			want: &SearchQuery{
				Sort: CreatedDesc,
				Constraint: &Constraint{
					Permanode: &PermanodeConstraint{},
				},
				Limit: 200,
			},
		},
	}
	for i, tt := range tests {
		got := tt.in.ExportPlannedQuery()
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("%d. for input:\n%s\ngot:\n%s\nwant:\n%s\n", i,
				prettyJSON(tt.in), prettyJSON(got), prettyJSON(tt.want))
		}
	}
}

func TestDescribeMarshal(t *testing.T) {
	// Empty Describe
	q := &SearchQuery{
		Describe: &DescribeRequest{},
	}
	enc, err := json.Marshal(q)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(enc), `{"around":null,"describe":{"blobref":null,"at":null}}`; got != want {
		t.Errorf("JSON: %s; want %s", got, want)
	}
	back := &SearchQuery{}
	err = json.Unmarshal(enc, back)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(q, back) {
		t.Errorf("Didn't round-trip. Got %#v; want %#v", back, q)
	}

	// DescribeRequest with multiple blobref
	q = &SearchQuery{
		Describe: &DescribeRequest{
			BlobRefs: []blob.Ref{blob.MustParse("sha-1234"), blob.MustParse("sha-abcd")},
		},
	}
	enc, err = json.Marshal(q)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(enc), `{"around":null,"describe":{"blobrefs":["sha-1234","sha-abcd"],"blobref":null,"at":null}}`; got != want {
		t.Errorf("JSON: %s; want %s", got, want)
	}
	back = &SearchQuery{}
	err = json.Unmarshal(enc, back)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(q, back) {
		t.Errorf("Didn't round-trip. Got %#v; want %#v", back, q)
	}

	// and the zero value
	q = &SearchQuery{}
	enc, err = json.Marshal(q)
	if err != nil {
		t.Fatal(err)
	}
	if string(enc) != `{"around":null}` {
		t.Errorf(`Zero value: %q; want null`, enc)
	}
}

func TestSortMarshal_UnspecifiedSort(t *testing.T) {
	testSortMarshal(t, UnspecifiedSort)
}

func TestSortMarshal_LastModifiedDesc(t *testing.T) {
	testSortMarshal(t, LastModifiedDesc)
}

func TestSortMarshal_CreatedDesc(t *testing.T) {
	testSortMarshal(t, CreatedDesc)
}

var sortMarshalWant = map[SortType]string{
	UnspecifiedSort:  `{"around":null}`,
	LastModifiedDesc: `{"sort":` + string(SortName[LastModifiedDesc]) + `,"around":null}`,
	CreatedDesc:      `{"sort":` + string(SortName[CreatedDesc]) + `,"around":null}`,
}

func testSortMarshal(t *testing.T, sortType SortType) {
	q := &SearchQuery{
		Sort: sortType,
	}
	enc, err := json.Marshal(q)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(enc), sortMarshalWant[sortType]; got != want {
		t.Errorf("JSON: %s; want %s", got, want)
	}
	back := &SearchQuery{}
	err = json.Unmarshal(enc, back)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(q, back) {
		t.Errorf("Didn't round-trip. Got %#v; want %#v", back, q)
	}

	// and the zero value
	q = &SearchQuery{}
	enc, err = json.Marshal(q)
	if err != nil {
		t.Fatal(err)
	}
	if string(enc) != `{"around":null}` {
		t.Errorf("Zero value: %s; want {}", enc)
	}
}

func BenchmarkQueryRecentPermanodes(b *testing.B) {
	b.ReportAllocs()
	testQueryTypes(b, corpusTypeOnly, func(qt *queryTest) {
		id := qt.id

		p1 := id.NewPlannedPermanode("1")
		id.SetAttribute(p1, "foo", "p1")
		p2 := id.NewPlannedPermanode("2")
		id.SetAttribute(p2, "foo", "p2")
		p3 := id.NewPlannedPermanode("3")
		id.SetAttribute(p3, "foo", "p3")

		req := &SearchQuery{
			Constraint: &Constraint{
				Permanode: &PermanodeConstraint{},
			},
			Limit:    2,
			Sort:     UnspecifiedSort,
			Describe: &DescribeRequest{},
		}

		h := qt.Handler()
		b.ResetTimer()

		for b.Loop() {
			*req.Describe = DescribeRequest{}
			_, err := h.Query(ctxbg, req)
			if err != nil {
				qt.t.Fatal(err)
			}
		}
	})
}

func BenchmarkQueryPermanodes(b *testing.B) {
	benchmarkQueryPermanodes(b, false)
}

func BenchmarkQueryDescribePermanodes(b *testing.B) {
	benchmarkQueryPermanodes(b, true)
}

func benchmarkQueryPermanodes(b *testing.B, describe bool) {
	b.ReportAllocs()
	testQueryTypes(b, corpusTypeOnly, func(qt *queryTest) {
		id := qt.id

		for i := range 1000 {
			pn := id.NewPlannedPermanode(fmt.Sprint(i))
			id.SetAttribute(pn, "foo", fmt.Sprint(i))
		}

		req := &SearchQuery{
			Constraint: &Constraint{
				Permanode: &PermanodeConstraint{},
			},
		}
		if describe {
			req.Describe = &DescribeRequest{}
		}

		h := qt.Handler()
		b.ResetTimer()

		for b.Loop() {
			if describe {
				*req.Describe = DescribeRequest{}
			}
			_, err := h.Query(ctxbg, req)
			if err != nil {
				qt.t.Fatal(err)
			}
		}
	})
}

func BenchmarkQueryPermanodeLocation(b *testing.B) {
	b.ReportAllocs()
	testQueryTypes(b, corpusTypeOnly, func(qt *queryTest) {
		id := qt.id

		// Upload a basic image
		srcRoot, err := osutil.PkSourceRoot()
		if err != nil {
			panic(fmt.Sprintf("source root folder not found: %v", err))
		}
		uploadFile := func(file string, modTime time.Time) blob.Ref {
			fileName := filepath.Join(srcRoot, "pkg", "search", "testdata", file)
			contents, err := os.ReadFile(fileName)
			if err != nil {
				panic(err)
			}
			br, _ := id.UploadFile(file, string(contents), modTime)
			return br
		}
		fileRef := uploadFile("dude-gps.jpg", time.Time{})

		var n int
		newPn := func() blob.Ref {
			n++
			return id.NewPlannedPermanode(fmt.Sprint(n))
		}

		pn := id.NewPlannedPermanode("photo")
		id.SetAttribute(pn, "camliContent", fileRef.String())

		for i := range 5 {
			pn := newPn()
			id.SetAttribute(pn, "camliNodeType", "foursquare.com:venue")
			id.SetAttribute(pn, "latitude", fmt.Sprint(50-i))
			id.SetAttribute(pn, "longitude", fmt.Sprint(i))
			for range 5 {
				qn := newPn()
				id.SetAttribute(qn, "camliNodeType", "foursquare.com:checkin")
				id.SetAttribute(qn, "foursquareVenuePermanode", pn.String())
			}
		}
		for i := range 10 {
			pn := newPn()
			id.SetAttribute(pn, "foo", fmt.Sprint(i))
		}

		req := &SearchQuery{
			Constraint: &Constraint{
				Permanode: &PermanodeConstraint{
					Location: &LocationConstraint{Any: true},
				},
			},
		}

		h := qt.Handler()
		b.ResetTimer()

		for b.Loop() {
			_, err := h.Query(ctxbg, req)
			if err != nil {
				qt.t.Fatal(err)
			}
		}
	})
}

// Issue 1118: be efficient when looking up a direct blobref with a "ref:" query.
func TestRefQuerySource(t *testing.T) {
	testQueryTypes(t, memIndexTypes, func(qt *queryTest) {
		id := qt.id
		fileRef, _ := id.UploadFile("some-stuff.txt", "hello", time.Unix(123, 0))
		qt.t.Logf("fileRef = %q", fileRef)

		sq := &SearchQuery{
			Constraint: &Constraint{
				BlobRefPrefix: fileRef.String(), // exact match
			},
		}
		qt.candidateSource = "one_blob"
		qt.wantRes(sq, fileRef)
	})
}

func TestRefQuerySource_Logical(t *testing.T) {
	testQueryTypes(t, memIndexTypes, func(qt *queryTest) {
		id := qt.id
		fileRef, _ := id.UploadFile("some-stuff.txt", "hello", time.Unix(123, 0))
		qt.t.Logf("fileRef = %q", fileRef)

		sq := &SearchQuery{
			Constraint: &Constraint{
				Logical: &LogicalConstraint{
					Op: "and",
					A: &Constraint{
						BlobRefPrefix: fileRef.String()[:10],
					},
					B: &Constraint{
						BlobRefPrefix: fileRef.String(), // exact match
					},
				},
			},
		}
		qt.candidateSource = "one_blob"
		qt.wantRes(sq, fileRef)
	})
}

// permanode camliNodeType candidate source
func TestIsCheckinQuerySource(t *testing.T) {
	testQueryTypes(t, memIndexTypes, func(qt *queryTest) {
		id := qt.id
		pn := id.NewPlannedPermanode("photo")
		id.SetAttribute(pn, "camliNodeType", "foursquare.com:checkin")

		sq := &SearchQuery{
			Expression: "is:checkin",
			Sort:       MapSort,
		}
		qt.candidateSource = "corpus_permanode_types"
		qt.wantRes(sq, pn)
	})
}

// BenchmarkLocationPredicate aims at measuring the impact of
// https://camlistore-review.googlesource.com/8049
// ( + https://camlistore-review.googlesource.com/8649)
// on location queries.
// It populates the corpus with enough fake foursquare checkins/venues and
// twitter locations to look realistic.
func BenchmarkLocationPredicate(b *testing.B) {
	b.ReportAllocs()
	testQueryTypes(b, corpusTypeOnly, func(qt *queryTest) {
		id := qt.id

		var n int
		newPn := func() blob.Ref {
			n++
			return id.NewPlannedPermanode(fmt.Sprint(n))
		}

		// create (~700) venues all over the world, and mark 25% of them as places we've been to
		venueIdx := 0
		for long := -180.0; long < 180.0; long += 10.0 {
			for lat := -90.0; lat < 90.0; lat += 10.0 {
				pn := newPn()
				id.SetAttribute(pn, "camliNodeType", "foursquare.com:venue")
				id.SetAttribute(pn, "latitude", fmt.Sprintf("%f", lat))
				id.SetAttribute(pn, "longitude", fmt.Sprintf("%f", long))
				if venueIdx%4 == 0 {
					qn := newPn()
					id.SetAttribute(qn, "camliNodeType", "foursquare.com:checkin")
					id.SetAttribute(qn, "foursquareVenuePermanode", pn.String())
				}
				venueIdx++
			}
		}

		// create 3K tweets, all with locations
		lat := 45.18
		long := 5.72
		for range 3000 {
			pn := newPn()
			id.SetAttribute(pn, "camliNodeType", "twitter.com:tweet")
			id.SetAttribute(pn, "latitude", fmt.Sprintf("%f", lat))
			id.SetAttribute(pn, "longitude", fmt.Sprintf("%f", long))
			lat += 0.01
			long += 0.01
		}

		// create 5K additional permanodes, but no location. Just as "noise".
		for range 5000 {
			newPn()
		}

		// Create ~2600 photos all over the world.
		for long := -180.0; long < 180.0; long += 5.0 {
			for lat := -90.0; lat < 90.0; lat += 5.0 {
				br, _ := id.UploadFile("photo.jpg", exifFileContentLatLong(lat, long), time.Time{})
				pn := newPn()
				id.SetAttribute(pn, "camliContent", br.String())
			}
		}

		h := qt.Handler()
		b.ResetTimer()

		locations := []string{
			"canada", "scotland", "france", "sweden", "germany", "poland", "russia", "algeria", "congo", "china", "india", "australia", "mexico", "brazil", "argentina",
		}
		for b.Loop() {
			for _, loc := range locations {
				req := &SearchQuery{
					Expression: "loc:" + loc,
					Limit:      -1,
				}
				resp, err := h.Query(ctxbg, req)
				if err != nil {
					qt.t.Fatal(err)
				}
				b.Logf("found %d permanodes in %v", len(resp.Blobs), loc)
			}
		}

	})
}

var altLocCache = make(map[string][]geocode.Rect)

func init() {
	cacheGeo := func(address string, n, e, s, w float64) {
		altLocCache[address] = []geocode.Rect{{
			NorthEast: geocode.LatLong{Lat: n, Long: e},
			SouthWest: geocode.LatLong{Lat: s, Long: w},
		}}
	}

	cacheGeo("canada", 83.0956562, -52.6206965, 41.6765559, -141.00187)
	cacheGeo("scotland", 60.8607515, -0.7246751, 54.6332381, -8.6498706)
	cacheGeo("france", 51.0891285, 9.560067700000001, 41.3423275, -5.142307499999999)
	cacheGeo("sweden", 69.0599709, 24.1665922, 55.3367024, 10.9631865)
	cacheGeo("germany", 55.0581235, 15.0418962, 47.2701114, 5.8663425)
	cacheGeo("poland", 54.835784, 24.1458933, 49.0020251, 14.1228641)
	cacheGeo("russia", 81.858122, -169.0456324, 41.1853529, 19.6404268)
	cacheGeo("algeria", 37.0898204, 11.999999, 18.968147, -8.667611299999999)
	cacheGeo("congo", 3.707791, 18.6436109, -5.0289719, 11.1530037)
	cacheGeo("china", 53.5587015, 134.7728098, 18.1576156, 73.4994136)
	cacheGeo("india", 35.5087008, 97.395561, 6.7535159, 68.1623859)
	cacheGeo("australia", -9.2198214, 159.2557541, -54.7772185, 112.9215625)
	cacheGeo("mexico", 32.7187629, -86.7105711, 14.5345486, -118.3649292)
	cacheGeo("brazil", 5.2717863, -29.3448224, -33.7506241, -73.98281709999999)
	cacheGeo("argentina", -21.7810459, -53.6374811, -55.05727899999999, -73.56036019999999)
	cacheGeo("Uitdam", ExportUitdamLC.North, ExportUitdamLC.East, ExportUitdamLC.South, ExportUitdamLC.West)

	geocode.AltLookupFn = func(ctx context.Context, addr string) ([]geocode.Rect, error) {
		r, ok := altLocCache[addr]
		if ok {
			return r, nil
		}
		return nil, nil
	}
}

var exifFileContent struct {
	once sync.Once
	jpeg []byte
}

// exifFileContentLatLong returns the contents of a
// jpeg/exif file with the GPS coordinates lat and long.
func exifFileContentLatLong(lat, long float64) string {
	exifFileContent.once.Do(func() {
		var buf bytes.Buffer
		jpeg.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 128, 128)), nil)
		exifFileContent.jpeg = buf.Bytes()
	})

	x := rawExifLatLong(lat, long)
	j := exifFileContent.jpeg

	app1sec := []byte{0xff, 0xe1, 0, 0}
	binary.BigEndian.PutUint16(app1sec[2:], uint16(len(x)+2))

	p := make([]byte, 0, len(j)+len(app1sec)+len(x))
	p = append(p, j[:2]...)   // ff d8
	p = append(p, app1sec...) // exif section header
	p = append(p, x...)       // raw exif
	p = append(p, j[2:]...)   // jpeg image

	return string(p)
}

// rawExifLatLong creates raw exif for lat/long
// for storage in a jpeg file.
func rawExifLatLong(lat, long float64) []byte {

	x := exifBuf{
		bo: binary.BigEndian,
		p:  []byte("MM"),
	}

	x.putUint16(42) // magic

	ifd0ofs := x.reservePtr() // room for ifd0 offset
	x.storePtr(ifd0ofs)

	const (
		gpsSubIfdTag = 0x8825

		gpsLatitudeRef  = 1
		gpsLatitude     = 2
		gpsLongitudeRef = 3
		gpsLongitude    = 4

		typeAscii    = 2
		typeLong     = 4
		typeRational = 5
	)

	// IFD0
	x.storePtr(ifd0ofs)
	x.putUint16(1) // 1 tag

	x.putTag(gpsSubIfdTag, typeLong, 1)
	gpsofs := x.reservePtr()

	// IFD1
	x.putUint32(0) // no IFD1

	// GPS sub-IFD
	x.storePtr(gpsofs)
	x.putUint16(4) // 4 tags

	x.putTag(gpsLatitudeRef, typeAscii, 2)
	if lat >= 0 {
		x.next(4)[0] = 'N'
	} else {
		x.next(4)[0] = 'S'
	}

	x.putTag(gpsLatitude, typeRational, 3)
	latptr := x.reservePtr()

	x.putTag(gpsLongitudeRef, typeAscii, 2)
	if long >= 0 {
		x.next(4)[0] = 'E'
	} else {
		x.next(4)[0] = 'W'
	}

	x.putTag(gpsLongitude, typeRational, 3)
	longptr := x.reservePtr()

	// write data referenced in GPS sub-IFD
	x.storePtr(latptr)
	x.putDegMinSecRat(lat)

	x.storePtr(longptr)
	x.putDegMinSecRat(long)

	return append([]byte("Exif\x00\x00"), x.p...)
}

type exifBuf struct {
	bo binary.ByteOrder
	p  []byte
}

func (x *exifBuf) next(n int) []byte {
	l := len(x.p)
	x.p = append(x.p, make([]byte, n)...)
	return x.p[l:]
}

func (x *exifBuf) putTag(tag, typ uint16, len uint32) {
	x.putUint16(tag)
	x.putUint16(typ)
	x.putUint32(len)
}

func (x *exifBuf) putUint16(n uint16) { x.bo.PutUint16(x.next(2), n) }
func (x *exifBuf) putUint32(n uint32) { x.bo.PutUint32(x.next(4), n) }

func (x *exifBuf) putDegMinSecRat(v float64) {
	if v < 0 {
		v = -v
	}
	deg := uint32(v)
	v = 60 * (v - float64(deg))
	min := uint32(v)
	v = 60 * (v - float64(min))
	μsec := uint32(v * 1e6)

	x.putUint32(deg)
	x.putUint32(1)
	x.putUint32(min)
	x.putUint32(1)
	x.putUint32(μsec)
	x.putUint32(1e6)
}

// reservePtr reserves room for a ptr in x.
func (x *exifBuf) reservePtr() int {
	l := len(x.p)
	x.next(4)
	return l
}

// storePtr stores the current write offset at p
// that have been reserved with reservePtr.
func (x *exifBuf) storePtr(p int) {
	x.bo.PutUint32(x.p[p:], uint32(len(x.p)))
}

func TestBestByLocation(t *testing.T) {
	res := &SearchResult{
		LocationArea: &camtypes.LocationBounds{
			North: 90,
			South: -90,
			East:  180,
			West:  -180,
		},
	}
	locm := map[blob.Ref]camtypes.Location{}

	const numResults = 5000
	const limit = 117
	const scale = 1000
	for i := range numResults {
		br := blob.RefFromString(fmt.Sprintf("foo %d", i))
		res.Blobs = append(res.Blobs, &SearchResultBlob{Blob: br})
		locm[br] = camtypes.Location{
			Latitude:  float64(rand.Intn(360*scale) - 180*scale),
			Longitude: float64(rand.Intn(180*scale) - 90*scale),
		}
	}

	ExportBestByLocation(res, locm, limit)
	if got := len(res.Blobs); got != limit {
		t.Errorf("got %d blobs; want %d", got, limit)
	}
}
