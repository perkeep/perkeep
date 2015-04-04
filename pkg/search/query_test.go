package search_test

import (
	"encoding/json"
	"flag"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/index"
	"camlistore.org/pkg/index/indextest"
	. "camlistore.org/pkg/search"
	"camlistore.org/pkg/test"
	"camlistore.org/pkg/types"
)

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
	t     testing.TB
	id    *indextest.IndexDeps
	itype indexType

	Handler func() *Handler
}

func querySetup(t testing.TB) (*indextest.IndexDeps, *Handler) {
	idx := index.NewMemoryIndex() // string key-value pairs in memory, as if they were on disk
	id := indextest.NewIndexDeps(idx)
	id.Fataler = t
	h := NewHandler(idx, id.SignerBlobRef)
	return id, h
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
	qt.Handler = func() *Handler {
		h := NewHandler(idx, qt.id.SignerBlobRef)
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

func dumpRes(t *testing.T, res *SearchResult) {
	t.Logf("Got: %#v", res)
	for i, got := range res.Blobs {
		t.Logf(" %d. %s", i, got)
	}
}

func (qt *queryTest) wantRes(req *SearchQuery, wanted ...blob.Ref) {
	if qt.itype == indexClassic {
		req.Sort = Unsorted
	}
	res, err := qt.Handler().Query(req)
	if err != nil {
		qt.t.Fatal(err)
	}

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

		// foo is 0beec7b5ea3f0fdbc95d0dd47f3c5bc275da8a33
		id.UploadFile("file.txt", "foo", time.Unix(1382073153, 0))
		// "bar.." is 08ef767ba2c93f8f40902118fa5260a65a2a4975
		id.UploadFile("file.txt", "bar..", time.Unix(1382073153, 0))

		sq := &SearchQuery{
			Constraint: &Constraint{
				BlobRefPrefix: "sha1-0",
			},
		}
		sres, err := qt.Handler().Query(sq)
		if err != nil {
			t.Fatal(err)
		}
		if len(sres.Blobs) < 2 {
			t.Errorf("expected at least 2 matches; got %d", len(sres.Blobs))
		}
		for _, res := range sres.Blobs {
			brStr := res.Blob.String()
			if !strings.HasPrefix(brStr, "sha1-0") {
				t.Errorf("matched blob %s didn't begin with sha1-0", brStr)
			}
		}
	})
}

func TestQueryTwoConstraints(t *testing.T) {
	testQuery(t, func(qt *queryTest) {
		id := qt.id
		id.UploadString("a")      // 86f7e437faa5a7fce15d1ddcb9eaeaea377667b8
		b := id.UploadString("b") // e9d71f5ee7c92d6dc9e92ffdad17b8bd49418f98
		id.UploadString("c4")     // e4666a670f042877c67a84473a71675ee0950a08

		sq := &SearchQuery{
			Constraint: &Constraint{
				BlobRefPrefix: "sha1-e", // matches b and c4
				BlobSize: &IntConstraint{ // matches a and b
					Min: 1,
					Max: 1,
				},
			},
		}
		qt.wantRes(sq, b)
	})
}

func TestQueryLogicalOr(t *testing.T) {
	testQuery(t, func(qt *queryTest) {
		id := qt.id

		// foo is 0beec7b5ea3f0fdbc95d0dd47f3c5bc275da8a33
		_, foo := id.UploadFile("file.txt", "foo", time.Unix(1382073153, 0))
		// "bar.." is 08ef767ba2c93f8f40902118fa5260a65a2a4975
		_, bar := id.UploadFile("file.txt", "bar..", time.Unix(1382073153, 0))

		sq := &SearchQuery{
			Constraint: &Constraint{
				Logical: &LogicalConstraint{
					Op: "or",
					A: &Constraint{
						BlobRefPrefix: "sha1-0beec7b5ea3f0fdbc95d0dd",
					},
					B: &Constraint{
						BlobRefPrefix: "sha1-08ef767ba2c93f8f40",
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

		// foo is 0beec7b5ea3f0fdbc95d0dd47f3c5bc275da8a33
		_, foo := id.UploadFile("file.txt", "foo", time.Unix(1382073153, 0))
		// "bar.." is 08ef767ba2c93f8f40902118fa5260a65a2a4975
		id.UploadFile("file.txt", "bar..", time.Unix(1382073153, 0))

		sq := &SearchQuery{
			Constraint: &Constraint{
				Logical: &LogicalConstraint{
					Op: "and",
					A: &Constraint{
						BlobRefPrefix: "sha1-0",
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

		// foo is 0beec7b5ea3f0fdbc95d0dd47f3c5bc275da8a33
		_, foo := id.UploadFile("file.txt", "foo", time.Unix(1382073153, 0))
		// "bar.." is 08ef767ba2c93f8f40902118fa5260a65a2a4975
		id.UploadFile("file.txt", "bar..", time.Unix(1382073153, 0))

		sq := &SearchQuery{
			Constraint: &Constraint{
				Logical: &LogicalConstraint{
					Op: "xor",
					A: &Constraint{
						BlobRefPrefix: "sha1-0",
					},
					B: &Constraint{
						BlobRefPrefix: "sha1-08ef767ba2c93f8f40",
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

		// foo is 0beec7b5ea3f0fdbc95d0dd47f3c5bc275da8a33
		_, foo := id.UploadFile("file.txt", "foo", time.Unix(1382073153, 0))
		// "bar.." is 08ef767ba2c93f8f40902118fa5260a65a2a4975
		_, bar := id.UploadFile("file.txt", "bar..", time.Unix(1382073153, 0))

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
							WholeRef: blob.SHA1FromString("hello"),
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
	testQuery(t, func(qt *queryTest) {
		id := qt.id
		fileRef, wholeRef := id.UploadFile("file.gif", "GIF87afoo", time.Unix(456, 0))
		res, err := qt.Handler().Describe(&DescribeRequest{
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
		res, err := handler.Query(req)
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
			if got, want := res.Continue, "pn:1322443958000123456:sha1-fbb5be10fcb4c88d32cfdddb20a7b8d13e9ba284"; got != want {
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
			res, err := handler.Query(req)
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
			[]blob.Ref{blobs[3], blobs[2]},
			[]blob.Ref{blobs[1], blobs[0]},
			[]blob.Ref{},
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
			res, err := handler.Query(req)
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
			[]blob.Ref{blobs[2], blobs[1]},
			[]blob.Ref{blobs[0]},
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
			res, err := handler.Query(req)
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
			_, err := h.Query(req)
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

func prettyJSON(v interface{}) string {
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

		for i := 0; i < b.N; i++ {
			*req.Describe = DescribeRequest{}
			_, err := h.Query(req)
			if err != nil {
				qt.t.Fatal(err)
			}
		}
	})
}
