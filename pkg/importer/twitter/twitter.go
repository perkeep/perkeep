/*
Copyright 2014 The Perkeep Authors

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
package twitter // import "perkeep.org/pkg/importer/twitter"

import (
	"errors"
	"fmt"
	"html"
	"log"
	"net/http"
	"os"
	"path"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"perkeep.org/internal/httputil"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/importer"
	"perkeep.org/pkg/schema"
	"perkeep.org/pkg/schema/nodeattr"

	"github.com/garyburd/go-oauth/oauth"

	"go4.org/ctxutil"
	"go4.org/syncutil"
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
	runCompleteVersion = "5"

	// acctAttrImportLikes specifies an optional attribute for the account permanode.
	// If set to true likes are imported via the twitter API.
	// You can enable importing likes like this:
	//   $ pk-put attr <acct-permanode> twitterImportLikes true
	// ... and re-do an import.
	acctAttrImportLikes = "twitterImportLikes"

	// Per-tweet note of how we imported it: either "zip" or "api"
	attrImportMethod = "twitterImportMethod"

	// acctAttrNewestTweetID is the ID of the newest of the account's
	// tweets imported through the API, after which an incremental run
	// asks for its tweets.
	acctAttrNewestTweetID = "twitterNewestTweetID"

	tweetsAtOnce = 20 // how many tweets to import at once

	// A tweet is stored as a permanode with the "twitter.com:tweet" camliNodeType value.
	nodeTypeTweet = "twitter.com:tweet"

	// A like is stored as a permanode with the "twitter.com:like" camliNodeType value.
	nodeTypeLike = "twitter.com:like"
)

var oAuthURIs = importer.OAuthURIs{
	TemporaryCredentialRequestURI: temporaryCredentialRequestURL,
	ResourceOwnerAuthorizationURI: resourceOwnerAuthorizationURL,
	TokenRequestURI:               tokenRequestURL,
}

func init() {
	importer.Register("twitter", &imp{})
}

var _ importer.ImporterSetupHTMLer = (*imp)(nil)

type imp struct {
	importer.OAuth1 // for CallbackRequestAccount and CallbackURLParameters
}

func (*imp) Properties() importer.Properties {
	return importer.Properties{
		Title:       "Twitter",
		Description: "import tweets and media from tweets",
		// TODO: doc URL for linking to info on historical tweets from ZIP files beyond API limit
		SupportsIncremental: true,
		NeedsAPIKey:         true,
	}
}

func (im *imp) IsAccountReady(acctNode *importer.Object) (ok bool, err error) {
	if acctNode.Attr(importer.AcctAttrUserID) != "" && acctNode.Attr(importer.AcctAttrAccessToken) != "" {
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
<!-- TODO(mpl): use CSS to style it to 80 chars wide instead of doing it in source -->
<p>
Click "Create your Twitter application".You should be redirected to the</br>
Application Management page of your newly created application.</br>
Go to the "Keys and Access Tokens" tab. Copy the "Consumer Key (API Key)" and</br>
"Consumer Secret (API Secret)" into the "Client ID" and "Client Secret" boxes</br>
above.
</p>
<p>
The X API serves only an account's most recent 3200 tweets (including
retweets), and charges for each it reads. To import all of them, and your
likes, profile and follows, download your archive from X ("Settings and
privacy", "Your account", "Download an archive of your data"), and import
its ZIP below, as an upload or from a file source. Then the account's runs
import only its new tweets through the API.
</p>
<p>
To import likes through the API as well, which it charges for, run
"pk-put attr &lt;acct-permanode&gt; twitterImportLikes true".
</p>
`, base, base+"/callback")
}

// A run is our state for a given run of the importer.
type run struct {
	*importer.RunContext
	im          *imp
	incremental bool // whether we've completed a run in the past

	oauthClient *oauth.Client      // No need to guard, used read-only.
	accessCreds *oauth.Credentials // No need to guard, used read-only.

	mu        sync.Mutex // guards anyErr and postsRead
	anyErr    bool
	postsRead int // tweets the API returned, by which it charges
}

var forceFullImport, _ = strconv.ParseBool(os.Getenv("CAMLI_TWITTER_FULL_IMPORT"))

func (im *imp) Run(ctx *importer.RunContext) error {
	clientId, secret, err := ctx.Credentials()
	if err != nil {
		return fmt.Errorf("no API credentials: %w", err)
	}
	acctNode := ctx.AccountNode()
	accessToken := acctNode.Attr(importer.AcctAttrAccessToken)
	accessSecret := acctNode.Attr(importer.AcctAttrAccessTokenSecret)
	if accessToken == "" || accessSecret == "" {
		return errors.New("access credentials not found")
	}
	r := &run{
		RunContext:  ctx,
		im:          im,
		incremental: !forceFullImport && acctNode.Attr(importer.AcctAttrCompletedVersion) == runCompleteVersion,

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
		return errors.New("userID hasn't been set by account setup")
	}

	defer func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.Logf("the API returned %d tweets this run", r.postsRead)
	}()
	skipAPITweets, _ := strconv.ParseBool(os.Getenv("CAMLI_TWITTER_SKIP_API_IMPORT"))
	if !skipAPITweets {
		if err := r.importTweets(userID, false); err != nil {
			return err
		}
	}

	// Reload the account node to read the attributes set on it from
	// outside the importer, such as with pk-put, but write to it through
	// ctx.AccountNode, which later runs read.
	acctNode, err = ctx.Host.ObjectFromRef(acctNode.PermanodeRef())
	if err != nil {
		return fmt.Errorf("error reloading account node: %w", err)
	}
	importLikes, err := strconv.ParseBool(acctNode.Attr(acctAttrImportLikes))
	if err == nil && importLikes {
		if err := r.importTweets(userID, true); err != nil {
			return err
		}
	}

	r.mu.Lock()
	anyErr := r.anyErr
	r.mu.Unlock()

	if !anyErr {
		if err := ctx.AccountNode().SetAttrs(importer.AcctAttrCompletedVersion, runCompleteVersion); err != nil {
			return err
		}
	}

	return nil
}

func (r *run) errorf(format string, args ...any) {
	r.Logf(format, args...)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.anyErr = true
}

// doAPIv2 gets the API v2 path apiPath, with the query parameters keyval,
// into result.
func (r *run) doAPIv2(result any, apiPath string, keyval ...string) error {
	return importer.OAuthContext{
		Ctx:    r.Context(),
		Client: r.oauthClient,
		Creds:  r.accessCreds,
	}.PopulateJSONFromURL(result, http.MethodGet, apiV2URL+apiPath, keyval...)
}

func (r *run) addPostsRead(n int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.postsRead += n
}

// importTweets imports the tweets posted by userID, including retweets,
// or, if likes, the tweets userID liked, through the API v2.
//
// The API serves only an account's most recent 3200 tweets, newest first.
// An incremental run of tweets asks for only those since the newest an
// earlier run imported, as the API charges by the tweet read; one of likes,
// which the API can't ask for so, stops at a page of likes all imported.
func (r *run) importTweets(userID string, likes bool) error {
	importType := "tweets"
	if likes {
		importType = "likes"
	}
	tweetsNode, err := r.getTopLevelNode(importType)
	if err != nil {
		return err
	}
	// The API gives no count of the tweets to come, so the bar is a
	// running count.
	bar := r.Progress().Bar(importType, "Importing "+importType)

	attrs := []string{
		"max_results", strconv.Itoa(apiPageSize),
		"tweet.fields", apiTweetFields,
		"expansions", apiExpansions,
		"media.fields", apiMediaFields,
	}
	acctNode := r.AccountNode()
	if !likes && r.incremental {
		if id := acctNode.Attr(acctAttrNewestTweetID); id != "" {
			attrs = append(attrs, "since_id", id)
		}
	}

	var (
		numTweets int
		newestID  string // of the tweets, the first page's newest
		nextToken string
		sawTweet  = map[string]bool{}
	)
	for page := 0; ; page++ {
		if err := r.Context().Err(); err != nil {
			r.errorf("interrupted")
			return err
		}
		pageAttrs := attrs
		if nextToken != "" {
			pageAttrs = append(attrs[:len(attrs):len(attrs)], "pagination_token", nextToken)
		}
		r.Logf("fetching page %d of %s for userid %s", page+1, importType, userID)
		var resp apiTweetsPage
		if err := r.doAPIv2(&resp, apiTweetsPath(userID, likes), pageAttrs...); err != nil {
			return err
		}
		r.addPostsRead(len(resp.Data))
		if page == 0 {
			newestID = resp.Meta.NewestID
		}

		var (
			newThisBatch = 0
			allDupMu     sync.Mutex
			allDups      = true
			gate         = syncutil.NewGate(tweetsAtOnce)
			grp          syncutil.Group
		)
		for _, tweet := range resp.tweets(likes) {
			// Dup-suppression.
			if sawTweet[tweet.Id] {
				continue
			}
			sawTweet[tweet.Id] = true
			newThisBatch++

			gate.Start()
			grp.Go(func() error {
				defer gate.Done()
				dup, err := r.importTweet(tweetsNode, tweet, true)
				if err != nil {
					r.errorf("error importing tweet %s %v", tweet.Id, err)
					return err
				}
				if dup {
					bar.Skip(1)
				} else {
					allDupMu.Lock()
					allDups = false
					allDupMu.Unlock()
					bar.Add(1)
				}
				return nil
			})
		}
		if err := grp.Err(); err != nil {
			return err
		}
		numTweets += newThisBatch
		r.Logf("imported %d %s this batch; %d total.", newThisBatch, importType, numTweets)
		if likes && r.incremental && allDups {
			r.Logf("incremental import found end batch")
			break
		}
		nextToken = resp.Meta.NextToken
		if nextToken == "" || newThisBatch == 0 {
			break
		}
	}
	bar.Finish()
	if !likes && newestID != "" {
		if err := acctNode.SetAttrs(acctAttrNewestTweetID, newestID); err != nil {
			return err
		}
	}
	r.Logf("imported %d %s through the API", numTweets, importType)
	return nil
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

// importTweet imports tweet, as a child of parent, and reports whether it
// was unchanged. viaAPI is true if it came via the API, or false if it came
// via an archive.
func (r *run) importTweet(parent *importer.Object, tweet tweetItem, viaAPI bool) (dup bool, err error) {
	select {
	case <-r.Context().Done():
		r.errorf("Twitter importer: interrupted")
		return false, r.Context().Err()
	default:
	}
	tweetNode, err := parent.ChildPathObject(tweet.ID())
	if err != nil {
		return false, err
	}
	return r.importTweetNode(tweetNode, tweet, viaAPI)
}

// importTweetNode imports tweet as tweetNode, as importTweet does.
func (r *run) importTweetNode(tweetNode *importer.Object, tweet tweetItem, viaAPI bool) (dup bool, err error) {
	attrs, err := r.tweetAttrs(tweetNode, tweet, viaAPI)
	if err != nil || len(attrs) == 0 {
		return true, err
	}
	changes, err := tweetNode.SetAttrs2(attrs...)
	if err == nil && changes {
		r.Logf("imported tweet %s", tweetNode.Attr(nodeattr.URL))
	}
	return !changes, err
}

// tweetAttrs returns the attributes of tweet, of tweetNode, to set: its
// media's that the node lacks, which it stores, and its others, unless the
// node was imported through the API and tweet wasn't.
func (r *run) tweetAttrs(tweetNode *importer.Object, tweet tweetItem, viaAPI bool) ([]string, error) {
	id := tweet.ID()
	url := fmt.Sprintf("https://twitter.com/%s/status/%v",
		r.AccountNode().Attr(importer.AcctAttrUserName),
		id)

	mediaAttrs, err := r.tweetMediaAttrs(tweetNode, tweet, url, viaAPI)
	if err != nil {
		return nil, err
	}

	// Because the archive format and the API format differ a bit, and
	// might diverge more in the future, never use the archive content
	// to overwrite data fetched via the API, but add the media the
	// API's import lacks, such as the videos of archives. If we add new
	// support for different fields in the future, we might want
	// to revisit this decision.  Be wary of flip/flopping data if
	// modifying this, though.
	if tweetNode.Attr(attrImportMethod) == "api" && !viaAPI {
		return mediaAttrs, nil
	}

	// e.g. "2014-06-12 19:11:51 +0000"
	createdTime, err := timeParseFirstFormat(tweet.CreatedAt(), time.RFC3339, time.RubyDate, "2006-01-02 15:04:05 -0700")
	if err != nil {
		return nil, fmt.Errorf("could not parse time %q: %w", tweet.CreatedAt(), err)
	}

	nodeType := nodeTypeTweet
	if tweet.Liked() {
		nodeType = nodeTypeLike
	}

	attrs := []string{
		"twitterId", id,
		nodeattr.Type, nodeType,
		nodeattr.StartDate, schema.RFC3339FromTime(createdTime),
		nodeattr.Content, tweet.Text(),
		nodeattr.URL, url,
	}
	if lat, long, ok := tweet.LatLong(); ok {
		attrs = append(attrs,
			nodeattr.Latitude, fmt.Sprint(lat),
			nodeattr.Longitude, fmt.Sprint(long),
		)
	}
	if viaAPI {
		attrs = append(attrs, attrImportMethod, "api")
	} else {
		attrs = append(attrs, attrImportMethod, "zip")
	}
	return append(attrs, mediaAttrs...), nil
}

// tweetMediaAttrs returns the attributes of the media of tweet, of URL
// url, that tweetNode lacks: those an archive's run stored, and those it
// fetches.
func (r *run) tweetMediaAttrs(tweetNode *importer.Object, tweet tweetItem, url string, viaAPI bool) (attrs []string, err error) {
	hasImage := tweetNode.Attr(nodeattr.CamliContentImage) != ""
	for i, m := range tweet.Media() {
		if sm, ok := m.(*storedMedia); ok {
			if sm.ref.Valid() {
				attrs = append(attrs, "camliPath:"+sm.name, sm.ref.String())
				if sm.photo && !hasImage {
					attrs = append(attrs, nodeattr.CamliContentImage, sm.ref.String())
					hasImage = true
				}
			}
			continue
		}
		filename := m.BaseFilename()
		if tweetNode.Attr("camliPath:"+filename) != "" && (i > 0 || hasImage) {
			// Don't re-import media we've already fetched.
			continue
		}
		fileRef, err := r.fetchMedia(m, filename, url, viaAPI)
		if err != nil {
			switch m.(type) {
			case twitpicImage, imgurImage:
				// Images of the tweet's links, on sites that are
				// gone, or limit how many are fetched, aren't the
				// tweet's own.
				r.Logf("skipping an image of a link of tweet %s: %v", url, err)
				continue
			}
			return nil, err
		}
		if !fileRef.Valid() {
			continue
		}
		attrs = append(attrs, "camliPath:"+filename, fileRef.String())
		if i == 0 {
			attrs = append(attrs, nodeattr.CamliContentImage, fileRef.String())
			hasImage = true
		}
		r.Logf("slurped %s as %s for tweet %s (%v)", filename, fileRef.String(), url, tweetNode.PermanodeRef())
	}
	return attrs, nil
}

// fetchMedia fetches m, of tweet of URL url, and stores it as a file named
// filename, from the first of its URLs that's found, or returns a zero
// ref if it has none.
func (r *run) fetchMedia(m tweetMedia, filename, url string, viaAPI bool) (blob.Ref, error) {
	urls := m.URLs()
	for _, mediaURL := range urls {
		res, err := ctxutil.Client(r.Context()).Get(mediaURL)
		if err != nil {
			return blob.Ref{}, fmt.Errorf("Error fetching %s for tweet %s: %w", mediaURL, url, err)
		}
		if res.StatusCode == http.StatusNotFound {
			res.Body.Close()
			continue
		}
		if res.StatusCode != 200 {
			res.Body.Close()
			return blob.Ref{}, fmt.Errorf("HTTP status %d fetching %s for tweet %s", res.StatusCode, mediaURL, url)
		}
		if !viaAPI {
			r.Logf("for zip tweet %s, reading %v", url, mediaURL)
		}
		fileRef, err := schema.WriteFileFromReader(r.Context(), r.Host.Target(), filename, res.Body)
		res.Body.Close()
		if err != nil {
			return blob.Ref{}, fmt.Errorf("Error fetching media %s for tweet %s: %w", mediaURL, url, err)
		}
		return fileRef, nil
	}
	if len(urls) > 0 {
		return blob.Ref{}, fmt.Errorf("All media URLs 404s for tweet %s", url)
	}
	return blob.Ref{}, nil
}

// path may be of: "tweets". (TODO: "lists", "direct_messages", etc.)
func (r *run) getTopLevelNode(path string) (*importer.Object, error) {
	acctNode := r.AccountNode()

	root := r.RootNode()
	rootTitle := fmt.Sprintf("%s's Twitter Data", acctNode.Attr(importer.AcctAttrUserName))
	if err := root.SetAttr(nodeattr.Title, rootTitle); err != nil {
		return nil, err
	}

	obj, err := root.ChildPathObject(path)
	if err != nil {
		return nil, err
	}
	var title string
	switch path {
	case "tweets":
		title = fmt.Sprintf("%s's Tweets", acctNode.Attr(importer.AcctAttrUserName))
	case "likes":
		title = fmt.Sprintf("%s's Likes", acctNode.Attr(importer.AcctAttrUserName))
	}
	return obj, obj.SetAttr(nodeattr.Title, title)
}

type userInfo struct {
	ID         string `json:"id_str"`
	ScreenName string `json:"screen_name"`
	Name       string `json:"name,omitempty"`
}

func getUserInfo(ctx importer.OAuthContext) (userInfo, error) {
	var ui userInfo
	if err := ctx.PopulateJSONFromURL(&ui, http.MethodGet, apiURL+userInfoAPIPath); err != nil {
		return ui, err
	}
	if ui.ID == "" {
		return ui, fmt.Errorf("No userid returned")
	}
	return ui, nil
}

func (im *imp) ServeSetup(w http.ResponseWriter, r *http.Request, ctx *importer.SetupContext) error {
	oauthClient, err := ctx.NewOAuthClient(oAuthURIs)
	if err != nil {
		err = fmt.Errorf("error getting OAuth client: %w", err)
		httputil.ServeError(w, r, err)
		return err
	}
	tempCred, err := oauthClient.RequestTemporaryCredentials(ctxutil.Client(ctx), ctx.CallbackURL(), nil)
	if err != nil {
		err = fmt.Errorf("Error getting temp cred: %w", err)
		httputil.ServeError(w, r, err)
		return err
	}
	if err := ctx.AccountNode.SetAttrs(
		importer.AcctAttrTempToken, tempCred.Token,
		importer.AcctAttrTempSecret, tempCred.Secret,
	); err != nil {
		err = fmt.Errorf("Error saving temp creds: %w", err)
		httputil.ServeError(w, r, err)
		return err
	}

	authURL := oauthClient.AuthorizationURL(tempCred, nil)
	http.Redirect(w, r, authURL, http.StatusFound)
	return nil
}

func (im *imp) ServeCallback(w http.ResponseWriter, r *http.Request, ctx *importer.SetupContext) {
	tempToken := ctx.AccountNode.Attr(importer.AcctAttrTempToken)
	tempSecret := ctx.AccountNode.Attr(importer.AcctAttrTempSecret)
	if tempToken == "" || tempSecret == "" {
		log.Printf("twitter: no temp creds in callback")
		httputil.BadRequestError(w, "no temp creds in callback")
		return
	}
	if tempToken != r.FormValue("oauth_token") {
		log.Printf("twitter: unexpected oauth_token in callback")
		httputil.BadRequestError(w, "unexpected oauth_token")
		return
	}
	oauthClient, err := ctx.NewOAuthClient(oAuthURIs)
	if err != nil {
		err = fmt.Errorf("error getting OAuth client: %w", err)
		httputil.ServeError(w, r, err)
		return
	}
	tokenCred, vals, err := oauthClient.RequestToken(
		ctxutil.Client(ctx),
		&oauth.Credentials{
			Token:  tempToken,
			Secret: tempSecret,
		},
		r.FormValue("oauth_verifier"),
	)
	if err != nil {
		httputil.ServeError(w, r, fmt.Errorf("Error getting request token: %w", err))
		return
	}
	userid := vals.Get("user_id")
	if userid == "" {
		httputil.ServeError(w, r, fmt.Errorf("Couldn't get user id: %w", err))
		return
	}
	if err := ctx.AccountNode.SetAttrs(
		importer.AcctAttrAccessToken, tokenCred.Token,
		importer.AcctAttrAccessTokenSecret, tokenCred.Secret,
	); err != nil {
		httputil.ServeError(w, r, fmt.Errorf("Error setting token attributes: %w", err))
		return
	}

	u, err := getUserInfo(importer.OAuthContext{Ctx: ctx.Context, Client: oauthClient, Creds: tokenCred})
	if err != nil {
		httputil.ServeError(w, r, fmt.Errorf("Couldn't get user info: %w", err))
		return
	}
	if err := ctx.AccountNode.SetAttrs(
		importer.AcctAttrUserID, u.ID,
		importer.AcctAttrName, u.Name,
		importer.AcctAttrUserName, u.ScreenName,
		nodeattr.Title, fmt.Sprintf("%s's Twitter Account", u.ScreenName),
	); err != nil {
		httputil.ServeError(w, r, fmt.Errorf("Error setting attribute: %w", err))
		return
	}
	http.Redirect(w, r, ctx.AccountURL(), http.StatusFound)
}

type tweetItem interface {
	ID() string
	LatLong() (lat, long float64, ok bool)
	CreatedAt() string
	Text() string
	Media() []tweetMedia
	Liked() bool
}

type tweetMedia interface {
	URLs() []string // use first non-404 one
	BaseFilename() string
}

// zipTweetItem is a tweet of an archive's zip file.
type zipTweetItem struct {
	Id           string `json:"id_str"`
	TextStr      string `json:"text"`
	CreatedAtStr string `json:"created_at"`

	// One or both might be present:
	Geo         *geo        `json:"geo"`         // lat, long
	Coordinates *coords     `json:"coordinates"` // geojson: long, lat
	Entities    zipEntities `json:"entities"`
}

func (t *zipTweetItem) ID() string {
	if t.Id == "" {
		panic("empty id")
	}
	return t.Id
}

func (t *zipTweetItem) CreatedAt() string { return t.CreatedAtStr }

func (t *zipTweetItem) Text() string { return html.UnescapeString(t.TextStr) }

func (t *zipTweetItem) LatLong() (lat, long float64, ok bool) {
	return latLong(t.Geo, t.Coordinates)
}

func latLong(g *geo, c *coords) (lat, long float64, ok bool) {
	if g != nil && len(g.Coordinates) == 2 {
		co := g.Coordinates
		if co[0] != 0 && co[1] != 0 {
			return co[0], co[1], true
		}
	}
	if c != nil && len(c.Coordinates) == 2 {
		co := c.Coordinates
		if co[0] != 0 && co[1] != 0 {
			return co[1], co[0], true
		}
	}
	return
}

func (t *zipTweetItem) Media() (ret []tweetMedia) {
	for _, m := range t.Entities.Media {
		ret = append(ret, m)
	}
	ret = append(ret, getImagesFromURLs(t.Entities.URLs)...)
	return
}

func (t *zipTweetItem) Liked() bool { return false }

type geo struct {
	Coordinates []float64 `json:"coordinates"` // lat,long
}

type coords struct {
	Coordinates []float64 `json:"coordinates"` // long,lat
}

type zipEntities struct {
	Media []*zipMedia  `json:"media"`
	URLs  []*urlEntity `json:"urls"`
}

//	e.g.  {
//	  "indices" : [ 105, 125 ],
//	  "url" : "http:\/\/t.co\/gbGO8Qep",
//	  "expanded_url" : "http:\/\/twitpic.com\/6mdqac",
//	  "display_url" : "twitpic.com\/6mdqac"
//	}
type urlEntity struct {
	URL         string `json:"url"`
	ExpandedURL string `json:"expanded_url"`
	DisplayURL  string `json:"display_url"`
}

var imgurRx = regexp.MustCompile(`\bimgur\.com/(\w\w\w+)`)

func getImagesFromURLs(urls []*urlEntity) (ret []tweetMedia) {
	// TODO: extract these regexps from tweet text too. Happens in
	// a few cases I've seen in my history.
	for _, u := range urls {
		if strings.HasPrefix(u.DisplayURL, "twitpic.com") {
			ret = append(ret, twitpicImage(strings.TrimPrefix(u.DisplayURL, "twitpic.com/")))
			continue
		}
		if m := imgurRx.FindStringSubmatch(u.DisplayURL); m != nil {
			ret = append(ret, imgurImage(m[1]))
			continue
		}
	}
	return
}

// zipMedia is a photo of a tweet of an archive's zip file.
type zipMedia struct {
	Id            string      `json:"id_str"`
	IdNum         int64       `json:"id"`
	MediaURL      string      `json:"media_url"`
	MediaURLHTTPS string      `json:"media_url_https"`
	Sizes         []mediaSize `json:"sizes"` // without a key! useless.
}

func (m *zipMedia) URLs() []string {
	// We don't get any suffix names, so just try some common
	// ones. The first non-404 will be used:
	u := m.baseURL()
	if u == "" {
		return nil
	}
	return []string{
		u + ":large",
		u,
	}
}

func (m *zipMedia) baseURL() string {
	if v := m.MediaURLHTTPS; v != "" {
		return v
	}
	return m.MediaURL
}

func (m *zipMedia) BaseFilename() string {
	return path.Base(m.baseURL())
}

type mediaSize struct {
	W      int    `json:"w"`
	H      int    `json:"h"`
	Resize string `json:"resize"`
}

// An image from twitpic.
type twitpicImage string

func (im twitpicImage) BaseFilename() string { return string(im) }

func (im twitpicImage) URLs() []string {
	return []string{"https://twitpic.com/show/large/" + string(im)}
}

// An image from imgur
type imgurImage string

func (im imgurImage) BaseFilename() string { return string(im) }

func (im imgurImage) URLs() []string {
	// Imgur ignores the suffix if it's .gif, .png, or .jpg. So just pick .gif.
	// The actual content will be returned.
	return []string{"https://i.imgur.com/" + string(im) + ".gif"}
}
