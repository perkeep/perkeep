// Copyright 2014 Tamás Gulácsi. All rights reserved.
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package picago

import (
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	neturl "net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	albumURL = "https://picasaweb.google.com/data/feed/api/user/{userID}?start-index={startIndex}"

	// imgmax=d is needed for original photo's download
	photoURL = "https://picasaweb.google.com/data/feed/api/user/{userID}/albumid/{albumID}?imgmax=d&start-index={startIndex}"
	userURL  = "https://picasaweb.google.com/data/feed/api/user/{userID}/contacts?kind=user"
)

var DebugDir = os.Getenv("PICAGO_DEBUG_DIR")

type User struct {
	ID, URI, Name, Thumbnail string
}

// An Album is a collection of Picasaweb or Google+ photos.
type Album struct {
	// ID is the stable identifier for an album.
	// e.g. "6041693388376552305"
	ID string

	// Name appears to be the Title, but with spaces removed. It
	// shows up in URLs but is not a stable
	// identifier. e.g. "BikingWithBlake"
	Name string

	// Title is the title of the album.
	// e.g. "Biking with Blake"
	Title string

	// Description is the Picasaweb "Description" field, and does
	// not appear available or shown in G+ Photos. It may be
	// contain newlines.
	Description string

	// Location is free-form location text. e.g. "San Bruno Mountain"
	Location string

	// URL is the main human-oriented (HTML) URL to the album.
	URL string

	// Published is the either the time the user actually created
	// and published the gallery or (in the case of Picasaweb at
	// least), the date that the user set on the gallery.  It will
	// be at day granularity, but the hour will be adjusted based
	// on whatever timezone the user is it. For instance, setting
	// July 21, 2014 while in California results in a time of
	// 2014-07-21T07:00:00.000Z since that was the UTC time at
	// which it became July 21st in US/Pacific on that day.
	Published time.Time

	// Updated is the server time any property of the gallery was
	// changed.  It appears to be at millisecond granularity.
	Updated time.Time

	AuthorName, AuthorURI string
}

// A Photo is a photo (or video) in a Picasaweb (or G+) gallery.
type Photo struct {
	// ID is the stable identifier for the photo.
	ID string

	// Filename is the image's filename from the Atom title field.
	Filename string

	// Description is the caption of the photo.
	Description string

	Keywords           []string
	Published, Updated time.Time

	// Latitude and Longitude optionally contain the GPS coordinates
	// of the photo.
	Latitude, Longitude float64

	// Location is free-form text describing the location of the
	// photo.
	Location string

	// URL is the URL of the photo or video.
	URL string

	// PageURL is the URL to the page showing just this image.
	PageURL string

	// Type is the Content-Type.
	Type string

	// Position is the 1-based position within a gallery.
	// It is zero if unknown.
	Position int

	Exif *Exif
}

// GetAlbums returns the list of albums of the given userID.
// If userID is empty, "default" is used.
func GetAlbums(client *http.Client, userID string) ([]Album, error) {
	if userID == "" {
		userID = "default"
	}
	url := strings.Replace(albumURL, "{userID}", userID, 1)

	var albums []Album
	var err error
	hasMore, startIndex := true, 1
	for hasMore {
		albums, hasMore, err = getAlbums(albums, client, url, startIndex)
		if !hasMore {
			break
		}
		startIndex = len(albums) + 1
	}
	return albums, err
}

func getAlbums(albums []Album, client *http.Client, url string, startIndex int) ([]Album, bool, error) {
	if startIndex <= 0 {
		startIndex = 1
	}
	feed, err := downloadAndParse(client,
		strings.Replace(url, "{startIndex}", strconv.Itoa(startIndex), 1))
	if err != nil {
		return albums, false, err
	}
	if len(feed.Entries) == 0 {
		return albums, false, nil
	}
	for _, entry := range feed.Entries {
		albums = append(albums, entry.album())
	}
	return albums, true, nil
}

func (e *Entry) album() Album {
	a := Album{
		ID:          e.ID,
		Name:        e.Name,
		Title:       e.Title,
		Location:    e.Location,
		AuthorName:  e.Author.Name,
		AuthorURI:   e.Author.URI,
		Published:   e.Published,
		Updated:     e.Updated,
		Description: e.Summary,
	}
	for _, link := range e.Links {
		if link.Rel == "alternate" && link.Type == "text/html" {
			a.URL = link.URL
			break
		}
	}
	if e.Media != nil {
		if a.Description == "" {
			a.Description = e.Media.Description
		}
	}
	return a
}

func GetPhotos(client *http.Client, userID, albumID string) ([]Photo, error) {
	if userID == "" {
		userID = "default"
	}
	url := strings.Replace(photoURL, "{userID}", userID, 1)
	url = strings.Replace(url, "{albumID}", albumID, 1)

	var photos []Photo
	var err error
	hasMore, startIndex := true, 1
	for hasMore {
		photos, hasMore, err = getPhotos(photos, client, url, startIndex)
		if !hasMore {
			break
		}
		startIndex = len(photos) + 1
	}
	return photos, err
}

func getPhotos(photos []Photo, client *http.Client, url string, startIndex int) ([]Photo, bool, error) {
	if startIndex <= 0 {
		startIndex = 1
	}
	feed, err := downloadAndParse(client,
		strings.Replace(url, "{startIndex}", strconv.Itoa(startIndex), 1))
	if err != nil {
		return nil, false, err
	}
	if len(feed.Entries) == 0 {
		return photos, false, nil
	}
	for i, entry := range feed.Entries {
		p, err := entry.photo()
		if err != nil {
			return nil, false, err
		}
		p.Position = startIndex + i
		photos = append(photos, p)
	}

	// The number of photos can change while the import is happening. More
	// realistically, Aaron Boodman has observed feed.NumPhotos disagreeing with
	// len(feed.Entries). So to be on the safe side, just keep trying until we
	// get a response with zero entries.
	return photos, true, nil
}

func (e *Entry) photo() (p Photo, err error) {
	var lat, long float64
	i := strings.Index(e.Point, " ")
	if i >= 1 {
		lat, err = strconv.ParseFloat(e.Point[:i], 64)
		if err != nil {
			return p, fmt.Errorf("cannot parse %q as latitude: %v", e.Point[:i], err)
		}
		long, err = strconv.ParseFloat(e.Point[i+1:], 64)
		if err != nil {
			return p, fmt.Errorf("cannot parse %q as longitude: %v", e.Point[i+1:], err)
		}
	}
	if e.Point != "" && lat == 0 && long == 0 {
		return p, fmt.Errorf("point=%q but couldn't parse it as lat/long", e.Point)
	}
	p = Photo{
		ID:          e.ID,
		Exif:        e.Exif,
		Description: e.Summary,
		Filename:    e.Title,
		Location:    e.Location,
		Published:   e.Published,
		Updated:     e.Updated,
		Latitude:    lat,
		Longitude:   long,
	}
	for _, link := range e.Links {
		if link.Rel == "alternate" && link.Type == "text/html" {
			p.PageURL = link.URL
			break
		}
	}
	if e.Media != nil {
		for _, kw := range strings.Split(e.Media.Keywords, ",") {
			if kw := strings.TrimSpace(kw); kw != "" {
				p.Keywords = append(p.Keywords, kw)
			}
		}
		if p.Description == "" {
			p.Description = e.Media.Description
		}
		if mc, ok := e.Media.bestContent(); ok {
			p.URL, p.Type = mc.URL, mc.Type
		}
		if p.Filename == "" {
			p.Filename = e.Media.Title
		}
	}
	return p, nil
}

func (m *Media) bestContent() (ret MediaContent, ok bool) {
	// Find largest non-Flash video.
	var bestPixels int64
	for _, mc := range m.Content {
		thisPixels := int64(mc.Width) * int64(mc.Height)
		if mc.Medium == "video" && mc.Type != "application/x-shockwave-flash" && thisPixels > bestPixels {
			ret = mc
			ok = true
			bestPixels = thisPixels
		}
	}
	if ok {
		return
	}

	// Else, just find largest anything.
	bestPixels = 0
	for _, mc := range m.Content {
		thisPixels := int64(mc.Width) * int64(mc.Height)
		if thisPixels > bestPixels {
			ret = mc
			ok = true
			bestPixels = thisPixels
		}
	}
	return
}

func downloadAndParse(client *http.Client, url string) (*Atom, error) {
	resp, err := client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("downloadAndParse: get %q: %v", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= http.StatusBadRequest {
		buf, _ := ioutil.ReadAll(resp.Body)
		return nil, fmt.Errorf("downloadAndParse(%s) got %s (%s)", url, resp.Status, buf)
	}
	var r io.Reader = resp.Body
	if DebugDir != "" {
		fn := filepath.Join(DebugDir, neturl.QueryEscape(url)+".xml")
		xmlfh, err := os.Create(fn)
		if err != nil {
			return nil, fmt.Errorf("error creating debug filx %s: %v", fn, err)
		}
		defer xmlfh.Close()
		r = io.TeeReader(resp.Body, xmlfh)
	}
	return ParseAtom(r)
}

// DownloadPhoto returns an io.ReadCloser for reading the photo bytes
func DownloadPhoto(client *http.Client, url string) (io.ReadCloser, error) {
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= http.StatusBadRequest {
		buf, _ := ioutil.ReadAll(resp.Body)
		return nil, fmt.Errorf("downloading %s: %s: %s", url, resp.Status, buf)
	}
	return resp.Body, nil
}

// GetUser returns the user's info
func GetUser(client *http.Client, userID string) (User, error) {
	if userID == "" {
		userID = "default"
	}
	url := strings.Replace(userURL, "{userID}", userID, 1)
	feed, err := downloadAndParse(client, url)
	if err != nil {
		return User{}, fmt.Errorf("GetUser: downloading %s: %v", url, err)
	}
	uri := feed.Author.URI
	id := uri
	i := strings.LastIndex(uri, "/")
	if i >= 0 {
		id = uri[i+1:]
	}
	return User{
		ID:        id,
		URI:       feed.Author.URI,
		Name:      feed.Author.Name,
		Thumbnail: feed.Thumbnail,
	}, nil
}
