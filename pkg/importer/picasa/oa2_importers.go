/*
Copyright 2014 The Camlistore Authors.

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

package picasa

import (
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/httputil"
	"camlistore.org/pkg/importer"

	"camlistore.org/third_party/code.google.com/p/goauth2/oauth"

	"go4.org/ctxutil"
	"golang.org/x/net/context"
)

const (
	// acctAttrOAuthToken stores `access + " " + refresh + " " + expiry`
	// See encodeToken and decodeToken.
	acctAttrOAuthToken = "oauthToken"
)

// extendedOAuth2 provides implementation for some common importer methods regarding authentication.
//
// The oauthConfig is used in the authentications - think Scope and AuthURL.
//
// The getUserInfo function (if provided) should return the
// user ID, first name and last name of the user.
type extendedOAuth2 struct {
	importer.OAuth2
	oauthConfig oauth.Config
	getUserInfo func(ctx context.Context) (*userInfo, error)
}

// newExtendedOAuth2 returns a default implementation of
// some common methods for OAuth2-based importers.
func newExtendedOAuth2(oauthConfig oauth.Config,
	getUserInfo func(ctx context.Context) (*userInfo, error),
) extendedOAuth2 {
	return extendedOAuth2{oauthConfig: oauthConfig, getUserInfo: getUserInfo}
}

func (extendedOAuth2) IsAccountReady(acctNode *importer.Object) (ok bool, err error) {
	if acctNode.Attr(importer.AcctAttrUserID) != "" && acctNode.Attr(acctAttrOAuthToken) != "" {
		return true, nil
	}
	return false, nil
}

func (im extendedOAuth2) SummarizeAccount(acct *importer.Object) string {
	ok, err := im.IsAccountReady(acct)
	if err != nil || !ok {
		return ""
	}
	if acct.Attr(importer.AcctAttrGivenName) == "" && acct.Attr(importer.AcctAttrFamilyName) == "" {
		return fmt.Sprintf("userid %s", acct.Attr(importer.AcctAttrUserID))
	}
	return fmt.Sprintf("userid %s (%s %s)",
		acct.Attr(importer.AcctAttrUserID),
		acct.Attr(importer.AcctAttrGivenName),
		acct.Attr(importer.AcctAttrFamilyName))
}

func (im extendedOAuth2) ServeSetup(w http.ResponseWriter, r *http.Request, ctx *importer.SetupContext) error {
	oauthConfig, err := im.auth(ctx)
	if err == nil {
		// we will get back this with the token, so use it for preserving account info
		state := "acct:" + ctx.AccountNode.PermanodeRef().String()
		http.Redirect(w, r, oauthConfig.AuthCodeURL(state), 302)
	}
	return err
}

// CallbackURLParameters returns the needed callback parameters - empty for Google Picasa.
func (im extendedOAuth2) CallbackURLParameters(acctRef blob.Ref) url.Values {
	return url.Values{}
}

// notOAuthTransport returns c's Transport, or its underlying transport if c.Transport
// is an OAuth Transport.
func notOAuthTransport(c *http.Client) (tr http.RoundTripper) {
	tr = c.Transport
	if otr, ok := tr.(*oauth.Transport); ok {
		tr = otr.Transport
	}
	return
}

func (im extendedOAuth2) ServeCallback(w http.ResponseWriter, r *http.Request, ctx *importer.SetupContext) {
	if im.getUserInfo == nil {
		panic("No getUserInfo is provided, don't use the default ServeCallback!")
	}

	oauthConfig, err := im.auth(ctx)
	if err != nil {
		httputil.ServeError(w, r, fmt.Errorf("Error getting oauth config: %v", err))
		return
	}

	if r.Method != "GET" {
		http.Error(w, "Expected a GET", 400)
		return
	}
	code := r.FormValue("code")
	if code == "" {
		http.Error(w, "Expected a code", 400)
		return
	}

	// picago calls take an *http.Client, so we need to provide one which already
	// has a transport set up correctly wrt to authentication. In particular, it
	// needs to have the access token that is obtained during Exchange.
	transport := &oauth.Transport{
		Config:    oauthConfig,
		Transport: notOAuthTransport(ctxutil.Client(ctx)),
	}
	token, err := transport.Exchange(code)
	log.Printf("Token = %#v, error %v", token, err)
	if err != nil {
		log.Printf("Token Exchange error: %v", err)
		httputil.ServeError(w, r, fmt.Errorf("token exchange error: %v", err))
		return
	}

	picagoCtx, cancel := context.WithCancel(context.WithValue(ctx, ctxutil.HTTPClient, transport.Client()))
	defer cancel()

	userInfo, err := im.getUserInfo(picagoCtx)
	if err != nil {
		log.Printf("Couldn't get username: %v", err)
		httputil.ServeError(w, r, fmt.Errorf("can't get username: %v", err))
		return
	}

	if err := ctx.AccountNode.SetAttrs(
		importer.AcctAttrUserID, userInfo.ID,
		importer.AcctAttrGivenName, userInfo.FirstName,
		importer.AcctAttrFamilyName, userInfo.LastName,
		acctAttrOAuthToken, encodeToken(token),
	); err != nil {
		httputil.ServeError(w, r, fmt.Errorf("Error setting attribute: %v", err))
		return
	}
	http.Redirect(w, r, ctx.AccountURL(), http.StatusFound)
}

// encodeToken encodes the oauth.Token as
// AccessToken + " " + RefreshToken + " " + Expiry.Unix()
func encodeToken(token *oauth.Token) string {
	if token == nil {
		return ""
	}
	var seconds int64
	if !token.Expiry.IsZero() {
		seconds = token.Expiry.Unix()
	}
	return token.AccessToken + " " + token.RefreshToken + " " + strconv.FormatInt(seconds, 10)
}

// decodeToken parses an access token, refresh token, and optional
// expiry unix timestamp separated by spaces into an oauth.Token.
// It returns as much as it can.
func decodeToken(encoded string) oauth.Token {
	var t oauth.Token
	f := strings.Fields(encoded)
	if len(f) > 0 {
		t.AccessToken = f[0]
	}
	if len(f) > 1 {
		t.RefreshToken = f[1]
	}
	if len(f) > 2 && f[2] != "0" {
		sec, err := strconv.ParseInt(f[2], 10, 64)
		if err == nil {
			t.Expiry = time.Unix(sec, 0)
		}
	}
	return t
}

func (im extendedOAuth2) auth(ctx *importer.SetupContext) (*oauth.Config, error) {
	clientId, secret, err := ctx.Credentials()
	if err != nil {
		return nil, err
	}
	conf := im.oauthConfig
	conf.ClientId, conf.ClientSecret, conf.RedirectURL = clientId, secret, ctx.CallbackURL()
	return &conf, nil
}

// userInfo contains basic information about the identity of the imported
// account owner. Its use is discouraged as it might be refactored soon.
// Importer implementations should rather make their own dedicated type for
// now.
type userInfo struct {
	ID        string
	FirstName string
	LastName  string
}
