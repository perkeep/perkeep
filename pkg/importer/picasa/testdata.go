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

package picasa

import (
	"encoding/xml"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/httputil"
	"camlistore.org/pkg/importer"
	"camlistore.org/pkg/osutil"

	"camlistore.org/third_party/github.com/tgulacsi/picago"
)

var _ importer.TestDataMaker = (*imp)(nil)

func (im *imp) SetTestAccount(acctNode *importer.Object) error {
	// TODO(mpl): refactor with twitter
	return acctNode.SetAttrs(
		importer.AcctAttrAccessToken, "fakeAccessToken",
		importer.AcctAttrAccessTokenSecret, "fakeAccessSecret",
		importer.AcctAttrUserID, "fakeUserId",
		importer.AcctAttrName, "fakeName",
		importer.AcctAttrUserName, "fakeScreenName",
	)
}

func (im *imp) MakeTestData() http.RoundTripper {

	const (
		apiURL        = "https://picasaweb.google.com/data/feed/api"
		nAlbums       = 10 // Arbitrary number of albums generated.
		nEntries      = 3  // number of albums or photos returned in the feed at each call.
		defaultUserId = "default"
	)

	albumsListCached := make(map[int]string)
	okHeader := `HTTP/1.1 200 OK
Content-Type: application/json; charset=UTF-8

`

	responses := make(map[string]func() *http.Response)

	// register the get albums list calls
	for i := 1; i < nAlbums+1; i += nEntries {
		url := fmt.Sprintf("%s/user/%s?start-index=%d", apiURL, defaultUserId, i)
		response := okHeader + fakeAlbumsList(i, nAlbums, nEntries, albumsListCached)
		responses[url] = httputil.StaticResponder(response)
	}

	// register the get album calls
	for i := 1; i < nAlbums+1; i++ {
		albumId := blob.RefFromString(fmt.Sprintf("Album %d", i)).DigestPrefix(10)
		for j := 1; j < i+1; j += nEntries {
			url := fmt.Sprintf("%s/user/%s/albumid/%s?imgmax=d&start-index=%d", apiURL, defaultUserId, albumId, j)
			// Using i as nTotal argument means album N will have N photos in it.
			response := okHeader + fakePhotosList(j, i, nEntries)
			responses[url] = httputil.StaticResponder(response)
		}
	}

	// register the photo download calls
	pudgyPic := fakePhoto()
	photoURL1 := "https://camlistore.org/pic/pudgy1.png"
	photoURL2 := "https://camlistore.org/pic/pudgy2.png"
	responses[photoURL1] = httputil.FileResponder(pudgyPic)
	responses[photoURL2] = httputil.FileResponder(pudgyPic)

	return httputil.NewFakeTransport(responses)
}

// fakeAlbumsList returns an xml feed of albums. The feed starts at index, and
// ends at index + nEntries (exclusive), or at nTotal (inclusive), whichever is the
// lowest.
func fakeAlbumsList(index, nTotal, nEntries int, cached map[int]string) string {
	if cl, ok := cached[index]; ok {
		return cl
	}

	max := index + nEntries
	if max > nTotal+1 {
		max = nTotal + 1
	}
	var entries []picago.Entry
	for i := index; i < max; i++ {
		entries = append(entries, fakeAlbum(i))
	}
	atom := &picago.Atom{
		TotalResults: nTotal,
		Entries:      entries,
	}

	feed, err := xml.MarshalIndent(atom, "", "	")
	if err != nil {
		log.Fatalf("%v", err)
	}
	cached[index] = string(feed)
	return cached[index]
}

func fakeAlbum(counter int) picago.Entry {
	author := picago.Author{
		Name: "fakeAuthorName",
	}
	media := &picago.Media{
		Description: "fakeAlbumDescription",
		Keywords:    "fakeKeyword1,fakeKeyword2",
	}
	title := fmt.Sprintf("Album %d", counter)
	year := time.Hour * 24 * 365
	month := year / 12
	return picago.Entry{
		ID:        blob.RefFromString(title).DigestPrefix(10),
		Published: time.Now().Add(-time.Duration(counter) * year),
		Updated:   time.Now().Add(-time.Duration(counter) * month),
		Name:      "fakeAlbumName",
		Title:     title,
		Summary:   "fakeAlbumSummary",
		Location:  "fakeAlbumLocation",
		Author:    author,
		Media:     media,
	}
}

// fakePhotosList returns an xml feed of an album's photos. The feed starts at
// index, and ends at index + nEntries (exclusive), or at nTotal (inclusive),
// whichever is the lowest.
func fakePhotosList(index, nTotal, nEntries int) string {
	max := index + nEntries
	if max > nTotal+1 {
		max = nTotal + 1
	}
	var entries []picago.Entry
	for i := index; i < max; i++ {
		entries = append(entries, fakePhotoEntry(i, nTotal))
	}
	atom := &picago.Atom{
		NumPhotos: nTotal,
		Entries:   entries,
	}

	feed, err := xml.MarshalIndent(atom, "", "	")
	if err != nil {
		log.Fatalf("%v", err)
	}
	return string(feed)
}

func fakePhotoEntry(photoNbr int, albumNbr int) picago.Entry {
	var content picago.EntryContent
	if photoNbr%2 == 0 {
		content = picago.EntryContent{
			URL:  "https://camlistore.org/pic/pudgy1.png",
			Type: "image/png",
		}
	}
	var point string
	if photoNbr%3 == 0 {
		point = "37.7447124 -122.4341914"
	} else {
		point = "45.1822842 5.7141854"
	}
	mediaContent := picago.MediaContent{
		URL:  "https://camlistore.org/pic/pudgy2.png",
		Type: "image/png",
	}
	media := &picago.Media{
		Title:       "fakePhotoTitle",
		Description: "fakePhotoDescription",
		Keywords:    "fakeKeyword1,fakeKeyword2",
		Content:     []picago.MediaContent{mediaContent},
	}
	// to be consistent, all the pics times should be anterior to their respective albums times. whatever.
	day := time.Hour * 24
	year := day * 365
	created := time.Now().Add(-time.Duration(photoNbr) * year)
	published := created.Add(day)
	updated := published.Add(day)

	exif := &picago.Exif{
		FStop:       7.7,
		Make:        "whatisthis?", // not obvious to me, needs doc in picago
		Model:       "potato",
		Exposure:    7.7,
		Flash:       false,
		FocalLength: 7.7,
		ISO:         100,
		Timestamp:   created.Unix(),
		UID:         "whatisthis?", // not obvious to me, needs doc in picago
	}

	title := fmt.Sprintf("Photo %d of album %d", photoNbr, albumNbr)
	return picago.Entry{
		ID:        blob.RefFromString(title).DigestPrefix(10),
		Exif:      exif,
		Summary:   "fakePhotoSummary",
		Title:     title,
		Location:  "fakePhotoLocation",
		Published: published,
		Updated:   updated,
		Media:     media,
		Point:     point,
		Content:   content,
	}
}

// TODO(mpl): refactor with twitter
func fakePhoto() string {
	camliDir, err := osutil.GoPackagePath("camlistore.org")
	if err == os.ErrNotExist {
		log.Fatal("Directory \"camlistore.org\" not found under GOPATH/src; are you not running with devcam?")
	}
	if err != nil {
		log.Fatalf("Error searching for \"camlistore.org\" under GOPATH: %v", err)
	}
	return filepath.Join(camliDir, filepath.FromSlash("third_party/glitch/npc_piggy__x1_walk_png_1354829432.png"))
}
