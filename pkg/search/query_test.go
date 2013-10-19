package search_test

import (
	"strings"
	"testing"
	"time"

	"camlistore.org/pkg/index"
	"camlistore.org/pkg/index/indextest"
	. "camlistore.org/pkg/search"
)

func TestQuery(t *testing.T) {
	idx := index.NewMemoryIndex()
	id := indextest.NewIndexDeps(idx)
	id.Fataler = t

	fileRef, wholeRef := id.UploadFile("file.txt", strings.Repeat("x", 1<<20), time.Unix(1382073153, 0))
	t.Logf("Fileref: %s", fileRef)
	t.Logf("wholeRef: %s", wholeRef)

	h := NewHandler(idx, id.SignerBlobRef)
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
	t.Logf("Got: %#v", sres)
	for i, got := range sres.Blobs {
		t.Logf(" %d. %s", i, got)
	}
}
