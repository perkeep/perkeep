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
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/context"
	"camlistore.org/pkg/httputil"
	"camlistore.org/pkg/importer"
	"camlistore.org/pkg/schema"
	"camlistore.org/pkg/syncutil"
	"camlistore.org/third_party/github.com/garyburd/go-oauth/oauth"
)

const (
	apiURL                        = "https://api.twitter.com/1.1/"
	temporaryCredentialRequestURL = "https://api.twitter.com/oauth/request_token"
	resourceOwnerAuthorizationURL = "https://api.twitter.com/oauth/authorize"
	tokenRequestURL               = "https://api.twitter.com/oauth/access_token"
	userInfoAPIPath               = "account/verify_credentials.json"

	// runCompleteVersion is a cache-busting version number of the
	// importer code. It should be incremented whenever the
	// behavior of this importer is updated enough to warrant a
	// complete run.  Otherwise, if the importer runs to
	// completion, this version number is recorded on the account
	// permanode and subsequent importers can stop early.
	runCompleteVersion = "4"

	// TODO(mpl): refactor these 4 below into an oauth package when doing flickr.
	acctAttrTempToken         = "oauthTempToken"
	acctAttrTempSecret        = "oauthTempSecret"
	acctAttrAccessToken       = "oauthAccessToken"
	acctAttrAccessTokenSecret = "oauthAccessTokenSecret"

	// acctAttrTweetZip specifies an optional attribte for the account permanode.
	// If set, it should be of a "file" schema blob referencing the tweets.zip
	// file that Twitter makes available for the full archive download.
	// The Twitter API doesn't go back forever in time, so if you started using
	// the Camlistore importer too late, you need to "camput file tweets.zip"
	// once downloading it from Twitter, and then:
	//   $ camput attr <acct-permanode> twitterArchiveZipFileRef <zip-fileref>
	// ... and re-do an import.
	acctAttrTweetZip = "twitterArchiveZipFileRef"

	// acctAttrZipDoneVersion is updated at the end of a successful zip import and
	// is used to determine whether the zip file needs to be re-imported in a future run.
	acctAttrZipDoneVersion = "twitterZipDoneVersion" // == "<fileref>:<runCompleteVersion>"

	// Per-tweet note of how we imported it: either "zip" or "api"
	attrImportMethod = "twitterImportMethod"

	tweetRequestLimit = 200 // max number of tweets we can get in a user_timeline request
	tweetsAtOnce      = 20  // how many tweets to import at once
)

func init() {
	importer.Register("twitter", &imp{})
}

var _ importer.ImporterSetupHTMLer = (*imp)(nil)

type imp struct {
	importer.OAuth1 // for CallbackRequestAccount and CallbackURLParameters
}

func (im *imp) NeedsAPIKey() bool { return true }

func (im *imp) IsAccountReady(acctNode *importer.Object) (ok bool, err error) {
	if acctNode.Attr(importer.AcctAttrUserID) != "" && acctNode.Attr(acctAttrAccessToken) != "" {
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
	s := fmt.Sprintf("@%s (%s), twitter id %s",
		acct.Attr(importer.AcctAttrUserName),
		acct.Attr(importer.AcctAttrName),
		acct.Attr(importer.AcctAttrUserID),
	)
	if acct.Attr(acctAttrTweetZip) != "" {
		s += " + zip file"
	}
	return s
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
	im          *imp
	incremental bool // whether we've completed a run in the past

	oauthClient *oauth.Client      // No need to guard, used read-only.
	accessCreds *oauth.Credentials // No need to guard, used read-only.

	mu     sync.Mutex // guards anyErr
	anyErr bool
}

func (r *run) oauthContext() oauthContext {
	return oauthContext{r.Context, r.oauthClient, r.accessCreds}
}

func (im *imp) Run(ctx *importer.RunContext) error {
	clientId, secret, err := ctx.Credentials()
	if err != nil {
		return fmt.Errorf("no API credentials: %v", err)
	}
	acctNode := ctx.AccountNode()
	accessToken := acctNode.Attr(acctAttrAccessToken)
	accessSecret := acctNode.Attr(acctAttrAccessTokenSecret)
	if accessToken == "" || accessSecret == "" {
		return errors.New("access credentials not found")
	}
	r := &run{
		RunContext:  ctx,
		im:          im,
		incremental: acctNode.Attr(importer.AcctAttrCompletedVersion) == runCompleteVersion,

		oauthClient: &oauth.Client{
			TemporaryCredentialRequestURI: temporaryCredentialRequestURL,
			ResourceOwnerAuthorizationURI: resourceOwnerAuthorizationURL,
			TokenRequestURI:               tokenRequestURL,
			Credentials: oauth.Credentials{
				Token:  clientId,
				Secret: secret,
			},
		},
		accessCreds: &oauth.Credentials{
			Token:  accessToken,
			Secret: accessSecret,
		},
	}

	userID := acctNode.Attr(importer.AcctAttrUserID)
	if userID == "" {
		return errors.New("UserID hasn't been set by account setup.")
	}

	if err := r.importTweets(userID); err != nil {
		return err
	}

	zipRef := acctNode.Attr(acctAttrTweetZip)
	zipDoneVal := zipRef + ":" + runCompleteVersion
	if zipRef != "" && !(r.incremental && acctNode.Attr(acctAttrZipDoneVersion) == zipDoneVal) {
		zipbr, ok := blob.Parse(zipRef)
		if !ok {
			return fmt.Errorf("invalid zip file blobref %q", zipRef)
		}
		fr, err := schema.NewFileReader(r.Host.BlobSource(), zipbr)
		if err != nil {
			return fmt.Errorf("error opening zip %v: %v", zipbr, err)
		}
		defer fr.Close()
		zr, err := zip.NewReader(fr, fr.Size())
		if err != nil {
			return fmt.Errorf("Error opening twitter zip file %v: %v", zipRef, err)
		}
		if err := r.importTweetsFromZip(userID, zr); err != nil {
			return err
		}
		if err := acctNode.SetAttrs(acctAttrZipDoneVersion, zipDoneVal); err != nil {
			return err
		}
	}

	r.mu.Lock()
	anyErr := r.anyErr
	r.mu.Unlock()

	if !anyErr {
		if err := acctNode.SetAttrs(importer.AcctAttrCompletedVersion, runCompleteVersion); err != nil {
			return err
		}
	}

	return nil
}

func (r *run) errorf(format string, args ...interface{}) {
	log.Printf(format, args...)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.anyErr = true
}

func (r *run) importTweets(userID string) error {
	maxId := ""
	continueRequests := true

	tweetsNode, err := r.getTopLevelNode("tweets", "Tweets")
	if err != nil {
		return err
	}

	numTweets := 0
	sawTweet := map[string]bool{}

	for continueRequests {
		if r.Context.IsCanceled() {
			r.errorf("Twitter importer: interrupted")
			return context.ErrCanceled
		}

		var resp []*tweetItem
		log.Printf("Fetching tweets for userid %s with max ID %q", userID, maxId)
		if err := r.oauthContext().doAPI(&resp, "statuses/user_timeline.json",
			"user_id", userID,
			"count", strconv.Itoa(tweetRequestLimit),
			"max_id", maxId); err != nil {
			return err
		}

		var (
			newThisBatch = 0
			allDupMu     sync.Mutex
			allDups      = true
			gate         = syncutil.NewGate(tweetsAtOnce)
			grp          syncutil.Group
		)
		for i := range resp {
			tweet := resp[i]

			// Dup-suppression.
			if sawTweet[tweet.Id] {
				continue
			}
			sawTweet[tweet.Id] = true
			newThisBatch++
			maxId = tweet.Id

			gate.Start()
			grp.Go(func() error {
				defer gate.Done()
				dup, err := r.importTweet(tweetsNode, tweet, true)
				if !dup {
					allDupMu.Lock()
					allDups = false
					allDupMu.Unlock()
				}
				if err != nil {
					r.errorf("Twitter importer: error importing tweet %s %v", tweet.Id, err)
				}
				return err
			})
		}
		if err := grp.Err(); err != nil {
			return err
		}
		numTweets += newThisBatch
		log.Printf("Imported %d tweets this batch; %d total.", newThisBatch, numTweets)
		if r.incremental && allDups {
			log.Printf("twitter incremental import found end batch")
			break
		}
		continueRequests = newThisBatch > 0
	}
	log.Printf("Successfully did full run of importing %d tweets", numTweets)
	return nil
}

func tweetsFromZipFile(zf *zip.File) (tweets []*tweetItem, err error) {
	rc, err := zf.Open()
	if err != nil {
		return nil, err
	}
	slurp, err := ioutil.ReadAll(rc)
	rc.Close()
	if err != nil {
		return nil, err
	}
	i := bytes.IndexByte(slurp, '[')
	if i < 0 {
		return nil, errors.New("No '[' found in zip file")
	}
	slurp = slurp[i:]
	if err := json.Unmarshal(slurp, &tweets); err != nil {
		return nil, fmt.Errorf("JSON error: %v", err)
	}
	return
}

func (r *run) importTweetsFromZip(userID string, zr *zip.Reader) error {
	log.Printf("Processing zip file with %d files", len(zr.File))

	tweetsNode, err := r.getTopLevelNode("tweets", "Tweets")
	if err != nil {
		return err
	}

	var (
		gate = syncutil.NewGate(tweetsAtOnce)
		grp  syncutil.Group
	)
	total := 0
	for _, zf := range zr.File {
		if !(strings.HasPrefix(zf.Name, "data/js/tweets/2") && strings.HasSuffix(zf.Name, ".js")) {
			continue
		}
		tweets, err := tweetsFromZipFile(zf)
		if err != nil {
			return fmt.Errorf("error reading tweets from %s: %v", zf.Name, err)
		}

		for i := range tweets {
			total++
			tweet := tweets[i]
			gate.Start()
			grp.Go(func() error {
				defer gate.Done()
				_, err := r.importTweet(tweetsNode, tweet, false)
				return err
			})
		}
	}
	err = grp.Err()
	log.Printf("zip import of tweets: %d total, err = %v", total, err)
	return err
}

func timeParseFirstFormat(timeStr string, format ...string) (t time.Time, err error) {
	if len(format) == 0 {
		panic("need more than 1 format")
	}
	for _, f := range format {
		t, err = time.Parse(f, timeStr)
		if err == nil {
			break
		}
	}
	return
}

// viaAPI is true if it came via the REST API, or false if it came via a zip file.
func (r *run) importTweet(parent *importer.Object, tweet *tweetItem, viaAPI bool) (dup bool, err error) {
	if r.Context.IsCanceled() {
		r.errorf("Twitter importer: interrupted")
		return false, context.ErrCanceled
	}
	tweetNode, err := parent.ChildPathObject(tweet.Id)
	if err != nil {
		return false, err
	}
	if tweetNode.Attr(attrImportMethod) == "api" && !viaAPI {
		return true, nil
	}

	// e.g. "2014-06-12 19:11:51 +0000"
	createdTime, err := timeParseFirstFormat(tweet.CreatedAt, time.RubyDate, "2006-01-02 15:04:05 -0700")
	if err != nil {
		return false, fmt.Errorf("could not parse time %q: %v", tweet.CreatedAt, err)
	}

	// TODO: import photos referenced in tweets
	url := fmt.Sprintf("https://twitter.com/%s/status/%v",
		r.AccountNode().Attr(importer.AcctAttrUserName),
		tweet.Id)
	attrs := []string{

		"twitterId", tweet.Id,
		"camliNodeType", "twitter.com:tweet",
		importer.AttrStartDate, schema.RFC3339FromTime(createdTime),
		"content", tweet.Text,
		importer.AttrURL, url,
	}
	if lat, long, ok := tweet.LatLong(); ok {
		attrs = append(attrs,
			"latitude", fmt.Sprint(lat),
			"longitude", fmt.Sprint(long),
		)
	}
	if viaAPI {
		attrs = append(attrs, attrImportMethod, "api")
	} else {
		attrs = append(attrs, attrImportMethod, "zip")
	}
	changes, err := tweetNode.SetAttrs2(attrs...)
	if err == nil && changes {
		log.Printf("Imported tweet %s", url)
	}
	return !changes, err
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

func getUserInfo(ctx oauthContext) (userInfo, error) {
	var ui userInfo
	if err := ctx.doAPI(&ui, userInfoAPIPath); err != nil {
		return ui, err
	}
	if ui.ID == "" {
		return ui, fmt.Errorf("No userid returned")
	}
	return ui, nil
}

func newOauthClient(ctx *importer.SetupContext) (*oauth.Client, error) {
	clientId, secret, err := ctx.Credentials()
	if err != nil {
		return nil, err
	}
	return &oauth.Client{
		TemporaryCredentialRequestURI: temporaryCredentialRequestURL,
		ResourceOwnerAuthorizationURI: resourceOwnerAuthorizationURL,
		TokenRequestURI:               tokenRequestURL,
		Credentials: oauth.Credentials{
			Token:  clientId,
			Secret: secret,
		},
	}, nil
}

func (im *imp) ServeSetup(w http.ResponseWriter, r *http.Request, ctx *importer.SetupContext) error {
	oauthClient, err := newOauthClient(ctx)
	if err != nil {
		err = fmt.Errorf("error getting OAuth client: %v", err)
		httputil.ServeError(w, r, err)
		return err
	}
	tempCred, err := oauthClient.RequestTemporaryCredentials(ctx.HTTPClient(), ctx.CallbackURL(), nil)
	if err != nil {
		err = fmt.Errorf("Error getting temp cred: %v", err)
		httputil.ServeError(w, r, err)
		return err
	}
	if err := ctx.AccountNode.SetAttrs(
		acctAttrTempToken, tempCred.Token,
		acctAttrTempSecret, tempCred.Secret,
	); err != nil {
		err = fmt.Errorf("Error saving temp creds: %v", err)
		httputil.ServeError(w, r, err)
		return err
	}

	authURL := oauthClient.AuthorizationURL(tempCred, nil)
	http.Redirect(w, r, authURL, 302)
	return nil
}

func (im *imp) ServeCallback(w http.ResponseWriter, r *http.Request, ctx *importer.SetupContext) {
	tempToken := ctx.AccountNode.Attr(acctAttrTempToken)
	tempSecret := ctx.AccountNode.Attr(acctAttrTempSecret)
	if tempToken == "" || tempSecret == "" {
		log.Printf("twitter: no temp creds in callback")
		httputil.BadRequestError(w, "no temp creds in callback")
		return
	}
	if tempToken != r.FormValue("oauth_token") {
		log.Printf("unexpected oauth_token: got %v, want %v", r.FormValue("oauth_token"), tempToken)
		httputil.BadRequestError(w, "unexpected oauth_token")
		return
	}
	oauthClient, err := newOauthClient(ctx)
	if err != nil {
		err = fmt.Errorf("error getting OAuth client: %v", err)
		httputil.ServeError(w, r, err)
		return
	}
	tokenCred, vals, err := oauthClient.RequestToken(
		ctx.Context.HTTPClient(),
		&oauth.Credentials{
			Token:  tempToken,
			Secret: tempSecret,
		},
		r.FormValue("oauth_verifier"),
	)
	if err != nil {
		httputil.ServeError(w, r, fmt.Errorf("Error getting request token: %v ", err))
		return
	}
	userid := vals.Get("user_id")
	if userid == "" {
		httputil.ServeError(w, r, fmt.Errorf("Couldn't get user id: %v", err))
		return
	}
	if err := ctx.AccountNode.SetAttrs(
		acctAttrAccessToken, tokenCred.Token,
		acctAttrAccessTokenSecret, tokenCred.Secret,
	); err != nil {
		httputil.ServeError(w, r, fmt.Errorf("Error setting token attributes: %v", err))
		return
	}

	u, err := getUserInfo(oauthContext{ctx.Context, oauthClient, tokenCred})
	if err != nil {
		httputil.ServeError(w, r, fmt.Errorf("Couldn't get user info: %v", err))
		return
	}
	if err := ctx.AccountNode.SetAttrs(
		importer.AcctAttrUserID, u.ID,
		importer.AcctAttrName, u.Name,
		importer.AcctAttrUserName, u.ScreenName,
	); err != nil {
		httputil.ServeError(w, r, fmt.Errorf("Error setting attribute: %v", err))
		return
	}
	http.Redirect(w, r, ctx.AccountURL(), http.StatusFound)
}

// oauthContext is used as a value type, wrapping a context and oauth information.
//
// TODO: move this up to pkg/importer?
type oauthContext struct {
	*context.Context
	client *oauth.Client
	creds  *oauth.Credentials
}

func (ctx oauthContext) doAPI(result interface{}, apiPath string, keyval ...string) error {
	if len(keyval)%2 == 1 {
		panic("Incorrect number of keyval arguments. must be even.")
	}
	form := url.Values{}
	for i := 0; i < len(keyval); i += 2 {
		if keyval[i+1] != "" {
			form.Set(keyval[i], keyval[i+1])
		}
	}
	fullURL := apiURL + apiPath
	res, err := ctx.doGet(fullURL, form)
	if err != nil {
		return err
	}
	err = httputil.DecodeJSON(res, result)
	if err != nil {
		return fmt.Errorf("could not parse response for %s: %v", fullURL, err)
	}
	return nil
}

func (ctx oauthContext) doGet(url string, form url.Values) (*http.Response, error) {
	if ctx.creds == nil {
		return nil, errors.New("No OAuth credentials. Not logged in?")
	}
	if ctx.client == nil {
		return nil, errors.New("No OAuth client.")
	}
	res, err := ctx.client.Get(ctx.HTTPClient(), ctx.creds, url, form)
	if err != nil {
		return nil, fmt.Errorf("Error fetching %s: %v", url, err)
	}
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Get request on %s failed with: %s", url, res.Status)
	}
	return res, nil
}

type tweetItem struct {
	Id        string `json:"id_str"`
	Text      string
	CreatedAt string `json:"created_at"`

	// One or both might be present:
	Geo         *geo    `json:"geo"`         // lat, long
	Coordinates *coords `json:"coordinates"` // geojson: long, lat
}

func (t *tweetItem) LatLong() (lat, long float64, ok bool) {
	if g := t.Geo; g != nil && len(g.Coordinates) == 2 {
		c := g.Coordinates
		if c[0] != 0 && c[1] != 0 {
			return c[0], c[1], true
		}
	}
	if g := t.Coordinates; g != nil && len(g.Coordinates) == 2 {
		c := g.Coordinates
		if c[0] != 0 && c[1] != 0 {
			return c[1], c[0], true
		}
	}
	return
}

type geo struct {
	Coordinates []float64 `json:"coordinates"` // lat,long
}

type coords struct {
	Coordinates []float64 `json:"coordinates"` // long,lat
}
