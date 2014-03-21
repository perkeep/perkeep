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
	"time"

	"camlistore.org/pkg/context"
	"camlistore.org/pkg/httputil"
	"camlistore.org/pkg/importer"
	"camlistore.org/pkg/jsonconfig"
	"camlistore.org/pkg/schema"
	"camlistore.org/third_party/github.com/garyburd/go-oauth/oauth"
)

const (
	apiURL            = "https://api.twitter.com/1.1/"
	tweetRequestLimit = 200 // max number of tweets we can get in a user_timeline request
)

var (
	oauthClient = &oauth.Client{
		TemporaryCredentialRequestURI: "https://api.twitter.com/oauth/request_token",
		ResourceOwnerAuthorizationURI: "https://api.twitter.com/oauth/authorize",
		TokenRequestURI:               "https://api.twitter.com/oauth/access_token",
	}
)

func init() {
	importer.Register("twitter", newFromConfig)
}

type imp struct {
	host   *importer.Host
	userid string // empty if the user isn't authenticated
	cred   *oauth.Credentials
}

func newFromConfig(cfg jsonconfig.Obj, host *importer.Host) (importer.Importer, error) {
	apiKey := cfg.RequiredString("apiKey")
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	parts := strings.Split(apiKey, ":")
	if len(parts) != 2 {
		return nil, fmt.Errorf("Twitter importer: Invalid apiKey configuration: %q", apiKey)
	}

	oauthClient.Credentials = oauth.Credentials{
		Token:  parts[0],
		Secret: parts[1],
	}

	return &imp{
		host: host,
		cred: &oauthClient.Credentials,
	}, nil
}

func (im *imp) CanHandleURL(url string) bool { return false }
func (im *imp) ImportURL(url string) error   { panic("unused") }

func (im *imp) Prefix() string {
	// This should only get called when we're importing, so it's OK to
	// assume we're authenticated.
	return fmt.Sprintf("twitter:%s", im.userid)
}

func (im *imp) String() string {
	// We use this in logging when we're not authenticated, so it should do
	// something reasonable in that case.
	userId := "<unauthenticated>"
	if im.userid != "" {
		userId = im.userid
	}
	return fmt.Sprintf("twitter:%s", userId)
}

func (im *imp) Run(ctx *context.Context) error {
	log.Print("Twitter running...")

	if err := im.importTweets(ctx); err != nil {
		return err
	}

	return nil
}

type tweetItem struct {
	Id        string `json:"id_str"`
	Text      string
	CreatedAt string `json:"created_at"`
}

func (im *imp) importTweets(ctx *context.Context) error {
	maxId := ""
	continueRequests := true

	for continueRequests {
		if ctx.IsCanceled() {
			log.Printf("Twitter importer: interrupted")
			return context.ErrCanceled
		}

		var resp []*tweetItem
		if err := im.doAPI(&resp, "statuses/user_timeline.json", "count", strconv.Itoa(tweetRequestLimit), "max_id", maxId); err != nil {
			return err
		}

		tweetsNode, err := im.getTopLevelNode("tweets", "Tweets")
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
			if ctx.IsCanceled() {
				log.Printf("Twitter importer: interrupted")
				return context.ErrCanceled
			}
			err = im.importTweet(tweetsNode, tweet)
			if err != nil {
				log.Printf("Twitter importer: error importing tweet %s %v", tweet.Id, err)
				continue
			}
		}
	}

	return nil
}

func (im *imp) importTweet(parent *importer.Object, tweet *tweetItem) error {
	tweetNode, err := parent.ChildPathObject(tweet.Id)
	if err != nil {
		return err
	}

	title := "Tweet id " + tweet.Id

	createdTime, err := time.Parse(time.RubyDate, tweet.CreatedAt)
	if err != nil {
		log.Printf("Twitter importer: error parsing time %s %v", tweet.Id, err)
		return err
	}

	// TODO: import photos referenced in tweets
	return tweetNode.SetAttrs(
		"twitterId", tweet.Id,
		"camliNodeType", "twitter.com:tweet",
		"startDate", schema.RFC3339FromTime(createdTime),
		"content", tweet.Text,
		"title", title)
}

// utility

func (im *imp) getTopLevelNode(path string, title string) (*importer.Object, error) {
	root, err := im.getRootNode()
	if err != nil {
		return nil, err
	}

	photos, err := root.ChildPathObject(path)
	if err != nil {
		return nil, err
	}

	if err := photos.SetAttr("title", title); err != nil {
		return nil, err
	}
	return photos, nil
}

func (im *imp) getRootNode() (*importer.Object, error) {
	root, err := im.host.RootObject()
	if err != nil {
		return nil, err
	}

	title := fmt.Sprintf("Twitter (%s)", im.userid)
	if err := root.SetAttr("title", title); err != nil {
		return nil, err
	}

	return root, nil
}

// twitter api builders

func (im *imp) doAPI(result interface{}, apiPath string, keyval ...string) error {
	if len(keyval)%2 == 1 {
		panic("Incorrect number of keyval arguments")
	}

	if im.cred == nil {
		return fmt.Errorf("No authentication creds")
	}

	if im.userid == "" {
		return fmt.Errorf("No user id")
	}

	form := url.Values{}
	form.Set("user_id", im.userid)
	for i := 0; i < len(keyval); i += 2 {
		if keyval[i+1] != "" {
			form.Set(keyval[i], keyval[i+1])
		}
	}

	res, err := im.doGet(apiURL+apiPath, form)
	if err != nil {
		return err
	}
	err = httputil.DecodeJSON(res, result)
	if err != nil {
		log.Printf("Error parsing response for %s: %s", apiURL, err)
	}
	return err
}

func (im *imp) doGet(url string, form url.Values) (*http.Response, error) {
	if im.cred == nil {
		return nil, errors.New("Not logged in. Go to /importer-twitter/login.")
	}

	res, err := oauthClient.Get(im.host.HTTPClient(), im.cred, url, form)
	if err != nil {
		return nil, err
	}

	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Auth request failed with: %s", res.Status)
	}

	return res, nil
}

// auth endpoints

func (im *imp) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if strings.HasSuffix(r.URL.Path, "/login") {
		im.serveLogin(w, r)
	} else if strings.HasSuffix(r.URL.Path, "/callback") {
		im.serveCallback(w, r)
	} else {
		httputil.BadRequestError(w, "Unknown path: %s", r.URL.Path)
	}
}

func (im *imp) serveLogin(w http.ResponseWriter, r *http.Request) {
	callback := im.host.BaseURL + "callback"
	tempCred, err := oauthClient.RequestTemporaryCredentials(im.host.HTTPClient(), callback, nil)
	if err != nil {
		httputil.ServeError(w, r, fmt.Errorf("Twitter importer: Error getting temp cred: %s", err))
		return
	}

	im.cred = tempCred

	authURL := oauthClient.AuthorizationURL(tempCred, nil)
	http.Redirect(w, r, authURL, 302)
}

func (im *imp) serveCallback(w http.ResponseWriter, r *http.Request) {
	if im.cred.Token != r.FormValue("oauth_token") {
		httputil.BadRequestError(w, "Twitter importer: unexpected oauth_token")
		return
	}

	tokenCred, vals, err := oauthClient.RequestToken(im.host.HTTPClient(), im.cred, r.FormValue("oauth_verifier"))
	if err != nil {
		httputil.ServeError(w, r, fmt.Errorf("Twitter importer: error getting request token: %s ", err))
		return
	}
	im.cred = tokenCred

	userid := vals.Get("user_id")
	if userid == "" {
		log.Printf("Couldn't get user id: %v", err)
		http.Error(w, "can't get user id", 500)
		return
	}
	im.userid = userid

	http.Redirect(w, r, im.host.BaseURL+"?mode=start", 302)
}
