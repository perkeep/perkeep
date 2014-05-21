// Copyright 2014 Tamás Gulácsi. All rights reserved.
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package picago

import (
	"fmt"
	"io"
	"io/ioutil"
	"log"
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

var DebugDir string

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

type Photo struct {
	ID, Title, Summary, Description, Location string
	Keywords                                  []string
	Published, Updated                        time.Time
	Latitude, Longitude                       float64
	URL, Type                                 string
	Exif                                      Exif
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
		albums = append(albums, Album{
			ID:          entry.ID,
			Name:        entry.Name,
			Summary:     entry.Summary,
			Title:       entry.Title,
			Description: entry.Media.Description,
			Location:    entry.Location,
			AuthorName:  entry.Author.Name,
			AuthorURI:   entry.Author.URI,
			Keywords:    strings.Split(entry.Media.Keywords, ","),
			Published:   entry.Published,
			Updated:     entry.Updated,
			URL:         albumURL,
		})
	}
	return albums, startIndex+len(feed.Entries) < feed.TotalResults, nil
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
	if cap(photos)-len(photos) < len(feed.Entries) {
		photos = append(photos, make([]Photo, 0, len(feed.Entries))...)
	}
	for _, entry := range feed.Entries {
		var lat, long float64
		i := strings.Index(entry.Point, " ")
		if i >= 1 {
			lat, err = strconv.ParseFloat(entry.Point[:i], 64)
			if err != nil {
				log.Printf("cannot parse %q as latitude: %v", entry.Point[:i], err)
			}
			long, err = strconv.ParseFloat(entry.Point[i+1:], 64)
			if err != nil {
				log.Printf("cannot parse %q as longitude: %v", entry.Point[i+1:], err)
			}
		}
		if entry.Point != "" && lat == 0 && long == 0 {
			log.Fatalf("point=%q but couldn't parse it as lat/long", entry.Point)
		}
		url, typ := entry.Content.URL, entry.Content.Type
		if url == "" {
			url, typ = entry.Media.Content.URL, entry.Media.Content.Type
		}
		title := entry.Title
		if title == "" {
			title = entry.Media.Title
		}
		photos = append(photos, Photo{
			ID:          entry.ID,
			Exif:        entry.Exif,
			Summary:     entry.Summary,
			Title:       title,
			Description: entry.Media.Description,
			Location:    entry.Location,
			//AuthorName:  entry.Author.Name,
			//AuthorURI:   entry.Author.URI,
			Keywords:  strings.Split(entry.Media.Keywords, ","),
			Published: entry.Published,
			Updated:   entry.Updated,
			URL:       url,
			Type:      typ,
			Latitude:  lat,
			Longitude: long,
		})
	}
	return photos, len(photos) < feed.NumPhotos, nil
}

func downloadAndParse(client *http.Client, url string) (*Atom, error) {
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= http.StatusBadRequest {
		buf, _ := ioutil.ReadAll(resp.Body)
		return nil, fmt.Errorf("downloadAndParse(%s) got %s\n%s", url, resp.Status, buf)
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
		return User{}, err
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
