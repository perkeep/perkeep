// Copyright 2014 Tamás Gulácsi. All rights reserved.
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package picago

import (
	"errors"
	"fmt"
	"net"
	"net/http"

	"camlistore.org/third_party/code.google.com/p/goauth2/oauth"
)

const picasaScope = "https://picasaweb.google.com/data/"

var ErrCodeNeeded = errors.New("Authorization code is needed")

// Authorize authorizes using OAuth2
// the ID and secret strings can be acquired from Google for the application
// https://developers.google.com/accounts/docs/OAuth2#basicsteps
func Authorize(ID, secret string) error {
	return errors.New("Not implemented")
}

// NewClient returns an authorized http.Client usable for requests,
// caching tokens in the given file.
func NewClient(id, secret, code, tokenCacheFilename string) (*http.Client, error) {
	return NewClientCache(id, secret, code, oauth.CacheFile(tokenCacheFilename))
}

// For redirect_uri, see https://developers.google.com/accounts/docs/OAuth2InstalledApp#choosingredirecturi .
//
// NewClientCache returns an authorized http.Client with the given oauth.Cache implementation
func NewClientCache(id, secret, code string, cache oauth.Cache) (*http.Client, error) {
	transport, err := NewTransport(id, secret, cache)
	if err != nil {
		return nil, err
	}

	// Try to pull the token from the cache; if this fails, we need to get one.
	token, err := transport.Config.TokenCache.Token()
	if err == nil {
		transport.Token = token
	} else {
		if id == "" || secret == "" {
			return nil, errors.New("token cache is empty, thus client ID and secret is needed!")
		}
		if code == "" {
			l, err := getListener()
			if err != nil {
				return nil, err
			}
			donech := make(chan struct{}, 1)
			transport.Config.RedirectURL = "http://" + l.Addr().String()
			// Get an authorization code from the data provider.
			// ("Please ask the user if I can access this resource.")
			url := transport.Config.AuthCodeURL("picago")
			fmt.Println("Visit this URL to get a code, then run again with code=YOUR_CODE\n")
			fmt.Println(url)

			srv := &http.Server{Handler: NewAuthorizeHandler(transport, donech)}
			go srv.Serve(l)
			<-donech
			l.Close()

			if transport.Token == nil {
				return nil, ErrCodeNeeded
			}
		}
		if transport.Token == nil {
			// Exchange the authorization code for an access token.
			// ("Here's the code you gave the user, now give me a token!")
			transport.Token, err = transport.Exchange(code)
			if err != nil {
				return nil, fmt.Errorf("Exchange: %v", err)
			}
		}
	}
	return &http.Client{Transport: transport}, nil
}

func NewTransport(id, secret string, cache oauth.Cache) (*oauth.Transport, error) {
	if id == "" || secret == "" {
		return nil, errors.New("Client ID and secret is needed!")
	}
	config := &oauth.Config{
		ClientId:     id,
		ClientSecret: secret,
		AuthURL:      "https://accounts.google.com/o/oauth2/auth",
		TokenURL:     "https://accounts.google.com/o/oauth2/token",
		Scope:        picasaScope,
		TokenCache:   cache,
	}
	return &oauth.Transport{Config: config}, nil
}

// NewAuthorizeHandler returns a http.HandlerFunc which will set the Token of
// the given oauth.Transport and send a struct{} on the donech on success.
func NewAuthorizeHandler(transport *oauth.Transport, donech chan<- struct{}) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token, err := transport.Exchange(r.FormValue("code"))
		if err != nil {
			http.Error(w, fmt.Sprintf("error exchanging code: %v", err), http.StatusBadRequest)
			return
		}
		transport.Token = token
		// The Transport now has a valid Token.
		donech <- struct{}{}
	}
}

func getListener() (*net.TCPListener, error) {
	return net.ListenTCP("tcp", &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
}
