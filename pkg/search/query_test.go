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
	t.Logf("Fileref: %s", fileRef)
	t.Logf("wholeRef: %s", wholeRef)

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
	dumpRes(t, sres)
	wantRes(t, sres, fileRef, wholeRef)
}

func TestQueryFile(t *testing.T) {
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

func TestQueryBlobSize(t *testing.T) {
	id, h := querySetup(t)

	_, smallFileRef := id.UploadFile("file.txt", strings.Repeat("x", 5<<10), time.Unix(1382073153, 0))
	id.UploadFile("file.txt", strings.Repeat("x", 20<<10), time.Unix(1382073153, 0))

	sq := &SearchQuery{
		Constraint: &Constraint{
			BlobSize: &BlobSizeConstraint{
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
