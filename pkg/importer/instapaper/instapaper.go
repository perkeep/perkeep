/*
Copyright 2013 The Perkeep Authors

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

package instapaper // import "perkeep.org/pkg/importer/instapaper"

import (
	"encoding/json"
	"fmt"
	"github.com/garyburd/go-oauth/oauth"
	"html/template"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"perkeep.org/internal/httputil"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/importer"
	"strconv"
)

func init() {
	importer.Register("Instapaper", &imp{})
}

// It must implement the importer.Importer interface in order for
// it to be registered (in the init above).
type imp struct {
	// The struct or underlying type implementing an importer
	// holds state that is global, and not per-account, so it
	// should not be used to cache account-specific
	// resources. Some importers (e.g. Foursquare) use this space
	// to cache mappings from site-specific global resource URLs
	// (e.g. category icons) to the fileref once it's been copied
	// into Perkeep.
}

func (*imp) Properties() importer.Properties {
	return importer.Properties{
		// NeedsAPIKey tells the importer framework that this
		// importer will be calling the
		// {RunContext,SetupContext}.Credentials method to get
		// the OAuth client ID & client secret, which may be
		// either configured on the importer permanode, or
		// statically in the server's config file.
		NeedsAPIKey: true,

		// SupportsIncremental signals to the importer host that this
		// importer has been optimized to be run regularly (e.g. every 5
		// minutes or half hour).  If it returns false, the user must
		// manually start imports.
		SupportsIncremental: false,
	}
}

const (
	AcctAttrAccessToken       = "oauthAccessToken"
	AcctAttrAccessTokenSecret = "oauthAccessTokenSecret"
	acctAttrUserId            = "userId"
	tokenRequestURL           = "https://www.instapaper.com/api/1/oauth/access_token"
	verifyUserRequestURL      = "https://www.instapaper.com/api/1/account/verify_credentials"
)

//var oAuthURIs = importer.OAuthURIs{
//	TokenRequestURI: tokenRequestURL,
//}

func (*imp) IsAccountReady(acct *importer.Object) (ready bool, err error) {
	// This method tells the importer framework whether this account
	// permanode (accessed via the importer.Object) is ready to start
	// an import.  Here you would typically check whether you have the
	// right metadata/tokens on the account.
	return acct.Attr(AcctAttrAccessToken) != "" && acct.Attr(AcctAttrAccessTokenSecret) != "" && acct.Attr(acctAttrUserId) != "", nil
}

// SummarizeAccount returns a summary for the account if it is configured,
// or an error string otherwise.
func (*imp) SummarizeAccount(acct *importer.Object) string {
	// This method is run by the importer framework if the account is
	// ready (see IsAccountReady) and summarizes the account in
	// the list of accounts on the importer page.
	return acct.Attr(acctAttrUserId) // return error due to account and password not being configured.
}
func (imp) ServeSetup(w http.ResponseWriter, r *http.Request, ctx *importer.SetupContext) error {
	return tmpl.ExecuteTemplate(w, "serveSetup", ctx)
}

var tmpl = template.Must(template.New("root").Parse(`
{{define "serveSetup"}}
<h1>Configuring Instapaper Account</h1>
<form method="get" action="{{.CallbackURL}}">
  <input type="hidden" name="acct" value="{{.AccountNode.PermanodeRef}}">
  <table border=0 cellpadding=3>
  <tr><td align=right>Username</td><td><input name="username" size=50></td></tr>
  <tr><td align=right>Password</td><td><input name="password" size=50></td></tr>
  <tr><td align=right></td><td><input type="submit" value="Add"></td></tr>
  </table>
</form>
{{end}}
`))

var _ importer.ImporterSetupHTMLer = (*imp)(nil)

func (im *imp) AccountSetupHTML(host *importer.Host) string {
	return "<h1>TODO: Write setup instructions here</h1><p>You will need to send an email.</p>"
}

func (im *imp) ServeCallback(w http.ResponseWriter, r *http.Request, ctx *importer.SetupContext) {
	username := r.FormValue("username")
	password := r.FormValue("password")

	// Have to assume password can be blank as Instapaper does not require a password
	if username == "" {
		http.Error(w, "Expected a username and password", 400)
		return
	}

	clientID, secret, err := ctx.Credentials()
	if err != nil {
		http.Error(w, "error retrieving clientID and secret from importer configuration", 400)
		return
	}

	oauthClient := &oauth.Client{
		Credentials: oauth.Credentials{
			Token:  clientID,
			Secret: secret,
		},
	}
	creds, err := im.getOauthCreds(username, password, oauthClient, ctx)
	if err != nil {
		httputil.ServeError(w, r, fmt.Errorf("error while attempting to request an access token: %v", err))
		return
	}

	user, err := im.getUser(creds, oauthClient, ctx)
	if err != nil {
		httputil.ServeError(w, r, fmt.Errorf("error while retrieving user information: %v", err))
		return
	}

	if err := ctx.AccountNode.SetAttrs(
		"title", fmt.Sprintf("Instapaper account: %s", user.Username),
		AcctAttrAccessToken, creds.Token,
		AcctAttrAccessTokenSecret, creds.Secret,
		acctAttrUserId, strconv.Itoa(user.UserId),
	); err != nil {
		httputil.ServeError(w, r, fmt.Errorf("error setting attributes: %v", err))
		return
	}
	http.Redirect(w, r, ctx.AccountURL(), http.StatusFound)
}

func (im *imp) getOauthCreds(username string, password string, oauthClient *oauth.Client, ctx *importer.SetupContext) (credentials *oauth.Credentials, err error) {
	req, err := http.NewRequest("POST", tokenRequestURL, nil)
	if err != nil {
		log.Println("error initializing request for access token")
		return nil, err
	}

	form := url.Values{}
	form.Set("x_auth_username", username)
	form.Set("x_auth_password", password)
	form.Set("x_auth_mode", "client_auth")
	req.Header.Set("Authorization", oauthClient.AuthorizationHeader(nil, "POST", req.URL, form))
	req.URL.RawQuery = form.Encode()
	resp, err := ctx.Host.HTTPClient().Do(req)
	if err != nil {
		log.Println("error requesting an access token")
		return nil, err
	}

	body, err := ioutil.ReadAll(resp.Body)
	resp.Body.Close()
	v, err := url.ParseQuery(string(body))
	if err != nil {
		log.Println("error parsing response from Instapaper")
		return nil, err
	}
	accessToken := v.Get("oauth_token")
	accessSecret := v.Get("oauth_token_secret")
	accessCreds := &oauth.Credentials{
		Token:  accessToken,
		Secret: accessSecret,
	}

	return accessCreds, nil
}

type User struct {
	UserId   int    `json:"user_id"`
	Username string `json:"username"`
}

func (im *imp) getUser(credentials *oauth.Credentials, oauthClient *oauth.Client, ctx *importer.SetupContext) (user *User, err error) {
	req, err := http.NewRequest("POST", verifyUserRequestURL, nil)
	if err != nil {
		log.Println("error initializing request to verify credentials")
		return nil, err
	}

	form := url.Values{}
	form.Set("x_auth_username", "")
	form.Set("x_auth_password", "")
	form.Set("x_auth_mode", "client_auth")
	req.Header.Set("Authorization", oauthClient.AuthorizationHeader(credentials, "POST", req.URL, form))
	req.URL.RawQuery = form.Encode()
	resp, err := ctx.Host.HTTPClient().Do(req)
	if err != nil {
		log.Println("request error: verify_credentials")
		return nil, err
	}

	body, err := ioutil.ReadAll(resp.Body)
	resp.Body.Close()
	var userResp []User
	err = json.Unmarshal(body, &userResp)
	if err != nil {
		log.Println("error parsing response from verify_credentials")
		return nil, err
	}

	return &userResp[0], nil
}

func (im *imp) Run(ctx *importer.RunContext) (err error) {
	log.Printf("Running Instapaper importer.")
	defer func() {
		log.Printf("Instapaper importer returned: %v", err)
	}()
	//root := ctx.RootNode()
	//fileRef, err := schema.WriteFileFromReader(ctx.Context(), ctx.Host.Target(), "foo.txt", strings.NewReader("Some file.\n"))
	//if err != nil {
	//	return err
	//}
	//obj, err := root.ChildPathObject("foo.txt")
	//if err != nil {
	//	return err
	//}
	//if err = obj.SetAttr("camliContent", fileRef.String()); err != nil {
	//	return err
	//}
	//n, _ := strconv.Atoi(ctx.AccountNode().Attr(acctAttrRunNumber))
	//n++
	//ctx.AccountNode().SetAttr(acctAttrRunNumber, fmt.Sprint(n))
	// Update the title each time, just to show it working. You
	// wouldn't actually do this:
	//return root.SetAttr("title", fmt.Sprintf("instapaper: %s import #%d", ctx.AccountNode().Attr(acctAttrUsername), n))

	clientID, secret, err := ctx.Credentials()
	oauthClient := &oauth.Client{
		Credentials: oauth.Credentials{
			Token:  clientID,
			Secret: secret,
		},
	}

	//oauthClient.SignForm(&oauthClient.Credentials, "POST", "https://www.instapaper.com/api/1/oauth/access_token", form)

	req, err := http.NewRequest("POST", "https://www.instapaper.com/api/1/oauth/access_token", nil)
	if err != nil {
		log.Fatalln(err)
		return err
	}

	form := url.Values{}
	form.Set("x_auth_username", "")
	form.Set("x_auth_password", "")
	form.Set("x_auth_mode", "client_auth")
	// Add more form values here

	req.Header.Set("Authorization", oauthClient.AuthorizationHeader(nil, "POST", req.URL, form))
	req.URL.RawQuery = form.Encode()
	resp, err := ctx.Host.HTTPClient().Do(req)
	if err != nil {
		return err
	}
	log.Printf("Response status: %v", resp.StatusCode)
	body, err := ioutil.ReadAll(resp.Body)
	resp.Body.Close()
	fmt.Println(string(body))
	return err
}

func (im *imp) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	httputil.BadRequestError(w, "Unexpected path: %s", r.URL.Path)
}

func (im *imp) CallbackRequestAccount(r *http.Request) (blob.Ref, error) {
	// We do not actually use OAuth, but this method works for us anyway.
	// Even if your importer implementation does not use OAuth, you can
	// probably just embed importer.OAuth1 in your implementation type.
	// If OAuth2, embedding importer.OAuth2 should work.
	return importer.OAuth1{}.CallbackRequestAccount(r)
}

func (im *imp) CallbackURLParameters(acctRef blob.Ref) url.Values {
	// See comment in CallbackRequestAccount.
	return importer.OAuth1{}.CallbackURLParameters(acctRef)
}
