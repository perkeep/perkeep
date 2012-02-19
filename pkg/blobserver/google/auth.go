/*
Copyright 2011 Google Inc.

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

package google

import (
	"time"

	"camlistore.org/third_party/code.google.com/p/goauth2/oauth"
)

const (
	Scope       = "https://www.googleapis.com/auth/devstorage.read_write"
	AuthURL     = "https://accounts.google.com/o/oauth2/auth"
	TokenURL    = "https://accounts.google.com/o/oauth2/token"
	RedirectURL = "urn:ietf:wg:oauth:2.0:oob"
)

func MakeOauthTransport(clientId string, clientSecret string, refreshToken string) *oauth.Transport {
	return &oauth.Transport{
		&oauth.Config{
			ClientId:     clientId,
			ClientSecret: clientSecret,
			Scope:        Scope,
			AuthURL:      AuthURL,
			TokenURL:     TokenURL,
			RedirectURL:  RedirectURL,
		},
		&oauth.Token{
			AccessToken:  "",
			RefreshToken: refreshToken,
			Expiry:       time.Time{}, // no expiry
		},
		nil,
	}
}
