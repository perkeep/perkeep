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

package twitter

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"perkeep.org/internal/osutil"
	"perkeep.org/pkg/importer"
)

var _ importer.TestDataMaker = (*imp)(nil)

func (im *imp) SetTestAccount(acctNode *importer.Object) error {
	return acctNode.SetAttrs(
		importer.KV(importer.AcctAttrAccessToken, "fakeAccessToken"),
		importer.KV(importer.AcctAttrAccessTokenSecret, "fakeAccessSecret"),
		importer.KV(importer.AcctAttrUserID, "fakeUserID"),
		importer.KV(importer.AcctAttrName, "fakeName"),
		importer.KV(importer.AcctAttrUserName, "fakeScreenName"),
	)
}

func (im *imp) MakeTestData() http.RoundTripper {
	const (
		fakeMaxId = int64(486450108201201664) // Most recent tweet.
		nTweets   = 300                       // Arbitrary number of tweets generated.
	)
	return &fakeAPI{
		maxID:   fakeMaxId,
		minID:   fakeMaxId - nTweets, // Oldest tweet in our timeline.
		picture: fakePicture(),
	}
}

// fakeAPI serves a timeline of the tweets of IDs from maxID down to, but
// not including, minID, as the API v2 does, in pages of apiPageSize, and
// their media.
type fakeAPI struct {
	maxID, minID int64
	picture      string // file of the media
}

func (f *fakeAPI) RoundTrip(req *http.Request) (*http.Response, error) {
	u := req.URL
	switch {
	case u.Host == "api.x.com" && u.Path == "/2/"+apiTweetsPath("fakeUserID", false):
		from, until := f.maxID, f.minID
		if tok := u.Query().Get("pagination_token"); tok != "" {
			var err error
			if from, err = strconv.ParseInt(tok, 10, 64); err != nil {
				return nil, err
			}
		}
		if s := u.Query().Get("since_id"); s != "" {
			since, err := strconv.ParseInt(s, 10, 64)
			if err != nil {
				return nil, err
			}
			until = max(until, since)
		}
		return jsonResponse(req, fakeTimeLine(from, until))
	case u.Host == "pbs.twimg.com", u.Host == "twitpic.com", u.Host == "i.imgur.com":
		pic, err := os.ReadFile(f.picture)
		if err != nil {
			return nil, err
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(pic)), Request: req}, nil
	}
	return &http.Response{StatusCode: http.StatusNotFound, Status: "404 Not Found", Body: http.NoBody, Request: req}, nil
}

func jsonResponse(req *http.Request, v any) (*http.Response, error) {
	j, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"application/json; charset=UTF-8"}},
		Body:       io.NopCloser(bytes.NewReader(j)),
		Request:    req,
	}, nil
}

// fakeTimeLine returns a page of a user timeline of apiPageSize tweets, starting
// with maxId as the most recent tweet id. It stops before apiPageSize if
// minId is reached.
func fakeTimeLine(maxId, minId int64) *apiTweetsPage {
	page := new(apiTweetsPage)
	page.Includes.Media = fakeMedia()
	end := max(maxId-int64(apiPageSize), minId)
	for i := maxId; i > end; i-- {
		tweet := &apiTweet{
			Id:           strconv.FormatInt(i, 10),
			TextStr:      fmt.Sprintf("fakeText %d", i),
			CreatedAtStr: time.Now().Format(time.RFC3339),
		}
		tweet.Attachments.MediaKeys, tweet.Entities.URLs = fakeEntities(int(i % 7))
		if c := fakeCoords(int(i % 5)); c != nil {
			tweet.Geo = &struct {
				Coordinates *coords `json:"coordinates"`
			}{c}
		}
		page.Data = append(page.Data, tweet)
	}
	page.Meta.ResultCount = len(page.Data)
	page.Meta.NewestID = strconv.FormatInt(maxId, 10)
	if end > minId {
		page.Meta.NextToken = strconv.FormatInt(end, 10)
	}
	return page
}

// fakeCoords returns the coordinates of the counter'th kind of location:
// none, San Francisco, or Grenoble.
func fakeCoords(counter int) *coords {
	switch counter {
	case 1:
		return &coords{[]float64{-122.4341914, 37.7447124}}
	case 2:
		return &coords{[]float64{5.7141854, 45.1822842}}
	}
	return nil
}

// fakeMedia returns the media that fakeEntities' tweets have.
func fakeMedia() []*apiMedia {
	return []*apiMedia{
		{MediaKey: "3_1", Type: "photo", URL: "https://pbs.twimg.com/media/foo.jpg"},
		{MediaKey: "3_2", Type: "photo", URL: "https://pbs.twimg.com/media/bar.png"},
	}
}

// fakeEntities returns the media keys and links of the counter'th kind of
// tweet: of no media or links, of fakeMedia, of links, of links to images
// on imgur and twitpic, or of both.
func fakeEntities(counter int) (mediaKeys []string, urls []*urlEntity) {
	notPicURL := &urlEntity{
		URL:         `http://t.co/whatever`,
		ExpandedURL: `http://perkeep.org`,
		DisplayURL:  `perkeep.org`,
	}
	imgurURL := &urlEntity{
		URL:         `http://t.co/whatever2`,
		ExpandedURL: `http://imgur.com/foo`,
		DisplayURL:  `imgur.com/foo`,
	}
	twitpicURL := &urlEntity{
		URL:         `http://t.co/whatever3`,
		ExpandedURL: `http://twitpic.com/bar`,
		DisplayURL:  `twitpic.com/bar`,
	}
	switch counter {
	case 1:
		return []string{"3_1", "3_2"}, nil
	case 2:
		return nil, []*urlEntity{notPicURL}
	case 3:
		return nil, []*urlEntity{notPicURL, imgurURL}
	case 4:
		return nil, []*urlEntity{twitpicURL, imgurURL}
	case 5:
		return []string{"3_2", "3_1"}, []*urlEntity{notPicURL, twitpicURL}
	case 6:
		return []string{"3_1", "3_2"}, []*urlEntity{imgurURL, twitpicURL}
	}
	return nil, nil
}

func fakePicture() string {
	camliDir, err := osutil.PkSourceRoot()
	if errors.Is(err, os.ErrNotExist) {
		log.Fatal("Directory \"perkeep.org\" not found under GOPATH/src; are you not running with devcam?")
	}
	if err != nil {
		log.Fatalf("source root folder not found: %v", err)
	}
	return filepath.Join(camliDir, filepath.FromSlash("clients/web/embed/glitch/npc_piggy__x1_walk_png_1354829432.png"))
}
