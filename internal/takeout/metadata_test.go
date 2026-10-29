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

package takeout

import (
	"context"
	"maps"
	"strings"
	"testing"
	"time"
)

// A sidecar as a 2019 export has it.
const testSidecar = `{
  "title": "IMG_9688.HEIC",
  "description": "  Space Needle  ",
  "url": "https://lh3.googleusercontent.com/-ACp/IMG_9688.HEIC",
  "imageViews": "0",
  "creationTime": {"timestamp": "1531597157", "formatted": "Jul 14, 2018, 7:39:17 PM UTC"},
  "modificationTime": {"timestamp": "1554366885", "formatted": "Apr 4, 2019, 8:34:45 AM UTC"},
  "geoData": {"latitude": 0.0, "longitude": 0.0, "altitude": 0.0, "latitudeSpan": 0.0, "longitudeSpan": 0.0},
  "geoDataExif": {"latitude": 47.62825, "longitude": -122.34935, "altitude": 67.75, "latitudeSpan": 0.0, "longitudeSpan": 0.0},
  "photoTakenTime": {"timestamp": "1531529326", "formatted": "Jul 14, 2018, 12:48:46 AM UTC"},
  "people": [{"name": "Brad"}, {"name": ""}]
}`

func TestParsePhotoMetadata(t *testing.T) {
	m, err := ParsePhotoMetadata([]byte(testSidecar))
	if err != nil {
		t.Fatal(err)
	}
	if m.Title != "IMG_9688.HEIC" || m.Description != "Space Needle" || !strings.HasSuffix(m.URL, "IMG_9688.HEIC") {
		t.Errorf("title %q, description %q, url %q", m.Title, m.Description, m.URL)
	}
	for _, tt := range []struct {
		name      string
		got, want time.Time
	}{
		{"PhotoTaken", m.PhotoTaken, time.Date(2018, 7, 14, 0, 48, 46, 0, time.UTC)},
		{"Created", m.Created, time.Date(2018, 7, 14, 19, 39, 17, 0, time.UTC)},
		{"Modified", m.Modified, time.Date(2019, 4, 4, 8, 34, 45, 0, time.UTC)},
	} {
		if !tt.got.Equal(tt.want) {
			t.Errorf("%s = %v; want %v", tt.name, tt.got, tt.want)
		}
	}
	// geoData is zero, so the location is geoDataExif's.
	if !m.HasLocation || m.Latitude != 47.62825 || m.Longitude != -122.34935 || m.Altitude != 67.75 {
		t.Errorf("location %v: %v, %v, %v", m.HasLocation, m.Latitude, m.Longitude, m.Altitude)
	}
	if len(m.People) != 1 || m.People[0] != "Brad" {
		t.Errorf("people = %q; want [Brad]", m.People)
	}
	if string(m.JSON) != testSidecar {
		t.Error("JSON isn't the sidecar")
	}
}

func TestParsePhotoMetadataNaN(t *testing.T) {
	m, err := ParsePhotoMetadata([]byte(`{"title": "a.jpg", "geoData": {"latitude": 16.36, "longitude": -86.43, "altitude": "NaN"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if !m.HasLocation || m.Latitude != 16.36 || m.Longitude != -86.43 || m.Altitude != 0 {
		t.Errorf("location %v: %v, %v, %v; want 16.36, -86.43, and no altitude", m.HasLocation, m.Latitude, m.Longitude, m.Altitude)
	}
	m, err = ParsePhotoMetadata([]byte(`{"title": "a.jpg", "geoData": {"latitude": "NaN", "longitude": "NaN"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if m.HasLocation {
		t.Errorf("NaN location is a location: %v, %v", m.Latitude, m.Longitude)
	}
}

func TestParseAlbumMetadata(t *testing.T) {
	m, err := ParseAlbumMetadata([]byte(`{"albumData": {"title": "Trip to Seattle", "description": "", "access": "protected", "location": "Seattle",
		"date": {"timestamp": "1449213423"}, "geoData": {"latitude": 47.6, "longitude": -122.3}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if m.Title != "Trip to Seattle" || m.Access != "protected" || m.Location != "Seattle" || !m.Date.Equal(time.Unix(1449213423, 0)) || !m.HasLocation {
		t.Errorf("got %+v", m)
	}
	// Newer exports.
	m, err = ParseAlbumMetadata([]byte(`{"title": "Chicago", "description": "Winter", "access": "", "date": {"timestamp": "1296684064"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if m.Title != "Chicago" || m.Description != "Winter" || !m.Date.Equal(time.Unix(1296684064, 0)) || m.HasLocation {
		t.Errorf("got %+v", m)
	}
}

func TestIsAlbumDir(t *testing.T) {
	for dir, want := range map[string]bool{
		"Trip to Seattle":    true,
		"Weekend 2013-08-16": true,
		"2018-07-13":         false,
		"2018-08-27-28":      false,
		"Photos from 2019":   false,
		"Photos from Paris":  true,
		"":                   false,
	} {
		if got := IsAlbumDir(dir); got != want {
			t.Errorf("IsAlbumDir(%q) = %v; want %v", dir, got, want)
		}
	}
	for name, want := range map[string]bool{
		"metadata.json":     true,
		"metadata(1).json":  true,
		"metadata(x).json":  false,
		"metadata.jpg.json": false,
	} {
		if got := IsAlbumMetadata(name); got != want {
			t.Errorf("IsAlbumMetadata(%q) = %v; want %v", name, got, want)
		}
	}
}

func TestMatchSidecars(t *testing.T) {
	long := "The.Newsroom.2012.S01E10.720p.HDTV.x264-EVOLVE.mkv"
	media := []string{
		"IMG_0001.JPG",
		"IMG_0990(1).JPG",
		"IMG_0990.JPG",
		long,
		"IMG_0002.HEIC",
		"PXL_0003.jpg",
		"renamed.jpg",
		"IMG_0004-edited.jpg",
		"2012-11-22.jpg",
		"2013-10-10(7).jpg",
	}
	sidecars := []string{
		"IMG_0001.JPG.json",
		"IMG_0990.JPG(1).json",
		"IMG_0990.JPG.json",
		long[:sidecarNameMax] + ".json",
		"IMG_0002.HEIC.supplemental-metadata.json",
		"PXL_0003.jpg.supplemental-me.json",
		"odd.json", // its title says renamed.jpg
		"metadata.json",
		"2012-11-22.json",    // its title has no extension
		"2013-10-10(7).json", // likewise
	}
	titles := map[string]string{"odd.json": "renamed.jpg"}
	got := MatchSidecars(media, sidecars, func(sc string) string { return titles[sc] })
	want := map[string]string{
		"IMG_0001.JPG":      "IMG_0001.JPG.json",
		"IMG_0990(1).JPG":   "IMG_0990.JPG(1).json",
		"IMG_0990.JPG":      "IMG_0990.JPG.json",
		long:                long[:sidecarNameMax] + ".json",
		"IMG_0002.HEIC":     "IMG_0002.HEIC.supplemental-metadata.json",
		"PXL_0003.jpg":      "PXL_0003.jpg.supplemental-me.json",
		"renamed.jpg":       "odd.json",
		"2012-11-22.jpg":    "2012-11-22.json",
		"2013-10-10(7).jpg": "2013-10-10(7).json",
	}
	if !maps.Equal(got, want) {
		t.Errorf("MatchSidecars:\n got %q\nwant %q", got, want)
	}
}

func TestNewLibrary(t *testing.T) {
	sidecar := func(title, desc string) string {
		return `{"title": "` + title + `", "description": "` + desc + `", "photoTakenTime": {"timestamp": "1531529326"}}`
	}
	zips := map[string][]byte{
		"takeout-20240101T120000Z-001.zip": makeZip(t, []testFile{
			{name: "Takeout/Google Photos/2018-07-13/IMG_0001.JPG", data: photoA},
			{name: "Takeout/Google Photos/2018-07-13/IMG_0001.JPG.json", data: sidecar("IMG_0001.JPG", "")},
			{name: "Takeout/Google Photos/2018-07-13/metadata.json", data: `{"albumData": {"title": "2018-07-13"}}`},
			{name: "Takeout/Google Photos/2018-07-14/VID_0002.MP4", data: videoB},
			{name: "Takeout/Google Photos/2018-07-14/VID_0002.MP4.json", data: sidecar("VID_0002.MP4", "a video")},
			{name: "Takeout/Google Photos/Trip/IMG_0001.JPG", data: photoA},
			{name: "Takeout/Google Photos/Trip/IMG_0001.JPG.json", data: sidecar("IMG_0001.JPG", "in the album")},
			{name: "Takeout/Google Photos/Trip/VID_0002.MP4", data: videoB},
			{name: "Takeout/Google Photos/Trip/metadata.json", data: `{"albumData": {"title": "Trip", "description": "First"}}`},
			{name: "Takeout/Google Photos/Trip/metadata(1).json", data: `{"albumData": {"title": "Trip", "description": "Second"}}`},
		}),
	}
	exps, err := LocalExports(writeZips(t, zips))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	cs := Contents(photosEntries(t, exps[0]))
	lib, err := NewLibrary(ctx, exps[0], cs, 4)
	if err != nil {
		t.Fatal(err)
	}
	if len(lib.Photos) != 2 {
		t.Fatalf("%d photos; want 2", len(lib.Photos))
	}
	photo, video := lib.Photos[0], lib.Photos[1]
	// The photo's metadata is from its first entry's sidecar, in the
	// day directory.
	if photo.Meta == nil || photo.Meta.Title != "IMG_0001.JPG" || photo.Meta.Description != "" {
		t.Errorf("photo metadata = %+v; want the day directory's sidecar", photo.Meta)
	}
	if video.Meta == nil || video.Meta.Description != "a video" {
		t.Errorf("video metadata = %+v", video.Meta)
	}
	// Day directories aren't albums.
	if len(lib.Albums) != 1 {
		t.Fatalf("%d albums; want 1", len(lib.Albums))
	}
	a := lib.Albums[0]
	if a.Title() != "Trip" || a.Description() != "First\n\nSecond" || len(a.Meta) != 2 {
		t.Errorf("album %q, description %q, %d metadata files", a.Title(), a.Description(), len(a.Meta))
	}
	if len(a.Photos) != 2 || a.Photos[0] != photo || a.Photos[1] != video {
		t.Errorf("album photos = %v; want the photo and the video", a.Photos)
	}
}
