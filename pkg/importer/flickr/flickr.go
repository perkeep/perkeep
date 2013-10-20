/*
Copyright 2013 The Camlistore Authors

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

// Package flickr implements an importer for flickr.com accounts.
package flickr

import (
	"crypto/md5"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"camlistore.org/pkg/importer"
	"camlistore.org/pkg/jsonconfig"
)

const (
	authURL   = "http://www.flickr.com/auth-72157636676651636"
	apiURL    = "http://api.flickr.com/services/rest/"
	apiSecret = "6ed517d5f44946c9"
	apiKey    = "b5801cdbc870073e7b136f24fb50396f"
)

func init() {
	importer.Register("flickr", newFromConfig)
}

type imp struct {
	authToken string
	userId    string
}

func newFromConfig(cfg jsonconfig.Obj) (importer.Importer, error) {
	// TODO(aa): miniToken config is temporary. There should be UI to auth using oauth.
	miniToken := cfg.RequiredString("miniToken")
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	im := &imp{}
	if miniToken != "" {
		if err := im.authenticate(http.DefaultClient, miniToken); err != nil {
			return nil, err
		}
	}
	return im, nil
}

func (im *imp) CanHandleURL(url string) bool { return false }
func (im *imp) ImportURL(url string) error   { panic("unused") }

func (im *imp) Prefix() string {
	return fmt.Sprintf("flickr:%s", im.userId)
}

type photoMeta struct {
	Id          string
	Title       string
	Ispublic    int
	Isfriend    int
	Isfamily    int
	Description struct {
		Content string `json:"_content"`
	}
	Dateupload     string
	Datetaken      string
	Originalformat string
	Lastupdate     string
	Latitude       float32
	Longitude      float32
	Tags           string
	Machinetags    string `json:"machine_tags"`
	Views          string
	Media          string
	URL            string `json:"url_o"`
}

type searchPhotosResult struct {
	Photos struct {
		Page    int
		Pages   int
		Perpage int
		Total   int `json:",string"`
		Photo   []photoMeta
	}

	Stat string
}

func (im *imp) Run(h *importer.Host, intr importer.Interrupt) error {
	if im.authToken == "" {
		return fmt.Errorf("miniToken config key required. Go to %s to get one.", authURL)
	}

	resp := searchPhotosResult{}
	if err := im.flickrRequest(h.HTTPClient(), map[string]string{
		"method":  "flickr.photos.search",
		"user_id": "me",
		"extras":  "description, date_upload, date_taken, original_format, last_update, geo, tags, machine_tags, views, media, url_o"},
		&resp); err != nil {
		return err
	}

	for _, item := range resp.Photos.Photo {
		camliIdFramgment := fmt.Sprintf("photo-%s", item.Id)
		photoContentHint := item.Lastupdate
		fmt.Println(camliIdFramgment, photoContentHint)
		// TODO(aa): Stuff
	}

	return nil
}

type getFullAuthTokenResp struct {
	Auth struct {
		Token struct {
			Content string `json:"_content"`
		}
		User struct {
			Nsid string
		}
	}
	Stat string
}

func (im *imp) authenticate(httpClient *http.Client, miniToken string) error {
	resp := getFullAuthTokenResp{}
	if err := im.flickrRequest(httpClient, map[string]string{
		"method":     "flickr.auth.getFullToken",
		"mini_token": miniToken}, &resp); err != nil {
		return err
	}
	im.userId = resp.Auth.User.Nsid
	im.authToken = resp.Auth.Token.Content
	return nil
}

func (im *imp) flickrRequest(httpClient *http.Client, params map[string]string, result interface{}) error {
	params["api_key"] = apiKey
	params["format"] = "json"
	params["nojsoncallback"] = "1"

	if im.authToken != "" {
		params["auth_token"] = im.authToken
	}

	paramList := make([]string, 0, len(params))
	for key, val := range params {
		paramList = append(paramList, key+val)
	}
	sort.Strings(paramList)

	hash := md5.New()
	body := apiSecret + strings.Join(paramList, "")
	io.WriteString(hash, body)
	digest := hash.Sum(nil)

	reqURL, _ := url.Parse(apiURL)
	q := reqURL.Query()
	for key, val := range params {
		q.Set(key, val)
	}
	q.Set("api_sig", fmt.Sprintf("%x", digest))
	reqURL.RawQuery = q.Encode()

	res, err := httpClient.Get(reqURL.String())
	if err != nil {
		return err
	}

	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("Auth request failed with: %s", res.Status)
	}

	defer res.Body.Close()
	return json.NewDecoder(res.Body).Decode(result)
}
