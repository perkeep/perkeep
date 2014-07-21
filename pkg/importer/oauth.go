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

package importer

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"

	"camlistore.org/pkg/blob"
)

const (
	AcctAttrTempToken         = "oauthTempToken"
	AcctAttrTempSecret        = "oauthTempSecret"
	AcctAttrAccessToken       = "oauthAccessToken"
	AcctAttrAccessTokenSecret = "oauthAccessTokenSecret"
	// AcctAttrOAuthToken stores `access + " " + refresh + " " + expiry`
	AcctAttrOAuthToken = "oauthToken"
)

// OAuth1 provides methods that the importer implementations can use to
// help with OAuth authentication.
type OAuth1 struct{}

func (OAuth1) CallbackRequestAccount(r *http.Request) (blob.Ref, error) {
	acctRef, ok := blob.Parse(r.FormValue("acct"))
	if !ok {
		return blob.Ref{}, errors.New("missing 'acct=' blobref param")
	}
	return acctRef, nil
}

func (OAuth1) CallbackURLParameters(acctRef blob.Ref) url.Values {
	v := url.Values{}
	v.Add("acct", acctRef.String())
	return v
}

// OAuth2 provides methods that the importer implementations can use to
// help with OAuth2 authentication.
type OAuth2 struct{}

func (OAuth2) CallbackRequestAccount(r *http.Request) (blob.Ref, error) {
	state := r.FormValue("state")
	if state == "" {
		return blob.Ref{}, errors.New("missing 'state' parameter")
	}
	if !strings.HasPrefix(state, "acct:") {
		return blob.Ref{}, errors.New("wrong 'state' parameter value, missing 'acct:' prefix.")
	}
	acctRef, ok := blob.Parse(strings.TrimPrefix(state, "acct:"))
	if !ok {
		return blob.Ref{}, errors.New("invalid account blobref in 'state' parameter")
	}
	return acctRef, nil
}

func (OAuth2) CallbackURLParameters(acctRef blob.Ref) url.Values {
	v := url.Values{}
	v.Set("state", "acct:"+acctRef.String())
	return v
}

// RedirectURL returns the redirect URI that imp should set in an oauth.Config
// for the authorization phase of OAuth2 authentication.
func (OAuth2) RedirectURL(imp Importer, ctx *SetupContext) string {
	// We strip our callback URL of its query component, because the Redirect URI
	// we send during authorization has to match exactly the registered redirect
	// URI(s). This query component should be stored in the "state" paremeter instead.
	// See http://tools.ietf.org/html/rfc6749#section-3.1.2.2
	fullCallback := ctx.CallbackURL()
	queryPart := imp.CallbackURLParameters(ctx.AccountNode.PermanodeRef())
	if len(queryPart) == 0 {
		log.Printf("WARNING: callback URL %q has no query component", fullCallback)
	}
	u, _ := url.Parse(fullCallback)
	v := u.Query()
	// remove query params in CallbackURLParameters
	for k := range queryPart {
		v.Del(k)
	}
	u.RawQuery = v.Encode()
	return u.String()
}

// RedirectState returns the "state" query parameter that should be used for the authorization
// phase of OAuth2 authentication. This parameter contains the query component of the redirection
// URI. See http://tools.ietf.org/html/rfc6749#section-3.1.2.2
func (OAuth2) RedirectState(imp Importer, ctx *SetupContext) (state string, err error) {
	m := imp.CallbackURLParameters(ctx.AccountNode.PermanodeRef())
	state = m.Get("state")
	if state == "" {
		return "", errors.New("\"state\" not found in callback parameters")
	}
	return state, nil
}

// IsAccountReady returns whether the account has been properly configured
// - whether the user ID and access token has been stored in the given account node.
func (OAuth2) IsAccountReady(acctNode *Object) (ok bool, err error) {
	if acctNode.Attr(AcctAttrUserID) != "" &&
		acctNode.Attr(AcctAttrAccessToken) != "" {
		return true, nil
	}
	return false, nil
}

// NeedsAPIKey returns whether the importer needs an API key - returns constant true.
func (OAuth2) NeedsAPIKey() bool { return true }

// SummarizeAccount returns a summary for the account if it is configured,
// or an error string otherwise.
func (im OAuth2) SummarizeAccount(acct *Object) string {
	ok, err := im.IsAccountReady(acct)
	if err != nil {
		return ""
	}
	if !ok {
		return ""
	}
	if acct.Attr(AcctAttrGivenName) == "" &&
		acct.Attr(AcctAttrFamilyName) == "" {
		return fmt.Sprintf("userid %s", acct.Attr(AcctAttrUserID))
	}
	return fmt.Sprintf("userid %s (%s %s)",
		acct.Attr(AcctAttrUserID),
		acct.Attr(AcctAttrGivenName),
		acct.Attr(AcctAttrFamilyName))
}
