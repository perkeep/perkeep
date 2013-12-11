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
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"camlistore.org/pkg/httputil"
	"camlistore.org/third_party/github.com/garyburd/go-oauth/oauth"
)

var (
	oauthClient = &oauth.Client{
		TemporaryCredentialRequestURI: "http://www.flickr.com/services/oauth/request_token",
		ResourceOwnerAuthorizationURI: "http://www.flickr.com/services/oauth/authorize",
		TokenRequestURI:               "http://www.flickr.com/services/oauth/access_token",
	}
)

type userInfo struct {
	Id       string
	Username string
	Cred     *oauth.Credentials
}

func (u *userInfo) Valid() error {
	if u.Id != "" || u.Username != "" || u.Cred.Token != "" || u.Cred.Secret != "" {
		return fmt.Errorf("Flickr importer: Invalid user: %v", u)
	}
	return nil
}

func (im *imp) writeCredentials() error {
	root, err := im.getRootNode()
	if err != nil {
		return err
	}
	if err := root.SetAttrs(
		"flickrUserId", im.user.Id,
		"flickrUsername", im.user.Username,
		"flickrToken", im.user.Cred.Token,
		"flickrSecret", im.user.Cred.Secret); err != nil {
		return err
	}
	return nil
}

func (im *imp) readCredentials() error {
	root, err := im.getRootNode()
	if err != nil {
		return err
	}

	u := &userInfo{
		Id:       root.Attr("flickrUserId"),
		Username: root.Attr("flickrUsername"),
		Cred: &oauth.Credentials{
			Token:  root.Attr("flickrToken"),
			Secret: root.Attr("flickrSecret"),
		},
	}
	if err := u.Valid(); err != nil {
		return err
	}

	im.user = u
	return nil
}

func (im *imp) serveLogin(w http.ResponseWriter, r *http.Request) {
	callback := im.host.BaseURL + "callback"
	tempCred, err := oauthClient.RequestTemporaryCredentials(im.host.HTTPClient(), callback, nil)
	if err != nil {
		httputil.ServeError(w, r, fmt.Errorf("Flickr importer: Error getting temp cred: %s", err))
		return
	}

	// TODO(aa): If we ever have multiple frontends running this code, storing this temporary state here won't work.
	im.user = &userInfo{Cred: tempCred}

	authURL := oauthClient.AuthorizationURL(im.user.Cred, url.Values{"perms": {"read"}})
	http.Redirect(w, r, authURL, 302)
}

func (im *imp) serveCallback(w http.ResponseWriter, r *http.Request) {
	if im.user == nil {
		httputil.BadRequestError(w, "Flickr importer: unexpected state: expected temporary oauth session")
		return
	}
	if im.user.Cred.Token != r.FormValue("oauth_token") {
		httputil.BadRequestError(w, "Flickr importer: unexpected oauth_token")
		return
	}
	tokenCred, form, err := oauthClient.RequestToken(im.host.HTTPClient(), im.user.Cred, r.FormValue("oauth_verifier"))
	if err != nil {
		httputil.ServeError(w, r, fmt.Errorf("Flickr importer: error getting request token: %s ", err))
		return
	}

	im.user = &userInfo{
		Id:       form.Get("user_nsid"),
		Username: form.Get("username"),
		Cred:     tokenCred,
	}
	im.writeCredentials()
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
