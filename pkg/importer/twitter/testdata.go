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

package twitter

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"camlistore.org/pkg/httputil"
	"camlistore.org/pkg/importer"
	"camlistore.org/pkg/osutil"
)

var _ importer.TestDataMaker = (*imp)(nil)

func (im *imp) SetTestAccount(acctNode *importer.Object) error {
	return acctNode.SetAttrs(
		importer.AcctAttrAccessToken, "fakeAccessToken",
		importer.AcctAttrAccessTokenSecret, "fakeAccessSecret",
		importer.AcctAttrUserID, "fakeUserID",
		importer.AcctAttrName, "fakeName",
		importer.AcctAttrUserName, "fakeScreenName",
	)
}

func (im *imp) MakeTestData() http.RoundTripper {
	const (
		fakeMaxId = int64(486450108201201664) // Most recent tweet.
		nTweets   = 300                       // Arbitrary number of tweets generated.
	)
	fakeMinId := fakeMaxId - nTweets // Oldest tweet in our timeline.

	timeLineURL := apiURL + userTimeLineAPIPath
	timeLineCached := make(map[int64]string)
	okHeader := `HTTP/1.1 200 OK
Content-Type: application/json; charset=UTF-8

`
	timeLineResponse := okHeader + fakeTimeLine(fakeMaxId, fakeMinId, timeLineCached)

	fakePic := fakePicture()
	responses := map[string]func() *http.Response{
		timeLineURL: httputil.StaticResponder(timeLineResponse),
		fmt.Sprintf("%s?count=%d&user_id=fakeUserID", timeLineURL, tweetRequestLimit): httputil.StaticResponder(timeLineResponse),
		"https://twitpic.com/show/large/bar":                                          httputil.FileResponder(fakePic),
		"https://i.imgur.com/foo.gif":                                                 httputil.FileResponder(fakePic),
	}

	// register all the user_timeline calls (max_id varies) that should occur,
	responses[fmt.Sprintf("%s?count=%d&max_id=%d&user_id=fakeUserID", timeLineURL, tweetRequestLimit, fakeMaxId-nTweets+1)] = httputil.StaticResponder(okHeader + fakeTimeLine(fakeMaxId-nTweets+1, fakeMinId, timeLineCached))
	if nTweets > tweetRequestLimit {
		// that is, once every tweetRequestLimit-1, going down from fakeMaxId.
		for i := fakeMaxId; i > fakeMinId; i -= tweetRequestLimit - 1 {
			responses[fmt.Sprintf("%s?count=%d&max_id=%d&user_id=fakeUserID", timeLineURL, tweetRequestLimit, i)] = httputil.StaticResponder(okHeader + fakeTimeLine(i, fakeMinId, timeLineCached))
		}
	}

	// register all the possible combinations of media twimg
	for _, scheme := range []string{"http://", "https://"} {
		for _, picsize := range []string{"thumb", "small", "medium", "large"} {
			responses[fmt.Sprintf("%spbs.twimg.com/media/foo.jpg:%s", scheme, picsize)] = httputil.FileResponder(fakePic)
			responses[fmt.Sprintf("%spbs.twimg.com/media/bar.png:%s", scheme, picsize)] = httputil.FileResponder(fakePic)
		}
	}

	return httputil.NewFakeTransport(responses)
}

// fakeTimeLine returns a JSON user timeline of tweetRequestLimit tweets, starting
// with maxId as the most recent tweet id. It stops before tweetRequestLimit if
// minId is reached. The returned timeline is saved in cached.
func fakeTimeLine(maxId, minId int64, cached map[int64]string) string {
	if tl, ok := cached[maxId]; ok {
		return tl
	}
	min := maxId - int64(tweetRequestLimit)
	if min <= minId {
		min = minId
	}
	var tweets []*apiTweetItem
	entitiesCounter := 0
	geoCounter := 0
	for i := maxId; i > min; i-- {
		tweet := &apiTweetItem{
			Id:           strconv.FormatInt(i, 10),
			TextStr:      fmt.Sprintf("fakeText %d", i),
			CreatedAtStr: time.Now().Format(time.RubyDate),
			Entities:     fakeEntities(entitiesCounter),
		}
		geo, coords := fakeGeo(geoCounter)
		tweet.Geo = geo
		tweet.Coordinates = coords
		tweets = append(tweets, tweet)
		entitiesCounter++
		geoCounter++
		if entitiesCounter == 10 {
			entitiesCounter = 0
		}
		if geoCounter == 5 {
			geoCounter = 0
		}
	}
	userTimeLine, err := json.MarshalIndent(tweets, "", "	")
	if err != nil {
		log.Fatalf("%v", err)
	}
	cached[maxId] = string(userTimeLine)
	return cached[maxId]
}

func fakeGeo(counter int) (*geo, *coords) {
	sf := []float64{37.7447124, -122.4341914}
	gre := []float64{45.1822842, 5.7141854}
	switch counter {
	case 0:
		return nil, nil
	case 1:
		return &geo{sf}, nil
	case 2:
		return nil, &coords{[]float64{gre[1], gre[0]}}
	case 3:
		return &geo{gre}, &coords{[]float64{sf[1], sf[0]}}
	default:
		return nil, nil
	}
}

func fakeEntities(counter int) entities {
	sizes := func() map[string]mediaSize {
		return map[string]mediaSize{
			"medium": {W: 591, H: 332, Resize: "fit"},
			"large":  {W: 591, H: 332, Resize: "fit"},
			"small":  {W: 338, H: 190, Resize: "fit"},
			"thumb":  {W: 150, H: 150, Resize: "crop"},
		}
	}
	mediaTwimg1 := func() *media {
		return &media{
			Id:            "1",
			IdNum:         1,
			MediaURL:      `http://pbs.twimg.com/media/foo.jpg`,
			MediaURLHTTPS: `https://pbs.twimg.com/media/foo.jpg`,
			Sizes:         sizes(),
		}
	}
	mediaTwimg2 := func() *media {
		return &media{
			Id:            "2",
			IdNum:         2,
			MediaURL:      `http://pbs.twimg.com/media/bar.png`,
			MediaURLHTTPS: `https://pbs.twimg.com/media/bar.png`,
			Sizes:         sizes(),
		}
	}
	notPicURL := func() *urlEntity {
		return &urlEntity{
			URL:         `http://t.co/whatever`,
			ExpandedURL: `http://camlistore.org`,
			DisplayURL:  `camlistore.org`,
		}
	}
	imgurURL := func() *urlEntity {
		return &urlEntity{
			URL:         `http://t.co/whatever2`,
			ExpandedURL: `http://imgur.com/foo`,
			DisplayURL:  `imgur.com/foo`,
		}
	}
	twitpicURL := func() *urlEntity {
		return &urlEntity{
			URL:         `http://t.co/whatever3`,
			ExpandedURL: `http://twitpic.com/bar`,
			DisplayURL:  `twitpic.com/bar`,
		}
	}

	// if you add another case, make sure the entities counter reset
	// in fakeTimeLine allows for that case to happen.
	// We could use global vars instead, but don't want to pollute the
	// twitter pkg namespace.
	switch counter {
	case 0:
		return entities{}
	case 1:
		return entities{
			Media: []*media{
				mediaTwimg1(),
				mediaTwimg2(),
			},
		}
	case 2:
		return entities{
			URLs: []*urlEntity{
				notPicURL(),
			},
		}
	case 3:
		return entities{
			URLs: []*urlEntity{
				notPicURL(),
				imgurURL(),
			},
		}
	case 4:
		return entities{
			URLs: []*urlEntity{
				twitpicURL(),
				imgurURL(),
			},
		}
	case 5:
		return entities{
			Media: []*media{
				mediaTwimg2(),
				mediaTwimg1(),
			},
			URLs: []*urlEntity{
				notPicURL(),
				twitpicURL(),
			},
		}
	case 6:
		return entities{
			Media: []*media{
				mediaTwimg1(),
				mediaTwimg2(),
			},
			URLs: []*urlEntity{
				imgurURL(),
				twitpicURL(),
			},
		}
	default:
		return entities{}
	}
}

func fakePicture() string {
	camliDir, err := osutil.GoPackagePath("camlistore.org")
	if err == os.ErrNotExist {
		log.Fatal("Directory \"camlistore.org\" not found under GOPATH/src; are you not running with devcam?")
	}
	if err != nil {
		log.Fatalf("Error searching for \"camlistore.org\" under GOPATH: %v", err)
	}
	return filepath.Join(camliDir, filepath.FromSlash("third_party/glitch/npc_piggy__x1_walk_png_1354829432.png"))
}
