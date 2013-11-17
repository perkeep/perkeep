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

package flickr

import (
	"encoding/json"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"camlistore.org/pkg/httputil"
	"camlistore.org/pkg/osutil"
	"camlistore.org/third_party/github.com/garyburd/go-oauth/oauth"
)

var (
	oauthClient = &oauth.Client{
		TemporaryCredentialRequestURI: "http://www.flickr.com/services/oauth/request_token",
		ResourceOwnerAuthorizationURI: "http://www.flickr.com/services/oauth/authorize",
		TokenRequestURI:               "http://www.flickr.com/services/oauth/access_token",
	}

	userFile = filepath.Join(osutil.CamliConfigDir(), "flickr-credentials.json")
)

// userInfo represents the Flickr user whose account we are interacting with.
// This struct is also serialized to <config-dir>/flickr-credentials.json.
// TODO(aa): Store this state within camlistore itself!
// TODO(aa): Support multiple instances of the importer per camlistore user.
type userInfo struct {
	Id   string             `json:"id"`
	Cred *oauth.Credentials `json:"creds"`
}

func writeCredentials(user *userInfo) {
	fi, err := os.Create(userFile)
	if err != nil {
		log.Printf("Error creating flickr credentials file: %s", err)
		return
	}

	if err = json.NewEncoder(fi).Encode(user); err != nil {
		log.Printf("Error writing flickr credentials: %s", err)
	}
}

func readCredentials() (*userInfo, error) {
	fi, err := os.Open(userFile)
	if err != nil {
		return nil, err
	}
	defer fi.Close()
	user := &userInfo{}
	err = json.NewDecoder(fi).Decode(user)
	if err != nil {
		return nil, err
	}
	return user, nil
}

func (im *imp) serveLogin(w http.ResponseWriter, r *http.Request) {
	if oauthClient.Credentials.Token == "" || oauthClient.Credentials.Secret == "" {
		w.Write([]byte("<h1>Bonk</h1>"))
		w.Write([]byte("<p>You need a Flickr API key to ride this attraction."))
		w.Write([]byte("<p><a href='http://www.flickr.com/services/apps/create/noncommercial/'>Get yours here</a> then modify the 'importer-flickr' key in your server-config.json file and restart your server."))
		return
	}

	callback := im.host.BaseURL + "callback"
	tempCred, err := oauthClient.RequestTemporaryCredentials(im.host.HTTPClient(), callback, nil)
	if err != nil {
		httputil.ForbiddenError(w, "Error getting temp cred: %s", err)
		return
	}
	writeCredentials(&userInfo{Cred: tempCred})
	authURL := oauthClient.AuthorizationURL(tempCred, url.Values{"perms": {"read"}})
	http.Redirect(w, r, authURL, 302)
}

func (im *imp) serveCallback(w http.ResponseWriter, r *http.Request) {
	tempUser, err := readCredentials()
	if err != nil {
		httputil.BadRequestError(w, err.Error())
		return
	}
	if tempUser.Cred.Token != r.FormValue("oauth_token") {
		httputil.ForbiddenError(w, "Unknown oauth_token.")
		return
	}
	tokenCred, form, err := oauthClient.RequestToken(im.host.HTTPClient(),
		tempUser.Cred, r.FormValue("oauth_verifier"))
	if err != nil {
		httputil.ForbiddenError(w, "Error getting request token: %s ", err)
		return
	}

	im.user = &userInfo{
		Id:   form.Get("user_nsid"),
		Cred: tokenCred,
	}
	writeCredentials(im.user)
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
