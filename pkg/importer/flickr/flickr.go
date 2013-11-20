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
	"log"
	"net/http"
	"net/url"
	"strings"

	"camlistore.org/pkg/importer"
	"camlistore.org/pkg/jsonconfig"
	"camlistore.org/pkg/schema"
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
	apiKey := cfg.RequiredString("apiKey")
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	parts := strings.Split(apiKey, ":")
	if len(parts) != 2 {
		return nil, fmt.Errorf("Flickr importer: Invalid apiKey configuration: %q", apiKey)
	}
	oauthClient.Credentials = oauth.Credentials{
		Token:  parts[0],
		Secret: parts[1],
	}
	return &imp{
		host: host,
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
		Photo   []*photoMeta
	}

	Stat string
}

func (im *imp) Run(intr importer.Interrupt) error {
	resp := searchPhotosResult{}
	if err := im.flickrAPIRequest(url.Values{
		"method":  {"flickr.photos.search"},
		"user_id": {"me"},
		"extras":  {"description, date_upload, date_taken, original_format, last_update, geo, tags, machine_tags, views, media, url_o"}},
		&resp); err != nil {
		return err
	}

	photos, err := im.getPhotosNode()
	if err != nil {
		return err
	}
	log.Printf("Importing %d photos into permanode %s",
		len(resp.Photos.Photo), photos.PermanodeRef().String())

	for _, item := range resp.Photos.Photo {
		if err := im.importPhoto(photos, item); err != nil {
			log.Printf("Flickr importer: error importing %s: %s", item.Id, err)
			continue
		}
	}

	return nil
}

// TODO(aa):
// * Parallelize: http://golang.org/doc/effective_go.html#concurrency
// * Do more than one "page" worth of results
// * Report progress and errors back through host interface
// * All the rest of the metadata (see photoMeta)
// * Conflicts: For all metadata changes, prefer any non-imported claims
// * Test!
func (im *imp) importPhoto(parent *importer.Object, photo *photoMeta) error {
	filename := fmt.Sprintf("%s.%s", photo.Id, photo.Originalformat)
	photoNode, err := parent.ChildPathObject(filename)
	if err != nil {
		return err
	}

	// Import all the metadata. SetAttrs() is a no-op if the value hasn't changed, so there's no cost to doing these on every run.
	// And this way if we add more things to import, they will get picked up.
	if err := photoNode.SetAttrs(
		"flickrId", photo.Id,
		"title", photo.Title,
		"description", photo.Description.Content); err != nil {
		return err
	}

	// Import the photo itself. Since it is expensive to fetch the image, we store its lastupdate and only refetch if it might have changed.
	if photoNode.Attr("flickrLastupdate") == photo.Lastupdate {
		return nil
	}
	res, err := im.flickrRequest(photo.URL, url.Values{})
	if err != nil {
		log.Printf("Flickr importer: Could not fetch %s: %s", photo.URL, err)
		return err
	}
	defer res.Body.Close()

	fileRef, err := schema.WriteFileFromReader(im.host.Target(), filename, res.Body)
	if err != nil {
		return err
	}
	if err := photoNode.SetAttr("camliContent", fileRef.String()); err != nil {
		return err
	}
	// Write lastupdate last, so that if any of the preceding fails, we will try again next time.
	if err := photoNode.SetAttr("flickrLastupdate", photo.Lastupdate); err != nil {
		return err
	}

	return nil
}

func (im *imp) getPhotosNode() (*importer.Object, error) {
	root, err := im.getRootNode()
	if err != nil {
		return nil, err
	}

	photos, err := root.ChildPathObject("photos")
	if err != nil {
		return nil, err
	}

	if err := photos.SetAttr("title", "Photos"); err != nil {
		return nil, err
	}
	return photos, nil
}

func (im *imp) getRootNode() (*importer.Object, error) {
	root, err := im.host.RootObject()
	if err != nil {
		return nil, err
	}

	if root.Attr("title") == "" {
		title := fmt.Sprintf("Flickr (%s)", im.user.Username)
		if err := root.SetAttr("title", title); err != nil {
			return nil, err
		}
	}
	return root, nil
}

func (im *imp) flickrAPIRequest(form url.Values, result interface{}) error {
	form.Set("format", "json")
	form.Set("nojsoncallback", "1")
	res, err := im.flickrRequest(apiURL, form)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	return json.NewDecoder(res.Body).Decode(result)
}

func (im *imp) flickrRequest(url string, form url.Values) (*http.Response, error) {
	if im.user == nil {
		return nil, errors.New("Not logged in. Go to /importer-flickr/login.")
	}

	res, err := oauthClient.Get(im.host.HTTPClient(), im.user.Cred, url, form)
	if err != nil {
		return nil, err
	}

	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Auth request failed with: %s", res.Status)
	}

	return res, nil
}
