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
	"io/ioutil"
	"log"
	"net/http"
	"strings"
	"sync"

	"camlistore.org/pkg/httputil"
	"camlistore.org/pkg/importer"
	"camlistore.org/pkg/jsonconfig"
	"camlistore.org/third_party/code.google.com/p/goauth2/oauth"
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
	token, err := im.tokenCache.Token()
	if err != nil {
		return fmt.Errorf("Foursquare importer can't run. Token error: %v", err)
	}

	res, err := im.host.HTTPClient().Get("https://api.foursquare.com/v2/users/self?oauth_token=" + token.AccessToken)
	if err != nil {
		log.Printf("Error fetching //api.foursquare.com/v2/users/self: %v", err)
		return err
	}
	all, _ := ioutil.ReadAll(res.Body)
	res.Body.Close()
	log.Printf("Got: %s", all)
	return nil
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
