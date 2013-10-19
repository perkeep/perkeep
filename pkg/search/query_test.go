package search_test

import (
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
