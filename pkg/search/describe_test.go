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
	"io/ioutil"
	"path/filepath"
	"testing"
	"time"

	"perkeep.org/internal/osutil"
	"perkeep.org/internal/testhooks"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/index"
	"perkeep.org/pkg/index/indextest"
	"perkeep.org/pkg/schema"
	"perkeep.org/pkg/search"
	"perkeep.org/pkg/test"
)

func init() {
	testhooks.SetUseSHA1(true)
}

var describedBlobs = map[string]blob.Ref{
	"abc-123":            blob.MustParse("sha1-39606e3c1730123c1cd80857b41a721ea5e6d4c5"),
	"abc-123c":           blob.MustParse("sha1-d2ec7f86e73d2434df7736bcab47e9cc1507faeb"),
	"abc-123c1":          blob.MustParse("sha1-6caf362b90f7713558860ef2b43c1bfa1af4d54b"),
	"abc-123cc":          blob.MustParse("sha1-4542245a5739f14ef5df3e3578603cb5599e979d"),
	"abc-888":            blob.MustParse("sha1-7a37eadfe010128b452cb950932c408149a0b6aa"),
	"abc-8881":           blob.MustParse("sha1-0b0fec9002df13dad544e303b932ab7900fe651f"),
	"fourcheckin-0":      blob.MustParse("sha1-5efbaa0911510dbfd9a3e527de821c9c4aaa1451"),
	"fourvenue-123":      blob.MustParse("sha1-2bc38525b7f5cb33657079f176b81dfc688a761a"),
	"venuepicset-123":    blob.MustParse("sha1-14b788811dcbf39aba11d06a7f4c3a85e158372e"),
	"venuepic-1":         blob.MustParse("sha1-322e19e5e2ff273b0e726817180d4e7a65fd18a6"),
	"somevenuepic-0":     blob.MustParse("sha1-6293628cff5169c577fa2c27b191827e5537dc2d"),
	"venuepic-2":         blob.MustParse("sha1-931e6fa5904f8ddb3dab7d6107f4d4acd5dc2c0c"),
	"somevenuepic-2":     blob.MustParse("sha1-eaa4c47801c00ea55bcca508cd9ac6aef224f8e4"),
	"homedir-0":          blob.MustParse("sha1-9c8c65b7cdd94fce2162492740a596c44ca87869"),
	"homedir-1":          blob.MustParse("sha1-5b906a7c5ff14c219fe2a58f97b8cf73814273c4"),
	"homedir-2":          blob.MustParse("sha1-d5f182577613cd4a0b75e23d461c6ea93c637e85"),
	"set-0":              blob.MustParse("sha1-db75da7350c909cc24640eff5fcd4613a235d57a"),
	"location-0":         blob.MustParse("sha1-94618fac5f1257bf0ac52fb07e391295ddb89e3a"),
	"locationpriority-1": blob.MustParse("sha1-d8d3f7e4a74a7fb29435c6d708f683bf330863a6"),
	"locationpriority-2": blob.MustParse("sha1-1841d4ee4c6edab302b1f13c78a6d3ee1a09fb38"),
	"locationoverride-1": blob.MustParse("sha1-6f245d8bd5b18d110d305f33b64cb1be190d43ff"),
	"locationoverride-2": blob.MustParse("sha1-51271c4962df329d4c32d5c56eccbe996de668b4"),
	"filewithloc-0":      blob.MustParse("sha1-24c572c7cf48de8c32298b9ac00c7cb7b9922d60"),
}

func searchDescribeSetup(t *testing.T) indexAndOwner {
	idx := index.NewMemoryIndex()
	tf := new(test.Fetcher)
	idx.InitBlobSource(tf)
	idx.KeyFetcher = tf
	fi := &fetcherIndex{
		tf:  tf,
		idx: idx,
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
		camliRootPath, err := osutil.GoPackagePath("perkeep.org")
		if err != nil {
			t.Fatalf("looking up perkeep.org location in $GOPATH: %v", err)
		}
		fileName := filepath.Join(camliRootPath, "pkg", "search", "testdata", file)
		contents, err := ioutil.ReadFile(fileName)
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
		index: idx,
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
