/*
Copyright 2026 The Perkeep Authors

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
	"testing"
	"time"

	"perkeep.org/internal/livejournal"
	"perkeep.org/pkg/blob"
)

// TestLiveJournalSync syncs a journal to a server, with a real index,
// twice: the second time changes nothing. Then, after an entry and a
// comment are deleted, it keeps what LiveJournal no longer has.
func TestLiveJournalSync(t *testing.T) {
	st := newTakeoutTestStore(t)
	ctx := context.Background()
	store := ljClientStore{st.c, st.signer}
	j := &livejournal.Journal{
		User: "brad",
		Entries: []*livejournal.Entry{
			{ItemID: 1, Anum: 11, EventTime: "2000-12-31 23:15:00", Subject: "New year", Event: "text\n\nmore",
				Security: "public", URL: "https://brad.livejournal.com/267.html",
				Props: map[string]string{"taglist": "a, b"}},
			{ItemID: 2, Anum: 3, EventTime: "2001-01-02 10:00:00", Event: "regrettable",
				Security: "public", URL: "https://brad.livejournal.com/515.html"},
		},
		Comments: []*livejournal.Comment{
			{ID: 5, JItemID: 1, PosterID: 7, Poster: "friend", State: "A", Subject: "Re", Body: "hi", Date: "2001-01-01T00:00:00Z"},
			{ID: 6, JItemID: 1, ParentID: 5, State: "D"},
		},
	}
	opts := livejournal.SyncOptions{Location: time.UTC, Workers: 2}
	res, err := livejournal.Sync(ctx, store, j, opts)
	if err != nil {
		t.Fatal(err)
	}
	if res.Permanodes != 5 || res.Created != 5 || res.Changed != 5 {
		t.Errorf("first sync: %+v; want 5 permanodes, all made and changed", res)
	}

	journal := permanodeAttrs(t, st, res.Journal)
	if journal.Get("title") != "LiveJournal - brad" || len(journal["camliMember"]) != 2 {
		t.Fatalf("journal attrs = %v", journal)
	}
	entryRef, ok := blob.Parse(journal.Get("camliMember"))
	if !ok {
		t.Fatalf("journal member %q isn't a blobref", journal.Get("camliMember"))
	}
	entry := permanodeAttrs(t, st, entryRef)
	if entry.Get("content") != "text\n\nmore" || entry.Get("startDate") != "2000-12-31T23:15:00Z" || len(entry["tag"]) != 2 {
		t.Errorf("entry attrs = %v", entry)
	}

	res, err = livejournal.Sync(ctx, store, j, opts)
	if err != nil {
		t.Fatal(err)
	}
	if res.Created != 0 || res.Changed != 0 {
		t.Errorf("second sync: %+v; want nothing changed", res)
	}

	// Entry 2 is deleted, and so is comment 5, which LiveJournal then
	// has no text, date, or poster of.
	j.Entries = j.Entries[:1]
	j.Comments[0] = &livejournal.Comment{ID: 5, JItemID: 1, State: "D"}
	res, err = livejournal.Sync(ctx, store, j, opts)
	if err != nil {
		t.Fatal(err)
	}
	if res.Changed != 1 || res.AttrChanges != 1 {
		t.Errorf("sync after deletions: %+v; want only the comment's state changed", res)
	}
	if got := permanodeAttrs(t, st, res.Journal)["camliMember"]; len(got) != 2 {
		t.Errorf("journal members after deleting an entry = %q; want both still", got)
	}
	commentRef, err := st.plannedPermanodeRef(ctx, "livejournal:brad:comment:5")
	if err != nil {
		t.Fatal(err)
	}
	comment := permanodeAttrs(t, st, commentRef)
	for attr, want := range map[string]string{
		"livejournalState":  "D",
		"title":             "Re",
		"content":           "hi",
		"startDate":         "2001-01-01T00:00:00Z",
		"livejournalPoster": "friend",
	} {
		if got := comment.Get(attr); got != want {
			t.Errorf("deleted comment's %s = %q; want %q", attr, got, want)
		}
	}
	if comment.Get("livejournalDateEstimated") != "" {
		t.Errorf("deleted comment's date became estimated: %v", comment)
	}
}
