package search_test

import (
	"strings"
	"testing"
	"time"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/index"
	"camlistore.org/pkg/index/indextest"
	. "camlistore.org/pkg/search"
)

func querySetup(t *testing.T) (*indextest.IndexDeps, *Handler) {
	idx := index.NewMemoryIndex()
	id := indextest.NewIndexDeps(idx)
	id.Fataler = t
	h := NewHandler(idx, id.SignerBlobRef)
	return id, h
}

func dumpRes(t *testing.T, res *SearchResult) {
	t.Logf("Got: %#v", res)
	for i, got := range res.Blobs {
		t.Logf(" %d. %s", i, got)
	}
}

func wantRes(t *testing.T, res *SearchResult, wanted ...blob.Ref) {
	need := make(map[blob.Ref]bool)
	for _, br := range wanted {
		need[br] = true
	}
	for _, bi := range res.Blobs {
		if !need[bi.Blob] {
			t.Errorf("unexpected search result: %v", bi.Blob)
		} else {
			delete(need, bi.Blob)
		}
	}
	for br := range need {
		t.Errorf("missing from search result: %v", br)
	}
}

func TestQuery(t *testing.T) {
	id, h := querySetup(t)
	fileRef, wholeRef := id.UploadFile("file.txt", "the content", time.Unix(1382073153, 0))

	sq := &SearchQuery{
		Constraint: &Constraint{
			Anything: true,
		},
		Limit: 0,
		Sort:  UnspecifiedSort,
	}
	sres, err := h.Query(sq)
	if err != nil {
		t.Fatal(err)
	}
	wantRes(t, sres, fileRef, wholeRef)
}

func TestQueryCamliType(t *testing.T) {
	id, h := querySetup(t)

	fileRef, _ := id.UploadFile("file.txt", "foo", time.Unix(1382073153, 0))

	sq := &SearchQuery{
		Constraint: &Constraint{
			CamliType: "file",
		},
	}
	sres, err := h.Query(sq)
	if err != nil {
		t.Fatal(err)
	}
	wantRes(t, sres, fileRef)
}

func TestQueryAnyCamliType(t *testing.T) {
	id, h := querySetup(t)

	fileRef, _ := id.UploadFile("file.txt", "foo", time.Unix(1382073153, 0))

	sq := &SearchQuery{
		Constraint: &Constraint{
			AnyCamliType: true,
		},
	}
	sres, err := h.Query(sq)
	if err != nil {
		t.Fatal(err)
	}
	wantRes(t, sres, fileRef)
}

func TestQueryBlobSize(t *testing.T) {
	id, h := querySetup(t)

	_, smallFileRef := id.UploadFile("file.txt", strings.Repeat("x", 5<<10), time.Unix(1382073153, 0))
	id.UploadFile("file.txt", strings.Repeat("x", 20<<10), time.Unix(1382073153, 0))

	sq := &SearchQuery{
		Constraint: &Constraint{
			BlobSize: &IntConstraint{
				Min: 4 << 10,
				Max: 6 << 10,
			},
		},
	}
	sres, err := h.Query(sq)
	if err != nil {
		t.Fatal(err)
	}
	wantRes(t, sres, smallFileRef)
}

func TestQueryBlobRefPrefix(t *testing.T) {
	id, h := querySetup(t)

	// foo is 0beec7b5ea3f0fdbc95d0dd47f3c5bc275da8a33
	id.UploadFile("file.txt", "foo", time.Unix(1382073153, 0))
	// "bar.." is 08ef767ba2c93f8f40902118fa5260a65a2a4975
	id.UploadFile("file.txt", "bar..", time.Unix(1382073153, 0))

	sq := &SearchQuery{
		Constraint: &Constraint{
			BlobRefPrefix: "sha1-0",
		},
	}
	sres, err := h.Query(sq)
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
}

func TestQueryLogicalOr(t *testing.T) {
	id, h := querySetup(t)

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
	sres, err := h.Query(sq)
	if err != nil {
		t.Fatal(err)
	}
	wantRes(t, sres, foo, bar)
}

func TestQueryLogicalAnd(t *testing.T) {
	id, h := querySetup(t)

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
	sres, err := h.Query(sq)
	if err != nil {
		t.Fatal(err)
	}
	wantRes(t, sres, foo)
}

func TestQueryLogicalXor(t *testing.T) {
	id, h := querySetup(t)

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
	sres, err := h.Query(sq)
	if err != nil {
		t.Fatal(err)
	}
	wantRes(t, sres, foo)
}

func TestQueryLogicalNot(t *testing.T) {
	id, h := querySetup(t)

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
	sres, err := h.Query(sq)
	if err != nil {
		t.Fatal(err)
	}
	wantRes(t, sres, foo, bar)
}

func TestQueryPermanodeAttrExact(t *testing.T) {
	id, h := querySetup(t)

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
	sres, err := h.Query(sq)
	if err != nil {
		t.Fatal(err)
	}
	wantRes(t, sres, p1)
}

func TestQueryPermanodeAttrAny(t *testing.T) {
	id, h := querySetup(t)

	p1 := id.NewPlannedPermanode("1")
	p2 := id.NewPlannedPermanode("2")
	id.SetAttribute(p1, "someAttr", "value1")
	id.SetAttribute(p2, "someAttr", "value2")

	sq := &SearchQuery{
		Constraint: &Constraint{
			Permanode: &PermanodeConstraint{
				Attr:     "someAttr",
				ValueAny: []string{"value1", "value3"},
			},
		},
	}
	sres, err := h.Query(sq)
	if err != nil {
		t.Fatal(err)
	}
	wantRes(t, sres, p1)
}

func TestQueryPermanodeAttrSet(t *testing.T) {
	id, h := querySetup(t)

	p1 := id.NewPlannedPermanode("1")
	id.SetAttribute(p1, "x", "y")
	p2 := id.NewPlannedPermanode("2")
	id.SetAttribute(p2, "someAttr", "value2")

	sq := &SearchQuery{
		Constraint: &Constraint{
			Permanode: &PermanodeConstraint{
				Attr:     "someAttr",
				ValueSet: true,
			},
		},
	}
	sres, err := h.Query(sq)
	if err != nil {
		t.Fatal(err)
	}
	wantRes(t, sres, p2)
}

// find a permanode (p2) that has a property being a blobref pointing
// to a sub-query
func TestQueryPermanodeAttrValueMatches(t *testing.T) {
	id, h := querySetup(t)

	p1 := id.NewPlannedPermanode("1")
	id.SetAttribute(p1, "bar", "baz")
	p2 := id.NewPlannedPermanode("2")
	id.SetAttribute(p2, "foo", p1.String())

	sq := &SearchQuery{
		Constraint: &Constraint{
			Permanode: &PermanodeConstraint{
				Attr: "foo",
				ValueMatches: &Constraint{
					Permanode: &PermanodeConstraint{
						Attr:  "bar",
						Value: "baz",
					},
				},
			},
		},
	}
	sres, err := h.Query(sq)
	if err != nil {
		t.Fatal(err)
	}
	wantRes(t, sres, p2)
}

// find permanodes matching a certain file query
func TestQueryFileConstraint(t *testing.T) {
	id, h := querySetup(t)

	fileRef, _ := id.UploadFile("some-stuff.txt", "hello", time.Unix(123, 0))
	p1 := id.NewPlannedPermanode("1")
	id.SetAttribute(p1, "camliContent", fileRef.String())

	fileRef2, _ := id.UploadFile("other-file", "hellooooo", time.Unix(456, 0))
	p2 := id.NewPlannedPermanode("2")
	id.SetAttribute(p2, "camliContent", fileRef2.String())

	sq := &SearchQuery{
		Constraint: &Constraint{
			Permanode: &PermanodeConstraint{
				Attr: "camliContent",
				ValueMatches: &Constraint{
					File: &FileConstraint{
						FileName: &StringConstraint{
							Contains: "-stuff",
						},
						MaxSize: 5,
					},
				},
			},
		},
	}
	sres, err := h.Query(sq)
	if err != nil {
		t.Fatal(err)
	}
	wantRes(t, sres, p1)
}

func TestQueryPermanodeModtime(t *testing.T) {
	id, h := querySetup(t)

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
					After:  time.Unix(1322443957, 456789),
					Before: time.Unix(1322443959, 0),
				},
			},
		},
	}
	sres, err := h.Query(sq)
	if err != nil {
		t.Fatal(err)
	}
	wantRes(t, sres, p2)
}
