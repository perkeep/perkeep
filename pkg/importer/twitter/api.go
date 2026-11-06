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

package twitter

import (
	"fmt"
	"html"
	"path"
)

// The X API v2, which serves an account's own tweets and likes. Its v1.1
// timeline and likes endpoints are gone.
const (
	apiV2URL = "https://api.x.com/2/"

	// apiPageSize is the most tweets the user tweets and liked tweets
	// endpoints return a page.
	apiPageSize = 100

	// The fields of the tweets and media to get. Media are of the
	// "includes" of a page, by their keys.
	apiTweetFields = "created_at,entities,attachments,geo,note_tweet"
	apiExpansions  = "attachments.media_keys"
	apiMediaFields = "url,preview_image_url,type"
)

// apiTweetsPath returns the API v2 path of the tweets of userID, or of the
// tweets it liked.
func apiTweetsPath(userID string, likes bool) string {
	if likes {
		return fmt.Sprintf("users/%s/liked_tweets", userID)
	}
	return fmt.Sprintf("users/%s/tweets", userID)
}

// apiTweetsPage is a page of the user tweets or liked tweets endpoints.
type apiTweetsPage struct {
	Data     []*apiTweet `json:"data"`
	Includes struct {
		Media []*apiMedia `json:"media"`
	} `json:"includes"`
	Meta struct {
		ResultCount int    `json:"result_count"`
		NewestID    string `json:"newest_id"`
		NextToken   string `json:"next_token"`
	} `json:"meta"`
}

// tweets returns the page's tweets, with their media, as liked tweets if
// liked.
func (p *apiTweetsPage) tweets(liked bool) []*apiTweet {
	media := make(map[string]*apiMedia)
	for _, m := range p.Includes.Media {
		media[m.MediaKey] = m
	}
	for _, t := range p.Data {
		t.liked = liked
		for _, k := range t.Attachments.MediaKeys {
			if m, ok := media[k]; ok && m.baseURL() != "" {
				t.media = append(t.media, m)
			}
		}
	}
	return p.Data
}

// apiTweet is a tweet of the API v2.
type apiTweet struct {
	Id           string `json:"id"`
	TextStr      string `json:"text"`
	CreatedAtStr string `json:"created_at"` // RFC 3339
	NoteTweet    *struct {
		Text string `json:"text"`
	} `json:"note_tweet"` // of a tweet longer than 280 characters, its whole text
	Attachments struct {
		MediaKeys []string `json:"media_keys"`
	} `json:"attachments"`
	Entities struct {
		URLs []*urlEntity `json:"urls"`
	} `json:"entities"`
	Geo *struct {
		Coordinates *coords `json:"coordinates"` // only if the tweet has an exact location
	} `json:"geo"`

	liked bool
	media []tweetMedia
}

func (t *apiTweet) ID() string        { return t.Id }
func (t *apiTweet) CreatedAt() string { return t.CreatedAtStr }
func (t *apiTweet) Liked() bool       { return t.liked }

func (t *apiTweet) Text() string {
	if t.NoteTweet != nil && t.NoteTweet.Text != "" {
		return html.UnescapeString(t.NoteTweet.Text)
	}
	return html.UnescapeString(t.TextStr)
}

func (t *apiTweet) LatLong() (lat, long float64, ok bool) {
	if t.Geo == nil {
		return
	}
	return latLong(nil, t.Geo.Coordinates)
}

func (t *apiTweet) Media() []tweetMedia {
	return append(t.media[:len(t.media):len(t.media)], getImagesFromURLs(t.Entities.URLs)...)
}

// apiMedia is a photo, video or GIF of the API v2. A video's or GIF's is
// its preview image.
type apiMedia struct {
	MediaKey        string `json:"media_key"`
	Type            string `json:"type"`
	URL             string `json:"url"`               // of a photo
	PreviewImageURL string `json:"preview_image_url"` // of a video or GIF
}

func (m *apiMedia) baseURL() string {
	if m.URL != "" {
		return m.URL
	}
	return m.PreviewImageURL
}

func (m *apiMedia) URLs() []string {
	u := m.baseURL()
	return []string{u + ":orig", u + ":large", u}
}

func (m *apiMedia) BaseFilename() string { return path.Base(m.baseURL()) }
