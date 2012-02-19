// Copyright 2011 The goauth2 Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// The oauth package provides support for making
// OAuth2-authenticated HTTP requests.
//
// Example usage:
//
//	// Specify your configuration. (typically as a global variable)
//	var config = &oauth.Config{
//		ClientId:     YOUR_CLIENT_ID,
//		ClientSecret: YOUR_CLIENT_SECRET,
//		Scope:        "https://www.googleapis.com/auth/buzz",
//		AuthURL:      "https://accounts.google.com/o/oauth2/auth",
//		TokenURL:     "https://accounts.google.com/o/oauth2/token",
//		RedirectURL:  "http://you.example.org/handler",
//	}
//
//	// A landing page redirects to the OAuth provider to get the auth code.
//	func landing(w http.ResponseWriter, r *http.Request) {
//		http.Redirect(w, r, config.AuthCodeURL("foo"), http.StatusFound)
//	}
//
//	// The user will be redirected back to this handler, that takes the
//	// "code" query parameter and Exchanges it for an access token.
//	func handler(w http.ResponseWriter, r *http.Request) {
//		t := &oauth.Transport{Config: config}
//		t.Exchange(r.FormValue("code"))
//		// The Transport now has a valid Token. Create an *http.Client
//		// with which we can make authenticated API requests.
//		c := t.Client()
//		c.Post(...)
//		// ...
//		// btw, r.FormValue("state") == "foo"
//	}
//
package oauth

// TODO(adg): A means of automatically saving credentials when updated.

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"time"
)

// Config is the configuration of an OAuth consumer.
type Config struct {
	ClientId     string
	ClientSecret string
	Scope        string
	AuthURL      string
	TokenURL     string
	RedirectURL  string // Defaults to out-of-band mode if empty.
}

func (c *Config) redirectURL() string {
	if c.RedirectURL != "" {
		return c.RedirectURL
	}
	return "oob"
}

// Token contains an end-user's tokens.
// This is the data you must store to persist authentication.
type Token struct {
	AccessToken  string
	RefreshToken string
	Expiry       time.Time // If zero the token has no (known) expiry time.
}

func (t *Token) Expired() bool {
	if t.Expiry.IsZero() {
		return false
	}
	return t.Expiry.Before(time.Now())
}

// Transport implements http.RoundTripper. When configured with a valid
// Config and Token it can be used to make authenticated HTTP requests.
//
//	t := &oauth.Transport{config}
//      t.Exchange(code)
//      // t now contains a valid Token
//	r, _, err := t.Client().Get("http://example.org/url/requiring/auth")
//
// It will automatically refresh the Token if it can,
// updating the supplied Token in place.
type Transport struct {
	*Config
	*Token

	// Transport is the HTTP transport to use when making requests.
	// It will default to http.DefaultTransport if nil.
	// (It should never be an oauth.Transport.)
	Transport http.RoundTripper
}

// Client returns an *http.Client that makes OAuth-authenticated requests.
func (t *Transport) Client() *http.Client {
	return &http.Client{Transport: t}
}

func (t *Transport) transport() http.RoundTripper {
	if t.Transport != nil {
		return t.Transport
	}
	return http.DefaultTransport
}

// AuthCodeURL returns a URL that the end-user should be redirected to,
// so that they may obtain an authorization code.
func (c *Config) AuthCodeURL(state string) string {
	url_, err := url.Parse(c.AuthURL)
	if err != nil {
		panic("AuthURL malformed: " + err.Error())
	}
	q := url.Values{
		"response_type": {"code"},
		"client_id":     {c.ClientId},
		"redirect_uri":  {c.redirectURL()},
		"scope":         {c.Scope},
		"state":         {state},
	}.Encode()
	if url_.RawQuery == "" {
		url_.RawQuery = q
	} else {
		url_.RawQuery += "&" + q
	}
	return url_.String()
}

// Exchange takes a code and gets access Token from the remote server.
func (t *Transport) Exchange(code string) (tok *Token, err error) {
	if t.Config == nil {
		return nil, errors.New("no Config supplied")
	}
	tok = new(Token)
	err = t.updateToken(tok, url.Values{
		"grant_type":   {"authorization_code"},
		"redirect_uri": {t.redirectURL()},
		"scope":        {t.Scope},
		"code":         {code},
	})
	if err == nil {
		t.Token = tok
	}
	return
}

// RoundTrip executes a single HTTP transaction using the Transport's
// Token as authorization headers.
//
// This method will attempt to renew the Token if it has expired and may return
// an error related to that Token renewal before attempting the client request.
// If the Token cannot be renewed a non-nil os.Error value will be returned.
// If the Token is invalid callers should expect HTTP-level errors,
// as indicated by the Response's StatusCode.
func (t *Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	if t.Config == nil {
		return nil, errors.New("no Config supplied")
	}
	if t.Token == nil {
		return nil, errors.New("no Token supplied")
	}

	// Refresh the Token if it has expired.
	if t.Expired() {
		if err := t.Refresh(); err != nil {
			return nil, err
		}
	}

	// Make the HTTP request.
	req.Header.Set("Authorization", "OAuth "+t.AccessToken)
	return t.transport().RoundTrip(req)
}

// Refresh renews the Transport's AccessToken using its RefreshToken.
func (t *Transport) Refresh() error {
	if t.Config == nil {
		return errors.New("no Config supplied")
	} else if t.Token == nil {
		return errors.New("no existing Token")
	}

	return t.updateToken(t.Token, url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {t.RefreshToken},
	})
}

func (t *Transport) updateToken(tok *Token, v url.Values) error {
	v.Set("client_id", t.ClientId)
	v.Set("client_secret", t.ClientSecret)
	r, err := (&http.Client{Transport: t.transport()}).PostForm(t.TokenURL, v)
	if err != nil {
		return err
	}
	defer r.Body.Close()
	if r.StatusCode != 200 {
		return errors.New("invalid response: " + r.Status)
	}
	var b struct {
		Access    string        `json:"access_token"`
		Refresh   string        `json:"refresh_token"`
		ExpiresIn time.Duration `json:"expires_in"`
	}
	if err = json.NewDecoder(r.Body).Decode(&b); err != nil {
		return err
	}
	tok.AccessToken = b.Access
	tok.RefreshToken = b.Refresh
	if b.ExpiresIn == 0 {
		tok.Expiry = time.Time{}
	} else {
		tok.Expiry = time.Now().Add(b.ExpiresIn * time.Second)
	}
	return nil
}
