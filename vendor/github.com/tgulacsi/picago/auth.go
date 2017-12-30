// Copyright 2017 Tamás Gulácsi. All rights reserved.
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package picago

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"

	"golang.org/x/oauth2"
)

// Endpoint contains the URL of the picasa auth endpoints
var Endpoint = oauth2.Endpoint{
	AuthURL:  "https://accounts.google.com/o/oauth2/auth",
	TokenURL: "https://accounts.google.com/o/oauth2/token",
}

func Config(id, secret string) *oauth2.Config {
	return &oauth2.Config{
		ClientID:     id,
		ClientSecret: secret,
		Endpoint:     Endpoint,
		Scopes:       []string{PicasaScope},
	}
}

// OAuth2 scope, manage your Picasa account
const PicasaScope = "https://picasaweb.google.com/data/"

var ErrCodeNeeded = errors.New("Authorization code is needed")

// Authorize authorizes using OAuth2
// the ID and secret strings can be acquired from Google for the application
// https://developers.google.com/accounts/docs/OAuth2#basicsteps
func Authorize(ID, secret string) error {
	return errors.New("Not implemented")
}

// For redirect_uri, see https://developers.google.com/accounts/docs/OAuth2InstalledApp#choosingredirecturi .
//
// NewClient returns an authorized http.Client usable for requests.
func NewClient(ctx context.Context, id, secret, code, tokenCacheFilename string, Log func(...interface{}) error) (*http.Client, error) {
	cfg := Config(id, secret)
	var fc *FileCache
	if tokenCacheFilename != "" {
		var err error
		if fc, err = NewTokenCache(tokenCacheFilename, nil, Log); err != nil {
			return nil, err
		}
	}
	t, err := cfg.Exchange(ctx, code)
	if Log != nil {
		Log("msg", "Exchange", "code", code, "token", t, "error", err)
	}
	fc.SetTokenSource(cfg.TokenSource(ctx, t))
	if err == nil {
		return oauth2.NewClient(ctx, fc), nil
	}

	t, err = fc.Token()
	if Log != nil {
		Log("msg", "fc.Token", "token", t, "error", err)
	}
	if err == nil {
		return oauth2.NewClient(ctx, fc), nil
	}

	if id == "" || secret == "" {
		return nil, errors.New("client ID and secret is needed!")
	}

	l, err := getListener()
	if err != nil {
		return nil, err
	}
	cfg.RedirectURL = "http://" + l.Addr().String()

	donech := make(chan *oauth2.Token, 1)
	defer close(donech)
	// Get an authorization code from the data provider.
	// ("Please ask the user if I can access this resource.")
	url := cfg.AuthCodeURL("picago")
	fmt.Printf("Visit this URL to allow access to your Picasa data:\n\n")
	fmt.Println(url)

	srv := &http.Server{Handler: NewAuthorizeHandler(cfg, donech)}
	go srv.Serve(l)
	t = <-donech
	l.Close()

	if t == nil {
		return nil, ErrCodeNeeded
	}
	fc.SetTokenSource(cfg.TokenSource(ctx, t))
	return oauth2.NewClient(ctx, fc), nil
}

// NewAuthorizeHandler returns a http.HandlerFunc which will set the Token of
// the given oauth2.Transport and send a struct{} on the donech on success.
func NewAuthorizeHandler(cfg *oauth2.Config, donech chan<- *oauth2.Token) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token, err := cfg.Exchange(r.Context(), r.FormValue("code"))
		if err == nil && token != nil {
			donech <- token
			return
		}
		http.Error(w, fmt.Sprintf("error exchanging code: %v", err), http.StatusBadRequest)
	}
}

func getListener() (*net.TCPListener, error) {
	return net.ListenTCP("tcp", &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
}
