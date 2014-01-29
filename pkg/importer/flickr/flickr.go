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
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"

	"camlistore.org/pkg/httputil"
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

func (im *imp) Run(intr importer.Interrupt) error {
	if err := im.importPhotosets(); err != nil {
		return err
	}
	if err := im.importPhotos(); err != nil {
		return err
	}
	return nil
}

type photosetsGetList struct {
	Photosets struct {
		Page     int
		Pages    int
		Perpage  int
		Photoset []*photosetsGetListItem
	}
}

type photosetsGetListItem struct {
	Id             string
	PrimaryPhotoId string `json:"primary"`
	Title          contentString
	Description    contentString
}

type photosetsGetPhotos struct {
	Photoset struct {
		Id    string
		Page  int `json:",string"`
		Pages int
		Photo []struct {
			Id             string
			Originalformat string
		}
	}
}

func (im *imp) importPhotosets() error {
	resp := photosetsGetList{}
	if err := im.flickrAPIRequest(&resp, "flickr.photosets.getList"); err != nil {
		return err
	}

	setsNode, err := im.getTopLevelNode("sets", "Sets")
	if err != nil {
		return err
	}
	log.Printf("Importing %d sets", len(resp.Photosets.Photoset))

	for _, item := range resp.Photosets.Photoset {
		for page := 1; page >= 1; {
			page, err = im.importPhotoset(setsNode, item, page)
			if err != nil {
				log.Printf("Flickr importer: error importing photoset %s: %s", item.Id, err)
				continue
			}
		}
	}
	return nil
}

func (im *imp) importPhotoset(parent *importer.Object, photoset *photosetsGetListItem, page int) (int, error) {
	photosetNode, err := parent.ChildPathObject(photoset.Id)
	if err != nil {
		return 0, err
	}

	if err := photosetNode.SetAttrs(
		"flickrId", photoset.Title.Content,
		"title", photoset.Title.Content,
		"description", photoset.Description.Content,
		"primaryPhotoId", photoset.PrimaryPhotoId); err != nil {
		return 0, err
	}

	resp := photosetsGetPhotos{}
	if err := im.flickrAPIRequest(&resp, "flickr.photosets.getPhotos",
		"page", fmt.Sprintf("%d", page), "photoset_id", photoset.Id, "extras", "original_format"); err != nil {
		return 0, err
	}

	log.Printf("Importing page %d from photoset %s", page, photoset.Id)

	photosNode, err := im.getPhotosNode()
	if err != nil {
		return 0, err
	}

	for _, item := range resp.Photoset.Photo {
		filename := fmt.Sprintf("%s.%s", item.Id, item.Originalformat)
		photoNode, err := photosNode.ChildPathObject(filename)
		if err != nil {
			log.Printf("Flickr importer: error finding photo node %s for addition to photoset %s: %s",
				item.Id, photoset.Id, err)
			continue
		}
		if err := photosetNode.SetAttr("camliPath:"+filename, photoNode.PermanodeRef().String()); err != nil {
			log.Printf("Flickr importer: error adding photo %s to photoset %s: %s",
				item.Id, photoset.Id, err)
		}
	}

	if resp.Photoset.Page < resp.Photoset.Pages {
		return page + 1, nil
	} else {
		return 0, nil
	}
}

type photosSearch struct {
	Photos struct {
		Page    int
		Pages   int
		Perpage int
		Total   int `json:",string"`
		Photo   []*photosSearchItem
	}

	Stat string
}

type photosSearchItem struct {
	Id             string
	Title          string
	Ispublic       int
	Isfriend       int
	Isfamily       int
	Description    contentString
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

func (im *imp) importPhotos() error {
	for page := 1; page >= 1; {
		var err error
		page, err = im.importPhotosPage(page)
		if err != nil {
			return err
		}
	}
	return nil
}

func (im *imp) importPhotosPage(page int) (int, error) {
	resp := photosSearch{}
	if err := im.flickrAPIRequest(&resp, "flickr.people.getPhotos", "page", fmt.Sprintf("%d", page),
		"extras", "description, date_upload, date_taken, original_format, last_update, geo, tags, machine_tags, views, media, url_o"); err != nil {
		return 0, err
	}

	photosNode, err := im.getPhotosNode()
	if err != nil {
		return 0, err
	}
	log.Printf("Importing %d photos on page %d of %d", len(resp.Photos.Photo), page, resp.Photos.Pages)

	for _, item := range resp.Photos.Photo {
		if err := im.importPhoto(photosNode, item); err != nil {
			log.Printf("Flickr importer: error importing %s: %s", item.Id, err)
			continue
		}
	}

	if resp.Photos.Pages > resp.Photos.Page {
		return page + 1, nil
	} else {
		return 0, nil
	}
}

// TODO(aa):
// * Parallelize: http://golang.org/doc/effective_go.html#concurrency
// * Do more than one "page" worth of results
// * Report progress and errors back through host interface
// * All the rest of the metadata (see photoMeta)
// * Conflicts: For all metadata changes, prefer any non-imported claims
// * Test!
func (im *imp) importPhoto(parent *importer.Object, photo *photosSearchItem) error {
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
	return im.getTopLevelNode("photos", "Photos")
}

func (im *imp) getTopLevelNode(path string, title string) (*importer.Object, error) {
	root, err := im.getRootNode()
	if err != nil {
		return nil, err
	}

	photos, err := root.ChildPathObject(path)
	if err != nil {
		return nil, err
	}

	if err := photos.SetAttr("title", title); err != nil {
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

func (im *imp) flickrAPIRequest(result interface{}, method string, keyval ...string) error {
	if len(keyval)%2 == 1 {
		panic("Incorrect number of keyval arguments")
	}

	if im.user == nil {
		return fmt.Errorf("No authenticated user")
	}

	form := url.Values{}
	form.Set("method", method)
	form.Set("format", "json")
	form.Set("nojsoncallback", "1")
	form.Set("user_id", im.user.Id)
	for i := 0; i < len(keyval); i += 2 {
		form.Set(keyval[i], keyval[i+1])
	}

	res, err := im.flickrRequest(apiURL, form)
	if err != nil {
		return err
	}
	err = httputil.DecodeJSON(res, result)
	if err != nil {
		log.Printf("Error parsing response for %s: %s", apiURL, err)
	}
	return err
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

type contentString struct {
	Content string `json:"_content"`
}
