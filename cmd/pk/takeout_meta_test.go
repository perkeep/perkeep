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
	"archive/zip"
	"context"
	"fmt"
	"maps"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	search "perkeep.org/internal/searchtypes"
	"perkeep.org/internal/takeout"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/schema/nodeattr"
)

func TestPhotoAttrChanges(t *testing.T) {
	m := &takeout.PhotoMetadata{
		Title:       "IMG_9688.HEIC",
		Description: "Space Needle",
		PhotoTaken:  time.Date(2018, 7, 14, 0, 48, 46, 0, time.UTC),
		HasLocation: true,
		Latitude:    47.62825,
		Longitude:   -122.34935,
		JSON:        []byte(`{"title": "IMG_9688.HEIC"}`),
	}
	tests := []struct {
		name string
		cur  url.Values
		want []attrChange
	}{
		{
			name: "empty",
			cur:  url.Values{},
			want: []attrChange{
				{"set", "title", "IMG_9688.HEIC"},
				{"set", "description", "Space Needle"},
				{"set", "dateCreated", "2018-07-14T00:48:46Z"},
				{"set", "latitude", "47.62825"},
				{"set", "longitude", "-122.34935"},
				{"set", "googlePhotosMetadata", `{"title": "IMG_9688.HEIC"}`},
			},
		},
		{
			name: "already_set",
			cur: url.Values{
				"title":                {"IMG_9688.HEIC"},
				"description":          {"Space Needle"},
				"dateCreated":          {"2018-07-14T00:48:46.000Z"}, // the same time
				"latitude":             {"47.628250"},
				"longitude":            {"-122.349350"},
				"googlePhotosMetadata": {`{"title": "IMG_9688.HEIC"}`},
			},
		},
		{
			name: "others_values",
			cur: url.Values{
				"title":       {"Needle"},
				"dateCreated": {"2018-07-13T17:48:46-07:00"}, // the same time
				"latitude":    {"47.6"},
				"longitude":   {"-122.3"},
			},
			want: []attrChange{
				{"set", "googlePhotosTitle", "IMG_9688.HEIC"},
				{"set", "description", "Space Needle"},
				{"set", "googlePhotosLatitude", "47.62825"},
				{"set", "googlePhotosLongitude", "-122.34935"},
				{"set", "googlePhotosMetadata", `{"title": "IMG_9688.HEIC"}`},
			},
		},
		{
			name: "google_values_set",
			cur: url.Values{
				"title":                 {"Needle"},
				"googlePhotosTitle":     {"IMG_9688.HEIC"},
				"description":           {"Space Needle"},
				"dateCreated":           {"2018-07-14T00:48:46Z"},
				"latitude":              {"47.6"},
				"longitude":             {"-122.3"},
				"googlePhotosLatitude":  {"47.62825"},
				"googlePhotosLongitude": {"-122.34935"},
				"googlePhotosMetadata":  {`{"title": "IMG_9688.HEIC"}`},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := photoAttrChanges(tt.cur, m); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got  %q\nwant %q", got, tt.want)
			}
		})
	}
}

func TestSetChanges(t *testing.T) {
	cur := url.Values{
		"title":       {"Trip"},
		"description": {"old", "older"},
		"camliMember": {"a", "b", "x"},
	}
	attrs := []attrChange{{"set", "title", "Trip"}, {"set", "description", "new"}}
	got := setChanges(cur, attrs, []string{"a", "b", "c"})
	want := []attrChange{
		{"set", "description", "new"},
		{"del", "camliMember", "x"},
		{"add", "camliMember", "c"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got  %q\nwant %q", got, want)
	}
}

// writeTakeoutZip writes a zip file of files, name to contents, in dir.
func writeTakeoutZip(t *testing.T, dir, name string, files map[string]string) {
	t.Helper()
	f, err := os.Create(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for _, name := range slices.Sorted(maps.Keys(files)) {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate, Modified: time.Unix(1700000000, 0)})
		if err != nil {
			t.Fatal(err)
		}
		w.Write([]byte(files[name]))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	f.Close()
}

func testSidecar(title, desc string) string {
	return fmt.Sprintf(`{"title": %q, "description": %q, "photoTakenTime": {"timestamp": "1531529326"}, "modificationTime": {"timestamp": "1554366885"}, "geoData": {"latitude": 47.62825, "longitude": -122.34935}}`, title, desc)
}

// syncTakeoutDir uploads the Google Photos files of the one export in
// dir, and syncs their metadata, returning the sync.
func syncTakeoutDir(t *testing.T, st *takeoutStore, dir string) *takeoutMetaSync {
	t.Helper()
	ctx := context.Background()
	exps, err := takeout.LocalExports(dir)
	if err != nil || len(exps) != 1 {
		t.Fatalf("LocalExports = %d exports, %v", len(exps), err)
	}
	exp := exps[0]
	if err := exp.LoadTOCs(ctx, 2); err != nil {
		t.Fatal(err)
	}
	var ents []takeout.Entry
	for _, e := range exp.Entries() {
		if takeout.IsPhotosMedia(e.File.Name) && !takeout.IsEdited(e.File.Name) {
			ents = append(ents, e)
		}
	}
	cs := takeout.Contents(ents)
	_, missing, err := takeout.Check(ctx, st, cs, 2, nil)
	if err != nil {
		t.Fatal(err)
	}
	if failed, err := takeout.Upload(ctx, st, missing, 2, nil); err != nil || failed != 0 {
		t.Fatalf("Upload: %d failed, %v", failed, err)
	}
	lib, err := takeout.NewLibrary(ctx, exp, cs, 2)
	if err != nil {
		t.Fatal(err)
	}
	ms := &takeoutMetaSync{st: st, exp: exp, lib: lib, workers: 2}
	if err := ms.run(ctx); err != nil {
		t.Fatal(err)
	}
	t.Logf("%s: %s", exp.Name, ms.report())
	return ms
}

// permanodeAttrs returns the attributes of permanode pn.
func permanodeAttrs(t *testing.T, st *takeoutStore, pn blob.Ref) url.Values {
	t.Helper()
	res, err := st.c.Describe(context.Background(), &search.DescribeRequest{BlobRef: pn})
	if err != nil {
		t.Fatal(err)
	}
	db := res.Meta.Get(pn)
	if db == nil || db.Permanode == nil {
		t.Fatalf("permanode %v not described", pn)
	}
	return db.Permanode.Attr
}

// photoPermanode returns the permanode whose camliContent is the file
// named name.
func photoPermanode(t *testing.T, st *takeoutStore, name string) blob.Ref {
	t.Helper()
	res, err := st.c.Query(context.Background(), &search.SearchQuery{
		Constraint: &search.Constraint{Permanode: &search.PermanodeConstraint{
			Attr:       nodeattr.CamliContent,
			ValueInSet: &search.Constraint{File: &search.FileConstraint{FileName: &search.StringConstraint{Equals: name}}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Blobs) != 1 {
		t.Fatalf("%d permanodes of %s; want 1", len(res.Blobs), name)
	}
	return res.Blobs[0].Blob
}

func TestTakeoutMetaSync(t *testing.T) {
	st := newTakeoutTestStore(t)
	ctx := context.Background()
	photoA := strings.Repeat("photo A ", 1000)
	photoB := strings.Repeat("photo B ", 1000)
	photoC := strings.Repeat("photo C ", 1000)

	dir1 := t.TempDir()
	writeTakeoutZip(t, dir1, "takeout-20240101T000000Z-001.zip", map[string]string{
		"Takeout/Google Photos/2018-07-13/A.JPG":      photoA,
		"Takeout/Google Photos/2018-07-13/A.JPG.json": testSidecar("A.JPG", "Space Needle"),
		"Takeout/Google Photos/2018-07-13/B.JPG":      photoB,
		"Takeout/Google Photos/2018-07-13/B.JPG.json": testSidecar("B.JPG", ""),
		"Takeout/Google Photos/Trip/A.JPG":            photoA,
		"Takeout/Google Photos/Trip/A.JPG.json":       testSidecar("A.JPG", "Space Needle"),
		"Takeout/Google Photos/Trip/B.JPG":            photoB,
		"Takeout/Google Photos/Trip/B.JPG.json":       testSidecar("B.JPG", ""),
		"Takeout/Google Photos/Trip/metadata.json":    `{"albumData": {"title": "Trip", "description": "Seattle", "date": {"timestamp": "1449213423"}}}`,
	})
	syncTakeoutDir(t, st, dir1)

	pnA, pnB := photoPermanode(t, st, "A.JPG"), photoPermanode(t, st, "B.JPG")
	a := permanodeAttrs(t, st, pnA)
	for attr, want := range map[string]string{
		"title":       "A.JPG",
		"description": "Space Needle",
		"dateCreated": "2018-07-14T00:48:46Z",
		"latitude":    "47.62825",
		"longitude":   "-122.34935",
	} {
		if got := a.Get(attr); got != want {
			t.Errorf("A's %s = %q; want %q", attr, got, want)
		}
	}
	if !strings.Contains(a.Get("googlePhotosMetadata"), "Space Needle") {
		t.Errorf("A's googlePhotosMetadata = %q", a.Get("googlePhotosMetadata"))
	}

	album, err := st.plannedPermanodeRef(ctx, albumPermanodeKeyPrefix+"Trip")
	if err != nil {
		t.Fatal(err)
	}
	checkMembers := func(what string, pn blob.Ref, want ...blob.Ref) url.Values {
		t.Helper()
		attrs := permanodeAttrs(t, st, pn)
		got := slices.Sorted(slices.Values(attrs["camliMember"]))
		var wantStrs []string
		for _, br := range want {
			wantStrs = append(wantStrs, br.String())
		}
		slices.Sort(wantStrs)
		if !slices.Equal(got, wantStrs) {
			t.Errorf("%s members = %q; want %q", what, got, wantStrs)
		}
		return attrs
	}
	attrs := checkMembers("album", album, pnA, pnB)
	if attrs.Get("camliNodeType") != nodeTypeGooglePhotosAlbum || attrs.Get("title") != "Trip" || attrs.Get("description") != "Seattle" ||
		attrs.Get("takeoutExport") != "takeout-20240101T000000Z" {
		t.Errorf("album attrs = %v", attrs)
	}
	root, err := st.plannedPermanodeRef(ctx, albumsRootPermanodeKey)
	if err != nil {
		t.Fatal(err)
	}
	checkMembers("root", root, album)

	// Something else retitles B. In a newer export, the album has C,
	// but not B.
	if err := st.applyChanges(ctx, pnB, []attrChange{{"set", "title", "Bee"}}, time.Now(), false); err != nil {
		t.Fatal(err)
	}
	dir2 := t.TempDir()
	writeTakeoutZip(t, dir2, "takeout-20250101T000000Z-001.zip", map[string]string{
		"Takeout/Google Photos/2018-07-13/A.JPG":      photoA,
		"Takeout/Google Photos/2018-07-13/A.JPG.json": testSidecar("A.JPG", "Space Needle"),
		"Takeout/Google Photos/2018-07-13/B.JPG":      photoB,
		"Takeout/Google Photos/2018-07-13/B.JPG.json": testSidecar("B.JPG", ""),
		"Takeout/Google Photos/2018-07-14/C.JPG":      photoC,
		"Takeout/Google Photos/2018-07-14/C.JPG.json": testSidecar("C.JPG", ""),
		"Takeout/Google Photos/Trip/A.JPG":            photoA,
		"Takeout/Google Photos/Trip/A.JPG.json":       testSidecar("A.JPG", "Space Needle"),
		"Takeout/Google Photos/Trip/C.JPG":            photoC,
		"Takeout/Google Photos/Trip/C.JPG.json":       testSidecar("C.JPG", ""),
		"Takeout/Google Photos/Trip/metadata.json":    `{"albumData": {"title": "Trip", "description": "Seattle", "date": {"timestamp": "1449213423"}}}`,
	})
	ms := syncTakeoutDir(t, st, dir2)
	pnC := photoPermanode(t, st, "C.JPG")
	checkMembers("album after the newer export", album, pnA, pnC)
	if ms.stats.photosChanged != 2 { // C's new attributes, and B's Google title
		t.Errorf("newer export changed %d photos; want 2", ms.stats.photosChanged)
	}
	if b := permanodeAttrs(t, st, pnB); b.Get("title") != "Bee" || b.Get("googlePhotosTitle") != "B.JPG" {
		t.Errorf("B's title = %q, googlePhotosTitle = %q; want Bee, as set after the first export, and B.JPG", b.Get("title"), b.Get("googlePhotosTitle"))
	}

	// The older export again doesn't undo the newer one, and finds
	// nothing to change in the photos.
	ms = syncTakeoutDir(t, st, dir1)
	checkMembers("album after the older export again", album, pnA, pnC)
	if ms.stats.albumsSkipped != 2 { // the album and the root
		t.Errorf("older export skipped %d sets; want 2", ms.stats.albumsSkipped)
	}
	if ms.stats.photosChanged != 0 {
		t.Errorf("older export changed %d photos; want 0", ms.stats.photosChanged)
	}
}
