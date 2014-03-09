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

// Package foursquare implements an importer for foursquare.com accounts.
package foursquare

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"camlistore.org/pkg/httputil"
	"camlistore.org/pkg/importer"
	"camlistore.org/pkg/jsonconfig"
	"camlistore.org/pkg/schema"
	"camlistore.org/third_party/code.google.com/p/goauth2/oauth"
)

const (
	apiURL = "https://api.foursquare.com/v2/"
)

func init() {
	importer.Register("foursquare", newFromConfig)
}

type imp struct {
	host *importer.Host

	oauthConfig *oauth.Config
	tokenCache  oauth.Cache

	mu   sync.Mutex
	user string
}

func newFromConfig(cfg jsonconfig.Obj, host *importer.Host) (importer.Importer, error) {
	apiKey := cfg.RequiredString("apiKey")
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	parts := strings.Split(apiKey, ":")
	if len(parts) != 2 {
		return nil, fmt.Errorf("Foursquare importer: Invalid apiKey configuration: %q", apiKey)
	}
	clientID, clientSecret := parts[0], parts[1]
	im := &imp{
		host:       host,
		tokenCache: &tokenCache{},
		oauthConfig: &oauth.Config{
			ClientId:     clientID,
			ClientSecret: clientSecret,
			AuthURL:      "https://foursquare.com/oauth2/authenticate",
			TokenURL:     "https://foursquare.com/oauth2/access_token",
			RedirectURL:  host.BaseURL + "callback",
		},
	}
	// TODO: schedule work?
	return im, nil
}

type tokenCache struct {
	mu    sync.Mutex
	token *oauth.Token
}

func (tc *tokenCache) Token() (*oauth.Token, error) {
	tc.mu.Lock()
	defer tc.mu.Unlock()
	if tc.token == nil {
		return nil, errors.New("no token")
	}
	return tc.token, nil
}

func (tc *tokenCache) PutToken(t *oauth.Token) error {
	tc.mu.Lock()
	defer tc.mu.Unlock()
	tc.token = t
	return nil

}
func (im *imp) CanHandleURL(url string) bool { return false }
func (im *imp) ImportURL(url string) error   { panic("unused") }

func (im *imp) Prefix() string {
	im.mu.Lock()
	defer im.mu.Unlock()
	if im.user == "" {
		// This should only get called when we're importing, but check anyway.
		panic("Prefix called before authenticated")
	}
	return fmt.Sprintf("foursquare:%s", im.user)
}

func (im *imp) String() string {
	im.mu.Lock()
	defer im.mu.Unlock()
	userId := "<unauthenticated>"
	if im.user != "" {
		userId = im.user
	}
	return fmt.Sprintf("foursquare:%s", userId)
}

func (im *imp) Run(intr importer.Interrupt) error {

	if err := im.importCheckins(); err != nil {
		return err
	}

	return nil
}

// structures from json

type userInfo struct {
	Response struct {
		User struct {
			Id string
		}
	}
}

type checkinsList struct {
	Response struct {
		Checkins struct {
			Items []*checkinItem
		}
	}
}

type checkinItem struct {
	Id        string
	CreatedAt int64 // unix time in seconds from 4sq
	Venue     venueItem
}

type venueItem struct {
	Id       string // eg 42474900f964a52087201fe3 from 4sq
	Name     string
	Location *venueLocationItem
}

type venueLocationItem struct {
	Address    string
	City       string
	PostalCode string
	State      string
	Country    string // 4sq provides "US"
	Lat        float64
	Lng        float64
}

// data import methods

func (im *imp) importCheckins() error {
	limit := 100
	offset := 0
	continueRequests := true

	for continueRequests {
		resp := checkinsList{}
		if err := im.doAPI(&resp, "users/self/checkins", "limit", strconv.Itoa(limit), "offset", strconv.Itoa(offset)); err != nil {
			return err
		}

		itemcount := len(resp.Response.Checkins.Items)
		log.Printf("Importing %d checkins", itemcount)
		if itemcount < 100 {
			continueRequests = false
		} else {
			offset += itemcount
		}

		checkinsNode, err := im.getTopLevelNode("checkins", "Checkins")
		if err != nil {
			return err
		}

		placesNode, err := im.getTopLevelNode("places", "Places")
		if err != nil {
			return err
		}

		for _, checkin := range resp.Response.Checkins.Items {
			err = im.importCheckin(checkinsNode, checkin)
			if err != nil {
				log.Printf("Foursquare importer: error importing checkin %s %v", checkin.Id, err)
				continue
			}

			err = im.importPlace(placesNode, &checkin.Venue)
			if err != nil {
				log.Printf("Foursquare importer: error importing place %s %v", checkin.Venue.Id, err)
				continue
			}
		}
	}

	return nil
}

func (im *imp) importCheckin(parent *importer.Object, checkin *checkinItem) error {
	checkinNode, err := parent.ChildPathObject(checkin.Id)
	if err != nil {
		return err
	}

	title := fmt.Sprintf("Checkin at %s", checkin.Venue.Name)

	if err := checkinNode.SetAttrs(
		"foursquareId", checkin.Id,
		"camliNodeType", "foursquare.com:checkin",
		"startDate", schema.RFC3339FromTime(time.Unix(checkin.CreatedAt, 0)),
		"title", title); err != nil {
		return err
	}

	return nil
}

func (im *imp) importPlace(parent *importer.Object, place *venueItem) error {
	placeNode, err := parent.ChildPathObject(place.Id)
	if err != nil {
		return err
	}

	if err := placeNode.SetAttrs(
		"foursquareId", place.Id,
		"camliNodeType", "foursquare.com:venue",
		"title", place.Name,
		"streetAddress", place.Location.Address,
		"addressLocality", place.Location.City,
		"postalCode", place.Location.PostalCode,
		"addressRegion", place.Location.State,
		"addressCountry", place.Location.Country,
		"latitude", fmt.Sprint(place.Location.Lat),
		"longitude", fmt.Sprint(place.Location.Lng)); err != nil {
		return err
	}

	return nil
}

func (im *imp) getTopLevelNode(path string, title string) (*importer.Object, error) {
	root, err := im.getRootNode()
	if err != nil {
		return nil, err
	}

	childObject, err := root.ChildPathObject(path)
	if err != nil {
		return nil, err
	}

	if err := childObject.SetAttr("title", title); err != nil {
		return nil, err
	}
	return childObject, nil
}

func (im *imp) getRootNode() (*importer.Object, error) {
	root, err := im.host.RootObject()
	if err != nil {
		return nil, err
	}

	if root.Attr("title") == "" {
		im.mu.Lock()
		user := im.user
		im.mu.Unlock()

		title := fmt.Sprintf("Foursquare (%s)", user)
		if err := root.SetAttr("title", title); err != nil {
			return nil, err
		}
	}
	return root, nil
}

func (im *imp) getUserId() (string, error) {
	user := userInfo{}

	if err := im.doAPI(&user, "users/self"); err != nil {
		return "", err
	}

	if user.Response.User.Id == "" {
		return "", fmt.Errorf("No username specified")
	}

	return user.Response.User.Id, nil
}

// foursquare api builders

func (im *imp) doAPI(result interface{}, apiPath string, keyval ...string) error {
	if len(keyval)%2 == 1 {
		panic("Incorrect number of keyval arguments")
	}

	token, err := im.tokenCache.Token()
	if err != nil {
		return fmt.Errorf("Token error: %v", err)
	}

	form := url.Values{}
	form.Set("v", "20140225") // 4sq requires this to version their API
	form.Set("oauth_token", token.AccessToken)
	for i := 0; i < len(keyval); i += 2 {
		form.Set(keyval[i], keyval[i+1])
	}

	fullURL := apiURL + apiPath
	res, err := im.doGet(fullURL, form)
	if err != nil {
		return err
	}
	err = httputil.DecodeJSON(res, result)
	if err != nil {
		log.Printf("Error parsing response for %s: %v", fullURL, err)
	}
	return err
}

func (im *imp) doGet(url string, form url.Values) (*http.Response, error) {
	requestURL := url + "?" + form.Encode()

	req, err := http.NewRequest("GET", requestURL, nil)
	if err != nil {
		return nil, err
	}

	res, err := im.host.HTTPClient().Do(req)
	if err != nil {
		log.Printf("Error fetching %s: %v", url, err)
		return nil, err
	}

	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Get request on %s failed with: %s", requestURL, res.Status)
	}

	return res, nil
}

// possibly common methods for accessing oauth2 sites

func (im *imp) serveLogin(w http.ResponseWriter, r *http.Request) {
	state := "no_clue_what_this_is" // TODO: ask adg to document this. or send him a CL.
	authURL := im.oauthConfig.AuthCodeURL(state)
	http.Redirect(w, r, authURL, 302)
}

func (im *imp) serveCallback(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "Expected a GET", 400)
		return
	}
	code := r.FormValue("code")
	if code == "" {
		http.Error(w, "Expected a code", 400)
		return
	}
	transport := &oauth.Transport{Config: im.oauthConfig}
	token, err := transport.Exchange(code)
	log.Printf("Token = %#v, error %v", token, err)
	if err != nil {
		log.Printf("Token Exchange error: %v", err)
		http.Error(w, "token exchange error", 500)
		return
	}
	im.tokenCache.PutToken(token)

	userid, err := im.getUserId()
	if err != nil {
		log.Printf("Couldn't get username: %v", err)
		http.Error(w, "can't get username", 500)
		return
	}
	im.user = userid

	http.Redirect(w, r, im.host.BaseURL+"?mode=start", 302)
}

func (im *imp) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if strings.HasSuffix(r.URL.Path, "/login") {
		im.serveLogin(w, r)
	} else if strings.HasSuffix(r.URL.Path, "/callback") {
		im.serveCallback(w, r)
	} else {
		httputil.BadRequestError(w, "Unknown path: %s", r.URL.Path)
	}
}
