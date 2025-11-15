/*
Copyright 2014 The Perkeep Authors

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
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"perkeep.org/internal/osutil"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/index"
	"perkeep.org/pkg/index/indextest"
	"perkeep.org/pkg/schema"
	"perkeep.org/pkg/search"
	"perkeep.org/pkg/test"
)

var describedBlobs = map[string]blob.Ref{
	"abc-123":            blob.MustParse("sha224-79038fc0d2e810fbb5dc690bc968d723238054b400b236849a12f715"),
	"abc-123c":           blob.MustParse("sha224-02ac4ea5801f067f06cc81a4f563b8858efffdc16095b281e697d58c"),
	"abc-123c1":          blob.MustParse("sha224-fb9935d85877a9bc171bab2c8323a779a68ab6e5be3f682d62996f35"),
	"abc-123cc":          blob.MustParse("sha224-00dbf97acb90a281ba0384aed708bd878f2e7f3d03ced900e567316d"),
	"abc-888":            blob.MustParse("sha224-a56191dc9fc29e381d38355bac8bc521650544e211f966707698eb3f"),
	"abc-8881":           blob.MustParse("sha224-7f8e9759c99bf52ea7b0d38a67170e7054ec09fcdd31919320ddcd3d"),
	"fourcheckin-0":      blob.MustParse("sha224-df25f3b243f2f5d96481804c1d1984f8c224d35be8bedc557e72bcc5"),
	"fourvenue-123":      blob.MustParse("sha224-2d92cc32c84496f368cadea7e47f678672433daa565811a469a9223d"),
	"venuepicset-123":    blob.MustParse("sha224-c788aec1b0deca7362cc7149a53ef5ff6dfd9ed1e80ba07ab58b06e6"),
	"venuepic-1":         blob.MustParse("sha224-8ad366b377b98c6dcabab3d4e42fde987123bc3e0e40dec7be4a83df"),
	"somevenuepic-0":     blob.MustParse("sha224-a9d921d8379ac0c15673d9e186c6621a5946ac4c22ecb5c167e5a3f1"),
	"venuepic-2":         blob.MustParse("sha224-fb6218e8a4a418e433c3110f75be36f8e1f58ed60e052c48646443c7"),
	"somevenuepic-2":     blob.MustParse("sha224-74d722cca8a4fd475299119183d7379157c1f973ff326e1fa37298dc"),
	"homedir-0":          blob.MustParse("sha224-c7a4437350dab36c2ae8b042c2bf99d8caa57d045d9ad4b9a487377d"),
	"homedir-1":          blob.MustParse("sha224-bde3c6f57bbc3fed5979eb9d3d56573a814985ccc6f8e4c669a1d8d4"),
	"homedir-2":          blob.MustParse("sha224-4aa170e43fb854631d63417ce31fa088f602668345b4011ef50d516e"),
	"set-0":              blob.MustParse("sha224-84d98395687a89cb973cb62cef77549ff133f19dd497e4ef277bcd6b"),
	"location-0":         blob.MustParse("sha224-ce0aa94f630097f2406abf9e4b54f28b65761bdaf791c5c621287227"),
	"locationpriority-1": blob.MustParse("sha224-b29ef5825b42f27c9383cf929d5340aed35da07a4577fa75d837378c"),
	"locationpriority-2": blob.MustParse("sha224-5f41f325a975fb2c402c3dcadfb96a119f020be074a6f58d2bdd2c77"),
	"locationoverride-1": blob.MustParse("sha224-c52bd08e3a80f73cb859a005bc9f4c3b9eae6a651b726e32cdf18d42"),
	"locationoverride-2": blob.MustParse("sha224-77f3c44baec0d0fc245b56bfaaa2f3be0910634b214de9b1b17d12e5"),
	"filewithloc-0":      blob.MustParse("sha224-7fa40667d233910c4fa7a6bd199aab43f330f8f1b9701de15459b2cb"),
}

func searchDescribeSetup(t *testing.T) indexAndOwner {
	id := indextest.NewIndexDeps(index.NewMemoryIndex())
	defer id.DumpIndex(t)

	fi := &fetcherIndex{
		tf:  id.BlobSource,
		idx: id.Index,
	}

	lastModtime = test.ClockOrigin
	checkErr(t, fi.addBlob(ownerRef))
	checkErr(t, fi.addPermanode("abc-123",
		"camliContent", dbRefStr("abc-123c"),
		"camliImageContent", dbRefStr("abc-888"),
	))

	checkErr(t, fi.addPermanode("abc-123c",
		"camliContent", dbRefStr("abc-123cc"),
		"camliImageContent", dbRefStr("abc-123c1"),
	))

	checkErr(t, fi.addPermanode("abc-123c1",
		"some", "image",
	))

	checkErr(t, fi.addPermanode("abc-123cc",
		"name", "leaf",
	))

	checkErr(t, fi.addPermanode("abc-888",
		"camliContent", dbRefStr("abc-8881"),
	))

	checkErr(t, fi.addPermanode("abc-8881",
		"name", "leaf8881",
	))

	checkErr(t, fi.addPermanode("fourcheckin-0",
		"camliNodeType", "foursquare.com:checkin",
		"foursquareVenuePermanode", dbRefStr("fourvenue-123"),
	))
	checkErr(t, fi.addPermanode("fourvenue-123",
		"camliNodeType", "foursquare.com:venue",
		"camliPath:photos", dbRefStr("venuepicset-123"),
		"latitude", "12",
		"longitude", "34",
	))

	checkErr(t, fi.addPermanode("venuepicset-123",
		"camliPath:1.jpg", dbRefStr("venuepic-1"),
	))

	checkErr(t, fi.addPermanode("venuepic-1",
		"camliContent", dbRefStr("somevenuepic-0"),
	))

	checkErr(t, fi.addPermanode("somevenuepic-0",
		"foo", "bar",
	))

	checkErr(t, fi.addPermanode("venuepic-2",
		"camliContent", dbRefStr("somevenuepic-2"),
	))

	checkErr(t, fi.addPermanode("somevenuepic-2",
		"foo", "baz",
	))

	checkErr(t, fi.addPermanode("homedir-0",
		"camliPath:subdir.1", dbRefStr("homedir-1"),
	))

	checkErr(t, fi.addPermanode("homedir-1",
		"camliPath:subdir.2", dbRefStr("homedir-2"),
	))

	checkErr(t, fi.addPermanode("homedir-2",
		"foo", "bar",
	))

	checkErr(t, fi.addPermanode("set-0",
		"camliMember", dbRefStr("venuepic-1"),
		"camliMember", dbRefStr("venuepic-2"),
	))

	uploadFile := func(file string) blob.Ref {
		srcRoot, err := osutil.PkSourceRoot()
		if err != nil {
			t.Fatalf("source root folder not found: %v", err)
		}
		fileName := filepath.Join(srcRoot, "pkg", "search", "testdata", file)
		contents, err := os.ReadFile(fileName)
		if err != nil {
			t.Fatal(err)
		}
		tb := &test.Blob{Contents: string(contents)}
		checkErr(t, fi.addBlob(tb))
		m := schema.NewFileMap(fileName)
		m.PopulateParts(int64(len(contents)), []schema.BytesPart{
			{
				Size:    uint64(len(contents)),
				BlobRef: tb.BlobRef(),
			}})
		lastModtime = lastModtime.Add(time.Second).UTC()
		m.SetModTime(lastModtime)
		fjson, err := m.JSON()
		if err != nil {
			t.Fatal(err)
		}
		fb := &test.Blob{Contents: fjson}
		checkErr(t, fi.addBlob(fb))
		return fb.BlobRef()
	}
	uploadFile("dude-gps.jpg")

	checkErr(t, fi.addPermanode("location-0",
		"camliContent", dbRefStr("filewithloc-0"),
	))

	checkErr(t, fi.addPermanode("locationpriority-1",
		"latitude", "67",
		"longitude", "78",
		"camliNodeType", "foursquare.com:checkin",
		"foursquareVenuePermanode", dbRefStr("fourvenue-123"),
		"camliContent", dbRefStr("filewithloc-0"),
	))

	checkErr(t, fi.addPermanode("locationpriority-2",
		"camliNodeType", "foursquare.com:checkin",
		"foursquareVenuePermanode", dbRefStr("fourvenue-123"),
		"camliContent", dbRefStr("filewithloc-0"),
	))

	checkErr(t, fi.addPermanode("locationoverride-1",
		"latitude", "67",
		"longitude", "78",
		"camliContent", dbRefStr("filewithloc-0"),
	))

	checkErr(t, fi.addPermanode("locationoverride-2",
		"latitude", "67",
		"longitude", "78",
		"camliNodeType", "foursquare.com:checkin",
		"foursquareVenuePermanode", dbRefStr("fourvenue-123"),
	))

	return indexAndOwner{
		index: id.Index,
		owner: owner.BlobRef(),
	}
}

func dbRefStr(name string) string {
	ref, ok := describedBlobs[name]
	if !ok {
		panic(name + " not found")
	}
	return ref.String()
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
		name:  "single",
		setup: searchDescribeSetup,
		postBody: marshalJSON(&search.DescribeRequest{
			BlobRef: describedBlobs["abc-123"],
		}),
		wantDescribed: []string{dbRefStr("abc-123")},
	},

	{
		name:  "follow all camliContent",
		setup: searchDescribeSetup,
		postBody: marshalJSON(&search.DescribeRequest{
			BlobRef: describedBlobs["abc-123"],
			Rules: []*search.DescribeRule{
				{
					Attrs: []string{"camliContent"},
				},
			},
		}),
		wantDescribed: []string{dbRefStr("abc-123"), dbRefStr("abc-123c"), dbRefStr("abc-123cc")},
	},

	{
		name: "follow only root camliContent",
		postBody: marshalJSON(&search.DescribeRequest{
			BlobRef: describedBlobs["abc-123"],
			Rules: []*search.DescribeRule{
				{
					IfResultRoot: true,
					Attrs:        []string{"camliContent"},
				},
			},
		}),
		wantDescribed: []string{dbRefStr("abc-123"), dbRefStr("abc-123c")},
	},

	{
		name: "follow all root, substring",
		postBody: marshalJSON(&search.DescribeRequest{
			BlobRef: describedBlobs["abc-123"],
			Rules: []*search.DescribeRule{
				{
					IfResultRoot: true,
					Attrs:        []string{"camli*"},
				},
			},
		}),
		wantDescribed: []string{dbRefStr("abc-123"), dbRefStr("abc-123c"), dbRefStr("abc-888")},
	},

	{
		name: "two rules, two attrs",
		postBody: marshalJSON(&search.DescribeRequest{
			BlobRef: describedBlobs["abc-123"],
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
		wantDescribed: []string{dbRefStr("abc-123"), dbRefStr("abc-123c"), dbRefStr("abc-123cc"), dbRefStr("abc-888"), dbRefStr("abc-8881")},
	},

	{
		name: "foursquare venue photos, but not recursive camliPath explosion",
		postBody: marshalJSON(&search.DescribeRequest{
			BlobRefs: []blob.Ref{
				describedBlobs["homedir-0"],
				describedBlobs["fourcheckin-0"],
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
		wantDescribed: []string{dbRefStr("homedir-0"), dbRefStr("fourcheckin-0"), dbRefStr("fourvenue-123"), dbRefStr("venuepicset-123"), dbRefStr("venuepic-1"), dbRefStr("somevenuepic-0")},
	},

	{
		name: "home dirs forever",
		postBody: marshalJSON(&search.DescribeRequest{
			BlobRefs: []blob.Ref{
				describedBlobs["homedir-0"],
			},
			Rules: []*search.DescribeRule{
				{
					Attrs: []string{"camliPath:*"},
				},
			},
		}),
		wantDescribed: []string{dbRefStr("homedir-0"), dbRefStr("homedir-1"), dbRefStr("homedir-2")},
	},

	{
		name: "find members",
		postBody: marshalJSON(&search.DescribeRequest{
			BlobRef: describedBlobs["set-0"],
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
		wantDescribed: []string{dbRefStr("set-0"), dbRefStr("venuepic-1"), dbRefStr("venuepic-2"), dbRefStr("somevenuepic-0"), dbRefStr("somevenuepic-2")},
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
	h := search.NewHandler(idx, owner)
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
		for i := range headstart {
			br := blobrefs[i]
			res, err := h.Describe(ctx, &search.DescribeRequest{
				BlobRef: br,
				Depth:   1,
			})
			if err != nil {
				t.Error(err)
				return
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
		ref       blob.Ref
		lat, long float64
		hasNoLoc  bool
	}{
		{ref: describedBlobs["filewithloc-0"], lat: 42.45, long: 18.76},
		{ref: describedBlobs["location-0"], lat: 42.45, long: 18.76},
		{ref: describedBlobs["locationpriority-1"], lat: 67, long: 78},
		{ref: describedBlobs["locationpriority-2"], lat: 12, long: 34},
		{ref: describedBlobs["locationoverride-1"], lat: 67, long: 78},
		{ref: describedBlobs["locationoverride-2"], lat: 67, long: 78},
		{ref: describedBlobs["homedir-0"], hasNoLoc: true},
	}

	ixo := searchDescribeSetup(t)
	ix := ixo.index
	ctx := context.Background()
	h := search.NewHandler(ix, owner)

	ix.RLock()
	defer ix.RUnlock()

	for _, tt := range tests {
		var err error
		br := tt.ref
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

// To make sure we don't regress into https://github.com/perkeep/perkeep/issues/1152
func TestDescribeEmptyDir(t *testing.T) {
	ix := index.NewMemoryIndex()
	ctx := context.Background()
	h := search.NewHandler(ix, owner)
	corpus, err := ix.KeepInMemory()
	if err != nil {
		t.Fatal(err)
	}
	h.SetCorpus(corpus)
	id := indextest.NewIndexDeps(ix)

	dir := id.UploadDir("empty", nil, time.Now().UTC())
	pn := id.NewPlannedPermanode("empty_dir")
	id.SetAttribute(pn, "camliContent", dir.String())

	ix.RLock()
	defer ix.RUnlock()

	if _, err := h.Describe(ctx, &search.DescribeRequest{
		BlobRef: pn,
		Rules: []*search.DescribeRule{
			{
				Attrs: []string{"camliContent"},
			},
		},
	}); err != nil {
		t.Fatalf("Describe for %v failed: %v", pn, err)
	}
}
