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

package livejournal

import (
	"net/url"
	"reflect"
	"testing"
	"time"

	"perkeep.org/internal/attrclaims"
	"perkeep.org/pkg/schema/nodeattr"
)

// testJournal returns a journal with two entries and three comments: a
// visible one, a reply that's been deleted (with text recovered from a
// dump, but no date), and one of no known entry.
func testJournal() *Journal {
	return &Journal{
		User: "brad",
		Entries: []*Entry{
			{ItemID: 1, Anum: 11, EventTime: "2000-12-31 23:15:00", Subject: "New year", Event: "text",
				Security: "public", URL: "https://brad.livejournal.com/267.html",
				SyncTime: time.Date(2001, 1, 2, 3, 4, 5, 0, time.UTC),
				Props:    map[string]string{"taglist": "a, b", "current_mood": "happy"}},
			{ItemID: 2, Anum: 3, EventTime: "2001-06-01 08:00:00", Event: "locked", Security: "usemask", AllowMask: "1",
				URL: "https://brad.livejournal.com/515.html", Props: map[string]string{}},
		},
		Comments: []*Comment{
			{ID: 5, JItemID: 1, PosterID: 7, Poster: "friend", State: "A", Body: "hi", Date: "2001-01-01T00:00:00Z"},
			{ID: 6, JItemID: 1, ParentID: 5, State: "D"},
			{ID: 9, State: "D"},
		},
	}
}

func TestNodes(t *testing.T) {
	pacific, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Fatal(err)
	}
	nodes, st := testJournal().nodes(pacific)
	if st.commentsSkipped != 1 {
		t.Errorf("commentsSkipped = %d; want 1", st.commentsSkipped)
	}
	want := []*node{
		{key: "livejournal:brad:entry:1", attrs: url.Values{
			nodeattr.Type:                   {NodeTypeEntry},
			AttrJournal:                     {"brad"},
			AttrItemID:                      {"1"},
			nodeattr.Title:                  {"New year"},
			nodeattr.Content:                {"text"},
			nodeattr.URL:                    {"https://brad.livejournal.com/267.html"},
			AttrEventTime:                   {"2000-12-31 23:15:00"},
			nodeattr.StartDate:              {"2001-01-01T07:15:00Z"},
			nodeattr.DateModified:           {"2001-01-02T03:04:05Z"},
			AttrSecurity:                    {"public"},
			attrTag:                         {"a", "b"},
			AttrPropPrefix + "current_mood": {"happy"},
			AttrPropPrefix + "taglist":      {"a, b"},
		}},
		{key: "livejournal:brad:entry:2", attrs: url.Values{
			nodeattr.Type:      {NodeTypeEntry},
			AttrJournal:        {"brad"},
			AttrItemID:         {"2"},
			nodeattr.Content:   {"locked"},
			nodeattr.URL:       {"https://brad.livejournal.com/515.html"},
			AttrEventTime:      {"2001-06-01 08:00:00"},
			nodeattr.StartDate: {"2001-06-01T15:00:00Z"},
			AttrSecurity:       {"usemask"},
			AttrAllowMask:      {"1"},
		}},
		{key: "livejournal:brad:comment:5", attrs: url.Values{
			nodeattr.Type:      {NodeTypeComment},
			AttrJournal:        {"brad"},
			AttrCommentID:      {"5"},
			AttrState:          {"A"},
			nodeattr.Content:   {"hi"},
			nodeattr.StartDate: {"2001-01-01T00:00:00Z"},
			AttrPosterID:       {"7"},
			AttrPoster:         {"friend"},
			nodeattr.URL:       {"https://brad.livejournal.com/267.html?thread=1291#t1291"},
			AttrEntry:          {"livejournal:brad:entry:1"},
		}},
		{key: "livejournal:brad:comment:6", attrs: url.Values{
			nodeattr.Type: {NodeTypeComment},
			AttrJournal:   {"brad"},
			AttrCommentID: {"6"},
			AttrState:     {"D"},
			// Undated, so it's dated as the comment before it.
			nodeattr.StartDate: {"2001-01-01T00:00:00Z"},
			AttrDateEstimated:  {"true"},
			nodeattr.URL:       {"https://brad.livejournal.com/267.html?thread=1547#t1547"},
			AttrEntry:          {"livejournal:brad:entry:1"},
			AttrParent:         {"livejournal:brad:comment:5"},
		}},
		{key: "livejournal:brad", attrs: url.Values{
			nodeattr.Type:        {NodeTypeJournal},
			AttrJournal:          {"brad"},
			nodeattr.Title:       {"LiveJournal - brad"},
			attrTag:              {"livejournal"},
			nodeattr.CamliMember: {"livejournal:brad:entry:1", "livejournal:brad:entry:2"},
		}},
	}
	if len(nodes) != len(want) {
		t.Fatalf("got %d nodes; want %d", len(nodes), len(want))
	}
	for i, n := range nodes {
		if n.key != want[i].key || !reflect.DeepEqual(n.attrs, want[i].attrs) {
			t.Errorf("node %d:\n got %s %v\nwant %s %v", i, n.key, n.attrs, want[i].key, want[i].attrs)
		}
	}
}

func TestAttrChanges(t *testing.T) {
	cur := url.Values{
		nodeattr.Title:       {"old"},
		attrTag:              {"a"},
		AttrPropPrefix + "x": {"gone"},
		nodeattr.CamliMember: {"m1", "m2"},
		"userAttr":           {"kept"},
	}
	want := url.Values{
		nodeattr.Title:       {"new"},
		attrTag:              {"a", "b"},
		nodeattr.CamliMember: {"m2", "m3"},
		nodeattr.Content:     {"text"},
	}
	got := attrChanges(cur, want)
	wantChanges := []attrclaims.Change{
		// Members are only added: m1 stays.
		{Op: "add", Attr: nodeattr.CamliMember, Value: "m3"},
		{Op: "set", Attr: nodeattr.Content, Value: "text"},
		{Op: "set", Attr: attrTag, Value: "a"},
		{Op: "add", Attr: attrTag, Value: "b"},
		{Op: "set", Attr: nodeattr.Title, Value: "new"},
		{Op: "del", Attr: AttrPropPrefix + "x"},
	}
	if !reflect.DeepEqual(got, wantChanges) {
		t.Errorf("changes:\n got %v\nwant %v", got, wantChanges)
	}
	if c := attrChanges(want, want); len(c) != 0 {
		t.Errorf("no-op changes = %v", c)
	}
}

func TestKeepLost(t *testing.T) {
	// A comment as imported when it still had its text, and its
	// attributes now that it's deleted, without text, date, or poster.
	cur := url.Values{
		nodeattr.Type:          {NodeTypeComment},
		AttrState:              {"A"},
		nodeattr.Title:         {"Re: hi"},
		nodeattr.Content:       {"what I said"},
		nodeattr.StartDate:     {"2001-01-01T00:00:00Z"},
		AttrPoster:             {"friend"},
		AttrPosterID:           {"7"},
		AttrRecovered:          {"sql-dump state A"},
		AttrPropPrefix + "pic": {"sm01"},
	}
	want := url.Values{
		nodeattr.Type:      {NodeTypeComment},
		AttrState:          {"D"},
		nodeattr.StartDate: {"2000-12-31T00:00:00Z"},
		AttrDateEstimated:  {"true"},
	}
	keepLost(cur, want)
	got := attrChanges(cur, want)
	wantChanges := []attrclaims.Change{{Op: "set", Attr: AttrState, Value: "D"}}
	if !reflect.DeepEqual(got, wantChanges) {
		t.Errorf("changes:\n got %v\nwant %v", got, wantChanges)
	}

	// One never dated keeps its estimate, and can still get one.
	cur = url.Values{AttrState: {"D"}}
	want = url.Values{AttrState: {"D"}, nodeattr.StartDate: {"2000-12-31T00:00:00Z"}, AttrDateEstimated: {"true"}}
	keepLost(cur, want)
	if got := attrChanges(cur, want); len(got) != 2 {
		t.Errorf("undated comment's changes = %v; want its estimated date set", got)
	}
}
