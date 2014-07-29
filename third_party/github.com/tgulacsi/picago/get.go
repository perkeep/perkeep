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

type Album struct {
	ID, Name, Title, Summary, Description, Location string
	AuthorName, AuthorURI                           string
	Keywords                                        []string
	Published, Updated                              time.Time
	URL                                             string
}

// A Photo is a photo (or video) in a Picasaweb (or G+) gallery.
type Photo struct {
	ID, Title, Summary, Description string
	Keywords                        []string
	Published, Updated              time.Time

	// Latitude and Longitude optionally contain the GPS coordinates
	// of the photo.
	Latitude, Longitude float64

	// Location is free-form text describing the location of the
	// photo.
	Location string

	// URL is the URL of the photo or video.
	URL string

	// Type is the Content-Type.
	Type string

	Exif *Exif
}

// Filename returns the filename of the photo (from title or ID + type).
func (p Photo) Filename() string {
	fn := p.Title
	if fn == "" {
		if len(p.URL) > 8 {
			bn := filepath.Base(p.URL[8:])
			if len(bn) < 128 {
				ext := filepath.Ext(bn)
				fn = bn[:len(bn)-len(ext)] + "-" + p.ID + ext
			}
			if fn == "" {
				fn = p.ID + "." + strings.SplitN(p.Type, "/", 2)[1]
			}
		}
	}
	return fn
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
		return nil, false, err
	}
	if len(feed.Entries) == 0 {
		return nil, false, nil
	}
	if cap(albums)-len(albums) < len(feed.Entries) {
		albums = append(albums, make([]Album, 0, len(feed.Entries))...)
	}
	for _, entry := range feed.Entries {
		albumURL := ""
		for _, link := range entry.Links {
			if link.Rel == "http://schemas.google.com/g/2005#feed" {
				albumURL = link.URL
				break
			}
		}
		var des string
		var kw []string
		if entry.Media != nil {
			des = entry.Media.Description
			kw = strings.Split(entry.Media.Keywords, ",")
		}
		albums = append(albums, Album{
			ID:          entry.ID,
			Name:        entry.Name,
			Summary:     entry.Summary,
			Title:       entry.Title,
			Description: des,
			Location:    entry.Location,
			AuthorName:  entry.Author.Name,
			AuthorURI:   entry.Author.URI,
			Keywords:    kw,
			Published:   entry.Published,
			Updated:     entry.Updated,
			URL:         albumURL,
		})
	}
	// since startIndex starts at 1, we need to compensate for this, just as we do for photos.
	return albums, startIndex+len(feed.Entries) <= feed.TotalResults, nil
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
		return nil, false, nil
	}
	for _, entry := range feed.Entries {
		p, err := entry.photo()
		if err != nil {
			return nil, false, err
		}
		photos = append(photos, p)
	}
	// startIndex starts with 1, we need to compensate for it.
	return photos, startIndex+len(feed.Entries) <= feed.NumPhotos, nil
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
		ID:        e.ID,
		Exif:      e.Exif,
		Summary:   e.Summary,
		Title:     e.Title,
		Location:  e.Location,
		Published: e.Published,
		Updated:   e.Updated,
		Latitude:  lat,
		Longitude: long,
	}
	if e.Media != nil {
		p.Keywords = strings.Split(e.Media.Keywords, ",")
		p.Description = e.Media.Description
		if mc, ok := e.Media.bestContent(); ok {
			p.URL, p.Type = mc.URL, mc.Type
		}
		if p.Title == "" {
			p.Title = e.Media.Title
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
