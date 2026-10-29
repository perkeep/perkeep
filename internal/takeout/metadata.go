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
	"encoding/json"
	"fmt"
	"math"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// PhotoMetadata is the metadata of a Google Photos photo or video, from
// the JSON file ("sidecar") that Takeout writes beside it.
type PhotoMetadata struct {
	Title       string // the original file name
	Description string
	URL         string

	PhotoTaken time.Time // when the photo was taken
	Created    time.Time // when it was added to Google Photos
	Modified   time.Time // when its metadata last changed in Google Photos

	// Location, if HasLocation, from geoData, or else geoDataExif.
	HasLocation                   bool
	Latitude, Longitude, Altitude float64

	People []string // names of the people tagged in it

	// JSON is the sidecar.
	JSON []byte
}

type takeoutTime struct {
	Timestamp string `json:"timestamp"`
}

func (t *takeoutTime) time() time.Time {
	if t == nil {
		return time.Time{}
	}
	sec, err := strconv.ParseInt(t.Timestamp, 10, 64)
	if err != nil || sec <= 0 {
		return time.Time{}
	}
	return time.Unix(sec, 0).UTC()
}

type takeoutGeo struct {
	Latitude  jsonFloat `json:"latitude"`
	Longitude jsonFloat `json:"longitude"`
	Altitude  jsonFloat `json:"altitude"`
}

func (g *takeoutGeo) valid() bool {
	return g != nil && g.Latitude.finite() && g.Longitude.finite() && (g.Latitude != 0 || g.Longitude != 0)
}

// jsonFloat is a number in JSON, which Takeout sometimes writes as a
// string, such as "NaN".
type jsonFloat float64

func (f *jsonFloat) UnmarshalJSON(b []byte) error {
	s := string(b)
	if unq, err := strconv.Unquote(s); err == nil {
		s = unq
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return fmt.Errorf("invalid number %s", b)
	}
	*f = jsonFloat(v)
	return nil
}

func (f jsonFloat) finite() bool { return !math.IsNaN(float64(f)) && !math.IsInf(float64(f), 0) }

// value returns f, or 0 if it's not finite.
func (f jsonFloat) value() float64 {
	if !f.finite() {
		return 0
	}
	return float64(f)
}

// ParsePhotoMetadata parses a photo's sidecar.
func ParsePhotoMetadata(data []byte) (*PhotoMetadata, error) {
	var j struct {
		Title          string       `json:"title"`
		Description    string       `json:"description"`
		URL            string       `json:"url"`
		PhotoTakenTime *takeoutTime `json:"photoTakenTime"`
		CreationTime   *takeoutTime `json:"creationTime"`
		ModTime        *takeoutTime `json:"photoLastModifiedTime"`
		OldModTime     *takeoutTime `json:"modificationTime"`
		GeoData        *takeoutGeo  `json:"geoData"`
		GeoDataExif    *takeoutGeo  `json:"geoDataExif"`
		People         []struct {
			Name string `json:"name"`
		} `json:"people"`
	}
	if err := json.Unmarshal(data, &j); err != nil {
		return nil, err
	}
	m := &PhotoMetadata{
		Title:       j.Title,
		Description: strings.TrimSpace(j.Description),
		URL:         j.URL,
		PhotoTaken:  j.PhotoTakenTime.time(),
		Created:     j.CreationTime.time(),
		Modified:    j.ModTime.time(),
		JSON:        data,
	}
	if m.Modified.IsZero() {
		m.Modified = j.OldModTime.time()
	}
	for _, g := range []*takeoutGeo{j.GeoData, j.GeoDataExif} {
		if g.valid() {
			m.HasLocation = true
			m.Latitude, m.Longitude, m.Altitude = g.Latitude.value(), g.Longitude.value(), g.Altitude.value()
			break
		}
	}
	for _, p := range j.People {
		if p.Name != "" {
			m.People = append(m.People, p.Name)
		}
	}
	return m, nil
}

// AlbumMetadata is the metadata of a Google Photos album, from the
// metadata.json file in its directory.
type AlbumMetadata struct {
	Title       string
	Description string
	Location    string
	Date        time.Time
	Access      string // such as "public" or "protected"

	HasLocation         bool
	Latitude, Longitude float64

	// JSON is the metadata.json file.
	JSON []byte
}

// ParseAlbumMetadata parses an album's metadata.json file.
func ParseAlbumMetadata(data []byte) (*AlbumMetadata, error) {
	var j struct {
		AlbumData struct {
			Title       string       `json:"title"`
			Description string       `json:"description"`
			Location    string       `json:"location"`
			Access      string       `json:"access"`
			Date        *takeoutTime `json:"date"`
			GeoData     *takeoutGeo  `json:"geoData"`
		} `json:"albumData"`
		// Newer exports have these at the top level.
		Title       string       `json:"title"`
		Description string       `json:"description"`
		Access      string       `json:"access"`
		Date        *takeoutTime `json:"date"`
	}
	if err := json.Unmarshal(data, &j); err != nil {
		return nil, err
	}
	a := j.AlbumData
	m := &AlbumMetadata{
		Title:       a.Title,
		Description: strings.TrimSpace(a.Description),
		Location:    strings.TrimSpace(a.Location),
		Date:        a.Date.time(),
		Access:      a.Access,
		JSON:        data,
	}
	if m.Title == "" {
		m.Title, m.Description, m.Access, m.Date = j.Title, strings.TrimSpace(j.Description), j.Access, j.Date.time()
	}
	if a.GeoData.valid() {
		m.HasLocation, m.Latitude, m.Longitude = true, a.GeoData.Latitude.value(), a.GeoData.Longitude.value()
	}
	return m, nil
}

var (
	// dayDirRx matches the names of the directories of Google Photos'
	// photos by date, such as "2018-07-13" or "2018-08-27-28", which
	// older exports have.
	dayDirRx = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}(-\d{2})*( #\d+)?$`)
	// yearDirRx matches "Photos from 2019", in newer exports.
	yearDirRx = regexp.MustCompile(`^Photos from \d{4}$`)
)

// IsAlbumDir reports whether dir, the name of a directory of Google
// Photos files in a Takeout export, such as "Trip to Seattle", is an
// album's, rather than one of the directories that Takeout puts every
// photo in by its date.
func IsAlbumDir(dir string) bool {
	return dir != "" && !dayDirRx.MatchString(dir) && !yearDirRx.MatchString(dir)
}

// IsAlbumMetadata reports whether name, the base name of a file in a
// Google Photos directory, is the metadata of an album in it. A directory
// has more than one, metadata(1).json and so on, if more than one album
// has its name.
func IsAlbumMetadata(name string) bool {
	_, ok := albumMetadataIndex(name)
	return ok
}

// albumMetadataIndex returns 0 for "metadata.json", n for
// "metadata(n).json", and ok false for other names.
func albumMetadataIndex(name string) (n int, ok bool) {
	if name == "metadata.json" {
		return 0, true
	}
	s, ok := strings.CutPrefix(name, "metadata(")
	if !ok {
		return 0, false
	}
	s, ok = strings.CutSuffix(s, ").json")
	n, err := strconv.Atoi(s)
	return n, ok && err == nil
}

// sidecarNameMax is how long, before ".json", Takeout cuts the names of
// sidecars, of a media file's name plus any suffix such as
// ".supplemental-metadata".
const sidecarNameMax = 46

// numberedRx matches the stem of a sidecar of a numbered duplicate, such
// as "IMG_0990.JPG(1)" for "IMG_0990(1).JPG".
var numberedRx = regexp.MustCompile(`^(.*)\((\d+)\)$`)

// MatchSidecars returns the sidecar of each of media, the base names of
// photos and videos in one directory, from sidecars, the base names of
// the JSON files in it, and titles, which returns a sidecar's title (the
// original file name), if needed to resolve names that Takeout mangled.
func MatchSidecars(media, sidecars []string, title func(sidecar string) string) map[string]string {
	mediaSet := make(map[string]bool, len(media))
	for _, m := range media {
		mediaSet[m] = true
	}
	matched := make(map[string]string) // media to sidecar
	match := func(m, sc string) bool {
		if !mediaSet[m] {
			return false
		}
		if _, ok := matched[m]; ok {
			return false
		}
		matched[m] = sc
		return true
	}
	var unmatched []string
	for _, sc := range sidecars {
		if IsAlbumMetadata(sc) {
			continue
		}
		stem := strings.TrimSuffix(sc, ".json")
		// Newer exports name sidecars "IMG.jpg.supplemental-metadata.json",
		// or, if that's too long, with the suffix cut short.
		if i := strings.LastIndex(stem, ".s"); i > 0 && strings.HasPrefix(".supplemental-metadata", stem[i:]) && len(stem[i:]) >= 3 {
			stem = stem[:i]
		}
		if match(stem, sc) {
			continue
		}
		if m := numberedRx.FindStringSubmatch(stem); m != nil {
			base, n := m[1], m[2]
			ext := path.Ext(base)
			if match(strings.TrimSuffix(base, ext)+"("+n+")"+ext, sc) {
				continue
			}
		}
		unmatched = append(unmatched, sc)
	}
	// Names cut short, or otherwise mangled: by the sidecar's title, or
	// the one media file whose name, less its extension, is the sidecar's
	// stem (as when a photo's title has no extension, and Takeout adds
	// one to the file's name, but not the sidecar's), or else starts with
	// it.
	for _, sc := range unmatched {
		if t := title(sc); t != "" && match(t, sc) {
			continue
		}
		stem := strings.TrimSuffix(sc, ".json")
		var exts []string
		for _, m := range media {
			if _, ok := matched[m]; !ok && strings.TrimSuffix(m, path.Ext(m)) == stem {
				exts = append(exts, m)
			}
		}
		if len(exts) == 1 {
			match(exts[0], sc)
			continue
		}
		if len(stem) < sidecarNameMax {
			continue
		}
		var cands []string
		for _, m := range media {
			if _, ok := matched[m]; !ok && strings.HasPrefix(m, stem) {
				cands = append(cands, m)
			}
		}
		if len(cands) == 1 {
			match(cands[0], sc)
		}
	}
	return matched
}
