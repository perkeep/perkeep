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
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"

	"camlistore.org/pkg/importer"
	"camlistore.org/pkg/jsonconfig"
	"camlistore.org/third_party/github.com/garyburd/go-oauth/oauth"
)

const (
	apiURL = "http://api.flickr.com/services/rest/"
)

func init() {
	importer.Register("flickr", newFromConfig)
}

type imp struct {
	host *importer.Host
	user *userInfo // nil if the user isn't authenticated
}

func newFromConfig(cfg jsonconfig.Obj, host *importer.Host) (importer.Importer, error) {
	oauthClient.Credentials = oauth.Credentials{
		Token:  cfg.OptionalString("appKey", ""),
		Secret: cfg.OptionalString("appSecret", ""),
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	user, _ := readCredentials()
	return &imp{
		host: host,
		user: user,
	}, nil
}

func (im *imp) CanHandleURL(url string) bool { return false }
func (im *imp) ImportURL(url string) error   { panic("unused") }

func (im *imp) Prefix() string {
	// This should only get called when we're importing, so it's OK to
	// assume we're authenticated.
	return fmt.Sprintf("flickr:%s", im.user.Id)
}

func (im *imp) String() string {
	// We use this in logging when we're not authenticated, so it should do
	// something reasonable in that case.
	userId := "<unauthenticated>"
	if im.user != nil {
		userId = im.user.Id
	}
	return fmt.Sprintf("flickr:%s", userId)
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

func (im *imp) Run(intr importer.Interrupt) error {
	resp := searchPhotosResult{}
	if err := im.flickrRequest(url.Values{
		"method":  {"flickr.photos.search"},
		"user_id": {"me"},
		"extras":  {"description, date_upload, date_taken, original_format, last_update, geo, tags, machine_tags, views, media, url_o"}},
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

func (im *imp) flickrRequest(form url.Values, result interface{}) error {
	if im.user == nil {
		return errors.New("Not logged in. Go to /importer-flickr/login.")
	}

	form.Set("format", "json")
	form.Set("nojsoncallback", "1")
	res, err := oauthClient.Get(im.host.HTTPClient(), im.user.Cred, apiURL, form)
	if err != nil {
		return err
	}

	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("Auth request failed with: %s", res.Status)
	}

	defer res.Body.Close()
	return json.NewDecoder(res.Body).Decode(result)
}
