/*
Copyright 2014 The Camlistore Authors

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
	"fmt"
	"testing"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/index"
	"camlistore.org/pkg/index/indextest"
	"camlistore.org/pkg/search"
	"camlistore.org/pkg/test"
	"camlistore.org/pkg/types/camtypes"

	"golang.org/x/net/context"
)

func addPermanode(fi *test.FakeIndex, pnStr string, attrs ...string) {
	pn := blob.MustParse(pnStr)
	fi.AddMeta(pn, "permanode", 123)
	for len(attrs) > 0 {
		k, v := attrs[0], attrs[1]
		attrs = attrs[2:]
		fi.AddClaim(owner, pn, "add-attribute", k, v)
	}
}

func addFileWithLocation(fi *test.FakeIndex, fileStr string, lat, long float64) {
	fileRef := blob.MustParse(fileStr)
	fi.AddFileLocation(fileRef, camtypes.Location{Latitude: lat, Longitude: long})
	fi.AddMeta(fileRef, "file", 123)
}

func searchDescribeSetup(fi *test.FakeIndex) index.Interface {
	addPermanode(fi, "abc-123",
		"camliContent", "abc-123c",
		"camliImageContent", "abc-888",
	)
	addPermanode(fi, "abc-123c",
		"camliContent", "abc-123cc",
		"camliImageContent", "abc-123c1",
	)
	addPermanode(fi, "abc-123c1",
		"some", "image",
	)
	addPermanode(fi, "abc-123cc",
		"name", "leaf",
	)
	addPermanode(fi, "abc-888",
		"camliContent", "abc-8881",
	)
	addPermanode(fi, "abc-8881",
		"name", "leaf8881",
	)

	addPermanode(fi, "fourcheckin-0",
		"camliNodeType", "foursquare.com:checkin",
		"foursquareVenuePermanode", "fourvenue-123",
	)
	addPermanode(fi, "fourvenue-123",
		"camliNodeType", "foursquare.com:venue",
		"camliPath:photos", "venuepicset-123",
		"latitude", "12",
		"longitude", "34",
	)
	addPermanode(fi, "venuepicset-123",
		"camliPath:1.jpg", "venuepic-1",
	)
	addPermanode(fi, "venuepic-1",
		"camliContent", "somevenuepic-0",
	)
	addPermanode(fi, "somevenuepic-0",
		"foo", "bar",
	)
	addPermanode(fi, "venuepic-2",
		"camliContent", "somevenuepic-2",
	)
	addPermanode(fi, "somevenuepic-2",
		"foo", "baz",
	)

	addPermanode(fi, "homedir-0",
		"camliPath:subdir.1", "homedir-1",
	)
	addPermanode(fi, "homedir-1",
		"camliPath:subdir.2", "homedir-2",
	)
	addPermanode(fi, "homedir-2",
		"foo", "bar",
	)

	addPermanode(fi, "set-0",
		"camliMember", "venuepic-1",
		"camliMember", "venuepic-2",
	)

	addFileWithLocation(fi, "filewithloc-0", 45, 56)
	addPermanode(fi, "location-0",
		"camliContent", "filewithloc-0",
	)

	addPermanode(fi, "locationpriority-1",
		"latitude", "67",
		"longitude", "78",
		"camliNodeType", "foursquare.com:checkin",
		"foursquareVenuePermanode", "fourvenue-123",
		"camliContent", "filewithloc-0",
	)

	addPermanode(fi, "locationpriority-2",
		"camliNodeType", "foursquare.com:checkin",
		"foursquareVenuePermanode", "fourvenue-123",
		"camliContent", "filewithloc-0",
	)

	addPermanode(fi, "locationoverride-1",
		"latitude", "67",
		"longitude", "78",
		"camliContent", "filewithloc-0",
	)

	addPermanode(fi, "locationoverride-2",
		"latitude", "67",
		"longitude", "78",
		"camliNodeType", "foursquare.com:checkin",
		"foursquareVenuePermanode", "fourvenue-123",
	)

	return fi
}

var searchDescribeTests = []handlerTest{
	{
		name:     "null",
		postBody: marshalJSON(&search.DescribeRequest{}),
		want: jmap(&search.DescribeResponse{
			Meta: search.MetaMap{},
		}),
	},

	{
		name: "single",
		postBody: marshalJSON(&search.DescribeRequest{
			BlobRef: blob.MustParse("abc-123"),
		}),
		wantDescribed: []string{"abc-123"},
	},

	{
		name: "follow all camliContent",
		postBody: marshalJSON(&search.DescribeRequest{
			BlobRef: blob.MustParse("abc-123"),
			Rules: []*search.DescribeRule{
				{
					Attrs: []string{"camliContent"},
				},
			},
		}),
		wantDescribed: []string{"abc-123", "abc-123c", "abc-123cc"},
	},

	{
		name: "follow only root camliContent",
		postBody: marshalJSON(&search.DescribeRequest{
			BlobRef: blob.MustParse("abc-123"),
			Rules: []*search.DescribeRule{
				{
					IfResultRoot: true,
					Attrs:        []string{"camliContent"},
				},
			},
		}),
		wantDescribed: []string{"abc-123", "abc-123c"},
	},

	{
		name: "follow all root, substring",
		postBody: marshalJSON(&search.DescribeRequest{
			BlobRef: blob.MustParse("abc-123"),
			Rules: []*search.DescribeRule{
				{
					IfResultRoot: true,
					Attrs:        []string{"camli*"},
				},
			},
		}),
		wantDescribed: []string{"abc-123", "abc-123c", "abc-888"},
	},

	{
		name: "two rules, two attrs",
		postBody: marshalJSON(&search.DescribeRequest{
			BlobRef: blob.MustParse("abc-123"),
			Rules: []*search.DescribeRule{
				{
					IfResultRoot: true,
					Attrs:        []string{"camliContent", "camliImageContent"},
				},
				{
					Attrs: []string{"camliContent"},
				},
			},
		}),
		wantDescribed: []string{"abc-123", "abc-123c", "abc-123cc", "abc-888", "abc-8881"},
	},

	{
		name: "foursquare venue photos, but not recursive camliPath explosion",
		postBody: marshalJSON(&search.DescribeRequest{
			BlobRefs: []blob.Ref{
				blob.MustParse("homedir-0"),
				blob.MustParse("fourcheckin-0"),
			},
			Rules: []*search.DescribeRule{
				{
					Attrs: []string{"camliContent", "camliContentImage"},
				},
				{
					IfCamliNodeType: "foursquare.com:checkin",
					Attrs:           []string{"foursquareVenuePermanode"},
				},
				{
					IfCamliNodeType: "foursquare.com:venue",
					Attrs:           []string{"camliPath:photos"},
					Rules: []*search.DescribeRule{
						{
							Attrs: []string{"camliPath:*"},
						},
					},
				},
			},
		}),
		wantDescribed: []string{"homedir-0", "fourcheckin-0", "fourvenue-123", "venuepicset-123", "venuepic-1", "somevenuepic-0"},
	},

	{
		name: "home dirs forever",
		postBody: marshalJSON(&search.DescribeRequest{
			BlobRefs: []blob.Ref{
				blob.MustParse("homedir-0"),
			},
			Rules: []*search.DescribeRule{
				{
					Attrs: []string{"camliPath:*"},
				},
			},
		}),
		wantDescribed: []string{"homedir-0", "homedir-1", "homedir-2"},
	},

	{
		name: "find members",
		postBody: marshalJSON(&search.DescribeRequest{
			BlobRef: blob.MustParse("set-0"),
			Rules: []*search.DescribeRule{
				{
					IfResultRoot: true,
					Attrs:        []string{"camliMember"},
					Rules: []*search.DescribeRule{
						{Attrs: []string{"camliContent"}},
					},
				},
			},
		}),
		wantDescribed: []string{"set-0", "venuepic-1", "venuepic-2", "somevenuepic-0", "somevenuepic-2"},
	},
}

func init() {
	checkNoDups("searchDescribeTests", searchDescribeTests)
}

func TestSearchDescribe(t *testing.T) {
	for _, ht := range searchDescribeTests {
		if ht.setup == nil {
			ht.setup = searchDescribeSetup
		}
		if ht.query == "" {
			ht.query = "describe"
		}
		ht.test(t)
	}
}

// should be run with -race
func TestDescribeRace(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in short mode")
	}
	idx := index.NewMemoryIndex()
	idxd := indextest.NewIndexDeps(idx)
	idxd.Fataler = t
	corpus, err := idxd.Index.KeepInMemory()
	if err != nil {
		t.Fatalf("error slurping index to memory: %v", err)
	}
	h := search.NewHandler(idx, idxd.SignerBlobRef)
	h.SetCorpus(corpus)
	donec := make(chan struct{})
	headstart := 500
	blobrefs := make([]blob.Ref, headstart)
	headstartc := make(chan struct{})
	go func() {
		for i := 0; i < headstart*2; i++ {
			nth := fmt.Sprintf("%d", i)
			// No need to lock the index here. It is already done within NewPlannedPermanode,
			// because it calls idxd.Index.ReceiveBlob.
			pn := idxd.NewPlannedPermanode(nth)
			idxd.SetAttribute(pn, "tag", nth)
			if i > headstart {
				continue
			}
			if i == headstart {
				headstartc <- struct{}{}
				continue
			}
			blobrefs[i] = pn
		}
	}()
	<-headstartc
	ctx := context.Background()
	go func() {
		for i := 0; i < headstart; i++ {
			br := blobrefs[i]
			res, err := h.Describe(ctx, &search.DescribeRequest{
				BlobRef: br,
				Depth:   1,
			})
			if err != nil {
				t.Fatal(err)
			}
			_, ok := res.Meta[br.String()]
			if !ok {
				t.Errorf("permanode %v wasn't in Describe response", br)
			}
		}
		donec <- struct{}{}
	}()
	<-donec
}

func TestDescribeLocation(t *testing.T) {
	tests := []struct {
		ref       string
		lat, long float64
		hasNoLoc  bool
	}{
		{ref: "filewithloc-0", lat: 45, long: 56},
		{ref: "location-0", lat: 45, long: 56},
		{ref: "locationpriority-1", lat: 67, long: 78},
		{ref: "locationpriority-2", lat: 12, long: 34},
		{ref: "locationoverride-1", lat: 67, long: 78},
		{ref: "locationoverride-2", lat: 67, long: 78},
		{ref: "homedir-0", hasNoLoc: true},
	}

	ix := searchDescribeSetup(test.NewFakeIndex())
	ctx := context.Background()
	h := search.NewHandler(ix, owner)

	ix.RLock()
	defer ix.RUnlock()

	for _, tt := range tests {
		var err error
		br := blob.MustParse(tt.ref)
		res, err := h.Describe(ctx, &search.DescribeRequest{
			BlobRef: br,
			Depth:   1,
		})
		if err != nil {
			t.Errorf("Describe for %v failed: %v", br, err)
			continue
		}
		db := res.Meta[br.String()]
		if db == nil {
			t.Errorf("Describe result for %v is missing", br)
			continue
		}
		loc := db.Location
		if tt.hasNoLoc {
			if loc != nil {
				t.Errorf("got location for %v, should have no location", br)
			}
		} else {
			if loc == nil {
				t.Errorf("no location in result for %v", br)
				continue
			}
			if loc.Latitude != tt.lat || loc.Longitude != tt.long {
				t.Errorf("location for %v invalid, got %f,%f want %f,%f",
					tt.ref, loc.Latitude, loc.Longitude, tt.lat, tt.long)
			}
		}
	}
}

// To make sure we don't regress into issue 881: i.e. a permanode with no attr
// should not lead us to call index.claimsIntfAttrValue with a nil claims argument.
func TestDescribePermNoAttr(t *testing.T) {
	ix := index.NewMemoryIndex()
	ctx := context.Background()
	h := search.NewHandler(ix, owner)
	corpus, err := ix.KeepInMemory()
	if err != nil {
		t.Fatal(err)
	}
	h.SetCorpus(corpus)
	id := indextest.NewIndexDeps(ix)
	br := id.NewPlannedPermanode("noattr-0")

	ix.RLock()
	defer ix.RUnlock()

	res, err := h.Describe(ctx, &search.DescribeRequest{
		BlobRef: br,
		Depth:   1,
	})
	if err != nil {
		t.Fatalf("Describe for %v failed: %v", br, err)
	}
	db := res.Meta[br.String()]
	if db == nil {
		t.Fatalf("Describe result for %v is missing", br)
	}
}
