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

// Package twitter implements a twitter.com importer.
package twitter

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"camlistore.org/pkg/context"
	"camlistore.org/pkg/httputil"
	"camlistore.org/pkg/importer"
	"camlistore.org/pkg/schema"
	"camlistore.org/third_party/github.com/garyburd/go-oauth/oauth"
)

const (
	apiURL                        = "https://api.twitter.com/1.1/"
	temporaryCredentialRequestURL = "https://api.twitter.com/oauth/request_token"
	resourceOwnerAuthorizationURL = "https://api.twitter.com/oauth/authorize"
	tokenRequestURL               = "https://api.twitter.com/oauth/access_token"
	userInfoAPIPath               = "account/verify_credentials.json"

	// Permanode attributes on account node:
	acctAttrUserID      = "twitterUserID"
	acctAttrScreenName  = "twitterScreenName"
	acctAttrUserFirst   = "twitterFirstName"
	acctAttrUserLast    = "twitterLastName"
	acctAttrAccessToken = "oauthAccessToken"

	tweetRequestLimit = 200 // max number of tweets we can get in a user_timeline request
)

func init() {
	importer.Register("twitter", &imp{})
}

var _ importer.ImporterSetupHTMLer = (*imp)(nil)

var (
	oauthClient = &oauth.Client{
		TemporaryCredentialRequestURI: temporaryCredentialRequestURL,
		ResourceOwnerAuthorizationURI: resourceOwnerAuthorizationURL,
		TokenRequestURI:               tokenRequestURL,
	}
)

type imp struct {
	// cred are the various credentials passed around during OAuth. First the temporary
	// ones, then the access token and secret.
	mu       sync.Mutex // guards credsVal
	credsVal *oauth.Credentials
}

func (im *imp) creds() *oauth.Credentials {
	im.mu.Lock()
	defer im.mu.Unlock()
	return im.credsVal
}

func (im *imp) setCreds(v *oauth.Credentials) {
	im.mu.Lock()
	defer im.mu.Unlock()
	im.credsVal = v
}

func (im *imp) NeedsAPIKey() bool { return true }

func (im *imp) IsAccountReady(acctNode *importer.Object) (ok bool, err error) {
	if acctNode.Attr(acctAttrUserID) != "" && acctNode.Attr(acctAttrAccessToken) != "" {
		return true, nil
	}
	return false, nil
}

func (im *imp) SummarizeAccount(acct *importer.Object) string {
	ok, err := im.IsAccountReady(acct)
	if err != nil {
		return "Not configured; error = " + err.Error()
	}
	if !ok {
		return "Not configured"
	}
	if acct.Attr(acctAttrUserFirst) == "" && acct.Attr(acctAttrUserLast) == "" {
		return fmt.Sprintf("@%s", acct.Attr(acctAttrScreenName))
	}
	return fmt.Sprintf("@%s (%s %s)", acct.Attr(acctAttrScreenName),
		acct.Attr(acctAttrUserFirst), acct.Attr(acctAttrUserLast))
}

func (im *imp) AccountSetupHTML(host *importer.Host) string {
	base := host.ImporterBaseURL() + "twitter"
	return fmt.Sprintf(`
<h1>Configuring Twitter</h1>
<p>Visit <a href='https://apps.twitter.com/'>https://apps.twitter.com/</a> and click "Create New App".</p>
<p>Use the following settings:</p>
<ul>
  <li>Name: Does not matter. (camlistore-importer).</li>
  <li>Description: Does not matter. (imports twitter data into camlistore).</li>
  <li>Website: <b>%s</b></li>
  <li>Callback URL: <b>%s</b></li>
</ul>
<p>Click "Create your Twitter application".You should be redirected to the Application Management page of your newly created application.
</br>Go to the API Keys tab. Copy the "API key" and "API secret" into the "Client ID" and "Client Secret" boxes above.</p>
`, base, base+"/callback")
}

// A run is our state for a given run of the importer.
type run struct {
	*importer.RunContext
	im *imp
}

func (im *imp) Run(ctx *importer.RunContext) error {
	r := &run{
		RunContext: ctx,
		im:         im,
	}
	userID := ctx.AccountNode().Attr(acctAttrUserID)
	if userID == "" {
		return errors.New("UserID hasn't been set by account setup.")
	}

	if err := r.importTweets(userID); err != nil {
		return err
	}
	return nil
}

type tweetItem struct {
	Id        string `json:"id_str"`
	Text      string
	CreatedAt string `json:"created_at"`
}

func (r *run) importTweets(userID string) error {
	maxId := ""
	continueRequests := true

	for continueRequests {
		if r.Context.IsCanceled() {
			log.Printf("Twitter importer: interrupted")
			return context.ErrCanceled
		}

		var resp []*tweetItem
		if err := r.im.doAPI(r.Context, &resp, "statuses/user_timeline.json",
			"user_id", userID,
			"count", strconv.Itoa(tweetRequestLimit),
			"max_id", maxId); err != nil {
			return err
		}

		tweetsNode, err := r.getTopLevelNode("tweets", "Tweets")
		if err != nil {
			return err
		}

		itemcount := len(resp)
		log.Printf("Twitter importer: Importing %d tweets", itemcount)
		if itemcount < tweetRequestLimit {
			continueRequests = false
		} else {
			lastTweet := resp[len(resp)-1]
			maxId = lastTweet.Id
		}

		for _, tweet := range resp {
			if r.Context.IsCanceled() {
				log.Printf("Twitter importer: interrupted")
				return context.ErrCanceled
			}
			err = r.importTweet(tweetsNode, tweet)
			if err != nil {
				log.Printf("Twitter importer: error importing tweet %s %v", tweet.Id, err)
				continue
			}
		}
	}

	return nil
}

func (r *run) importTweet(parent *importer.Object, tweet *tweetItem) error {
	tweetNode, err := parent.ChildPathObject(tweet.Id)
	if err != nil {
		return err
	}

	title := "Tweet id " + tweet.Id

	createdTime, err := time.Parse(time.RubyDate, tweet.CreatedAt)
	if err != nil {
		return fmt.Errorf("could not parse time %q: %v", tweet.CreatedAt, err)
	}

	// TODO: import photos referenced in tweets
	return tweetNode.SetAttrs(
		"twitterId", tweet.Id,
		"camliNodeType", "twitter.com:tweet",
		"startDate", schema.RFC3339FromTime(createdTime),
		"content", tweet.Text,
		"title", title)
}

func (r *run) getTopLevelNode(path string, title string) (*importer.Object, error) {
	tweets, err := r.RootNode().ChildPathObject(path)
	if err != nil {
		return nil, err
	}
	if err := tweets.SetAttr("title", title); err != nil {
		return nil, err
	}
	return tweets, nil
}

// TODO(mpl): move to an api.go when we it gets bigger.

type userInfo struct {
	ID         string `json:"id_str"`
	ScreenName string `json:"screen_name"`
	Name       string `json:"name,omitempty"`
}

func (im *imp) getUserInfo(ctx *context.Context) (userInfo, error) {
	var ui userInfo
	if err := im.doAPI(ctx, &ui, userInfoAPIPath); err != nil {
		return ui, err
	}
	if ui.ID == "" {
		return ui, fmt.Errorf("No userid returned")
	}
	return ui, nil
}

func (im *imp) doAPI(ctx *context.Context, result interface{}, apiPath string, keyval ...string) error {
	if len(keyval)%2 == 1 {
		panic("Incorrect number of keyval arguments. must be even.")
	}

	if im.creds() == nil {
		return fmt.Errorf("No authentication creds")
	}

	form := url.Values{}
	for i := 0; i < len(keyval); i += 2 {
		if keyval[i+1] != "" {
			form.Set(keyval[i], keyval[i+1])
		}
	}

	fullURL := apiURL + apiPath
	res, err := im.doGet(ctx, fullURL, form)
	if err != nil {
		return err
	}
	err = httputil.DecodeJSON(res, result)
	if err != nil {
		return fmt.Errorf("could not parse response for %s: %v", fullURL, err)
	}
	return nil
}

func (im *imp) doGet(ctx *context.Context, url string, form url.Values) (*http.Response, error) {
	creds := im.creds()
	if creds == nil {
		return nil, errors.New("No OAuth credentials. Not logged in?")
	}
	res, err := oauthClient.Get(ctx.HTTPClient(), creds, url, form)
	if err != nil {
		return nil, fmt.Errorf("Error fetching %s: %v", url, err)
	}
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Get request on %s failed with: %s", url, res.Status)
	}
	return res, nil
}

func auth(ctx *importer.SetupContext) (*oauth.Credentials, error) {
	clientId, secret, err := ctx.Credentials()
	if err != nil {
		return nil, err
	}
	return &oauth.Credentials{
		Token:  clientId,
		Secret: secret,
	}, nil
}

func (im *imp) ServeSetup(w http.ResponseWriter, r *http.Request, ctx *importer.SetupContext) error {
	cred, err := auth(ctx)
	if err != nil {
		err = fmt.Errorf("Error getting API credentials: %v", err)
		httputil.ServeError(w, r, err)
		return err
	}
	oauthClient.Credentials = *cred
	tempCred, err := oauthClient.RequestTemporaryCredentials(ctx.HTTPClient(), ctx.CallbackURL(), nil)
	if err != nil {
		err = fmt.Errorf("Error getting temp cred: %v", err)
		httputil.ServeError(w, r, err)
	}
	im.setCreds(tempCred)

	authURL := oauthClient.AuthorizationURL(tempCred, nil)
	http.Redirect(w, r, authURL, 302)
	return nil
}

func (im *imp) ServeCallback(w http.ResponseWriter, r *http.Request, ctx *importer.SetupContext) {
	creds := im.creds()
	if creds == nil {
		log.Printf("twitter: nil creds in callback")
		httputil.BadRequestError(w, "nil creds in callback")
		return
	}
	if creds.Token != r.FormValue("oauth_token") {
		log.Printf("unexpected oauth_token: got %v, want %v", r.FormValue("oauth_token"), creds.Token)
		httputil.BadRequestError(w, "unexpected oauth_token")
		return
	}

	tokenCred, vals, err := oauthClient.RequestToken(ctx.Context.HTTPClient(), creds, r.FormValue("oauth_verifier"))
	if err != nil {
		httputil.ServeError(w, r, fmt.Errorf("Error getting request token: %v ", err))
		return
	}
	userid := vals.Get("user_id")
	if userid == "" {
		httputil.ServeError(w, r, fmt.Errorf("Couldn't get user id: %v", err))
		return
	}
	im.setCreds(tokenCred)

	u, err := im.getUserInfo(ctx.Context)
	if err != nil {
		httputil.ServeError(w, r, fmt.Errorf("Couldn't get user info: %v", err))
		return
	}
	firstName, lastName := "", ""
	if u.Name != "" {
		if pieces := strings.Fields(u.Name); len(pieces) == 2 {
			firstName = pieces[0]
			lastName = pieces[1]
		}
	}
	if err := ctx.AccountNode.SetAttrs(
		acctAttrUserID, u.ID,
		acctAttrUserFirst, firstName,
		acctAttrUserLast, lastName,
		acctAttrScreenName, u.ScreenName,
		acctAttrAccessToken, tokenCred.Token,
	); err != nil {
		httputil.ServeError(w, r, fmt.Errorf("Error setting attribute: %v", err))
		return
	}
	http.Redirect(w, r, ctx.AccountURL(), http.StatusFound)
}
