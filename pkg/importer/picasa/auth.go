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

// Package picasa is an importer for Picasa Web.
package picasa

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"

	"camlistore.org/pkg/httputil"
	"camlistore.org/third_party/code.google.com/p/goauth2/oauth"
)

const tokenAttrName = "picasaOAuth2Token"

// PutToken saves the token into the root node.
func (im *imp) PutToken(token *oauth.Token) error {
	root, err := im.getRootNode()
	if err != nil {
		return err
	}
	encoded, err := json.Marshal(token)
	if err != nil {
		return err
	}
	if err := root.SetAttr(tokenAttrName, string(encoded)); err != nil {
		return err
	}
	return nil
}

func (im *imp) Token() (*oauth.Token, error) {
	root, err := im.getRootNode()
	if err != nil {
		return nil, err
	}
	encoded := root.Attr(tokenAttrName)
	if encoded == "" {
		return nil, errors.New("No OAuth2 token")
	}
	token := &oauth.Token{}
	if err := json.Unmarshal([]byte(encoded), token); err != nil {
		return nil, err
	}
	return token, nil
}

func (im *imp) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if strings.HasSuffix(r.URL.Path, "/login") || strings.HasSuffix(r.URL.Path, "/start") {
		im.serveLogin(w, r)
	} else if strings.HasSuffix(r.URL.Path, "/callback") {
		im.serveCallback(w, r)
	} else {
		httputil.BadRequestError(w, "Unexpected path: %s", r.URL.Path)
	}
}

func (im *imp) serveLogin(w http.ResponseWriter, r *http.Request) {
	im.Lock()
	defer im.Unlock()
	token, err := im.Token()
	if err == nil {
		im.transport.Token = token
		http.Redirect(w, r, im.host.BaseURL+"?mode=start", 302)
		return
	}
	im.transport.Config.RedirectURL = im.host.BaseURL + "callback"
	authURL := im.transport.Config.AuthCodeURL("picago")
	http.Redirect(w, r, authURL, 302)
}

func (im *imp) serveCallback(w http.ResponseWriter, r *http.Request) {
	im.Lock()
	defer im.Unlock()
	token, err := im.transport.Exchange(r.FormValue("code"))
	if err != nil {
		http.Error(w, fmt.Sprintf("error exchanging code: %v", err), http.StatusBadRequest)
		return
	}
	im.transport.Token = token
	if err = im.PutToken(token); err != nil {
		log.Printf("Picasa Importer serveCallback: %v", err)
	}
	http.Redirect(w, r, im.host.BaseURL+"?mode=start", 302)
}
