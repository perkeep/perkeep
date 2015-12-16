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
	"os"
	"path"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/httputil"
	"camlistore.org/pkg/importer"
	"camlistore.org/pkg/schema"
	"camlistore.org/pkg/schema/nodeattr"

	"camlistore.org/third_party/github.com/garyburd/go-oauth/oauth"

	"go4.org/ctxutil"
	"go4.org/syncutil"
)

const (
	apiURL                        = "https://api.twitter.com/1.1/"
	temporaryCredentialRequestURL = "https://api.twitter.com/oauth/request_token"
	resourceOwnerAuthorizationURL = "https://api.twitter.com/oauth/authorize"
	tokenRequestURL               = "https://api.twitter.com/oauth/access_token"
	userInfoAPIPath               = "account/verify_credentials.json"
	userTimeLineAPIPath           = "statuses/user_timeline.json"

	// runCompleteVersion is a cache-busting version number of the
	// importer code. It should be incremented whenever the
	// behavior of this importer is updated enough to warrant a
	// complete run.  Otherwise, if the importer runs to
	// completion, this version number is recorded on the account
	// permanode and subsequent importers can stop early.
	runCompleteVersion = "4"

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

func (im *imp) NeedsAPIKey() bool         { return true }
func (im *imp) SupportsIncremental() bool { return true }

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

var forceFullImport, _ = strconv.ParseBool(os.Getenv("CAMLI_TWITTER_FULL_IMPORT"))

func (im *imp) Run(ctx *importer.RunContext) error {
	clientId, secret, err := ctx.Credentials()
	if err != nil {
		return fmt.Errorf("no API credentials: %v", err)
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
		return errors.New("UserID hasn't been set by account setup.")
	}

	skipAPITweets, _ := strconv.ParseBool(os.Getenv("CAMLI_TWITTER_SKIP_API_IMPORT"))
	if !skipAPITweets {
		if err := r.importTweets(userID); err != nil {
			return err
		}
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

func (r *run) doAPI(result interface{}, apiPath string, keyval ...string) error {
	return importer.OAuthContext{
		r.Context,
		r.oauthClient,
		r.accessCreds}.PopulateJSONFromURL(result, apiURL+apiPath, keyval...)
}

func (r *run) importTweets(userID string) error {
	maxId := ""
	continueRequests := true

	tweetsNode, err := r.getTopLevelNode("tweets")
	if err != nil {
		return err
	}

	numTweets := 0
	sawTweet := map[string]bool{}

	// If attrs is changed, so should the expected responses accordingly for the
	// RoundTripper of MakeTestData (testdata.go).
	attrs := []string{
		"user_id", userID,
		"count", strconv.Itoa(tweetRequestLimit),
	}
	for continueRequests {
		select {
		case <-r.Done():
			r.errorf("Twitter importer: interrupted")
			return r.Err()
		default:
		}

		var resp []*apiTweetItem
		var err error
		if maxId == "" {
			log.Printf("Fetching tweets for userid %s", userID)
			err = r.doAPI(&resp, userTimeLineAPIPath, attrs...)
		} else {
			log.Printf("Fetching tweets for userid %s with max ID %s", userID, maxId)
			err = r.doAPI(&resp, userTimeLineAPIPath,
				append(attrs, "max_id", maxId)...)
		}
		if err != nil {
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

func tweetsFromZipFile(zf *zip.File) (tweets []*zipTweetItem, err error) {
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

	tweetsNode, err := r.getTopLevelNode("tweets")
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
func (r *run) importTweet(parent *importer.Object, tweet tweetItem, viaAPI bool) (dup bool, err error) {
	select {
	case <-r.Done():
		r.errorf("Twitter importer: interrupted")
		return false, r.Err()
	default:
	}
	id := tweet.ID()
	tweetNode, err := parent.ChildPathObject(id)
	if err != nil {
		return false, err
	}

	// Because the zip format and the API format differ a bit, and
	// might diverge more in the future, never use the zip content
	// to overwrite data fetched via the API. If we add new
	// support for different fields in the future, we might want
	// to revisit this decision.  Be wary of flip/flopping data if
	// modifying this, though.
	if tweetNode.Attr(attrImportMethod) == "api" && !viaAPI {
		return true, nil
	}

	// e.g. "2014-06-12 19:11:51 +0000"
	createdTime, err := timeParseFirstFormat(tweet.CreatedAt(), time.RubyDate, "2006-01-02 15:04:05 -0700")
	if err != nil {
		return false, fmt.Errorf("could not parse time %q: %v", tweet.CreatedAt(), err)
	}

	url := fmt.Sprintf("https://twitter.com/%s/status/%v",
		r.AccountNode().Attr(importer.AcctAttrUserName),
		id)

	attrs := []string{
		"twitterId", id,
		nodeattr.Type, "twitter.com:tweet",
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

	for i, m := range tweet.Media() {
		filename := m.BaseFilename()
		if tweetNode.Attr("camliPath:"+filename) != "" && (i > 0 || tweetNode.Attr("camliContentImage") != "") {
			// Don't re-import media we've already fetched.
			continue
		}
		tried, gotMedia := 0, false
		for _, mediaURL := range m.URLs() {
			tried++
			res, err := ctxutil.Client(r).Get(mediaURL)
			if err != nil {
				return false, fmt.Errorf("Error fetching %s for tweet %s : %v", mediaURL, url, err)
			}
			if res.StatusCode == http.StatusNotFound {
				continue
			}
			if res.StatusCode != 200 {
				return false, fmt.Errorf("HTTP status %d fetching %s for tweet %s", res.StatusCode, mediaURL, url)
			}
			if !viaAPI {
				log.Printf("For zip tweet %s, reading %v", url, mediaURL)
			}
			fileRef, err := schema.WriteFileFromReader(r.Host.Target(), filename, res.Body)
			res.Body.Close()
			if err != nil {
				return false, fmt.Errorf("Error fetching media %s for tweet %s: %v", mediaURL, url, err)
			}
			attrs = append(attrs, "camliPath:"+filename, fileRef.String())
			if i == 0 {
				attrs = append(attrs, "camliContentImage", fileRef.String())
			}
			log.Printf("Slurped %s as %s for tweet %s (%v)", mediaURL, fileRef.String(), url, tweetNode.PermanodeRef())
			gotMedia = true
			break
		}
		if !gotMedia && tried > 0 {
			return false, fmt.Errorf("All media URLs 404s for tweet %s", url)
		}
	}

	changes, err := tweetNode.SetAttrs2(attrs...)
	if err == nil && changes {
		log.Printf("Imported tweet %s", url)
	}
	return !changes, err
}

// The path be one of "tweets".
// In the future: "lists", "direct_messages", etc.
func (r *run) getTopLevelNode(path string) (*importer.Object, error) {
	acctNode := r.AccountNode()

	root := r.RootNode()
	rootTitle := fmt.Sprintf("%s's Twitter Data", acctNode.Attr(importer.AcctAttrUserName))
	log.Printf("root title = %q; want %q", root.Attr(nodeattr.Title), rootTitle)
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
	if err := ctx.PopulateJSONFromURL(&ui, apiURL+userInfoAPIPath); err != nil {
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
		err = fmt.Errorf("error getting OAuth client: %v", err)
		httputil.ServeError(w, r, err)
		return err
	}
	tempCred, err := oauthClient.RequestTemporaryCredentials(ctxutil.Client(ctx), ctx.CallbackURL(), nil)
	if err != nil {
		err = fmt.Errorf("Error getting temp cred: %v", err)
		httputil.ServeError(w, r, err)
		return err
	}
	if err := ctx.AccountNode.SetAttrs(
		importer.AcctAttrTempToken, tempCred.Token,
		importer.AcctAttrTempSecret, tempCred.Secret,
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
	tempToken := ctx.AccountNode.Attr(importer.AcctAttrTempToken)
	tempSecret := ctx.AccountNode.Attr(importer.AcctAttrTempSecret)
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
	oauthClient, err := ctx.NewOAuthClient(oAuthURIs)
	if err != nil {
		err = fmt.Errorf("error getting OAuth client: %v", err)
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
		httputil.ServeError(w, r, fmt.Errorf("Error getting request token: %v ", err))
		return
	}
	userid := vals.Get("user_id")
	if userid == "" {
		httputil.ServeError(w, r, fmt.Errorf("Couldn't get user id: %v", err))
		return
	}
	if err := ctx.AccountNode.SetAttrs(
		importer.AcctAttrAccessToken, tokenCred.Token,
		importer.AcctAttrAccessTokenSecret, tokenCred.Secret,
	); err != nil {
		httputil.ServeError(w, r, fmt.Errorf("Error setting token attributes: %v", err))
		return
	}

	u, err := getUserInfo(importer.OAuthContext{ctx.Context, oauthClient, tokenCred})
	if err != nil {
		httputil.ServeError(w, r, fmt.Errorf("Couldn't get user info: %v", err))
		return
	}
	if err := ctx.AccountNode.SetAttrs(
		importer.AcctAttrUserID, u.ID,
		importer.AcctAttrName, u.Name,
		importer.AcctAttrUserName, u.ScreenName,
		nodeattr.Title, fmt.Sprintf("%s's Twitter Account", u.ScreenName),
	); err != nil {
		httputil.ServeError(w, r, fmt.Errorf("Error setting attribute: %v", err))
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
}

type tweetMedia interface {
	URLs() []string // use first non-404 one
	BaseFilename() string
}

type apiTweetItem struct {
	Id           string   `json:"id_str"`
	TextStr      string   `json:"text"`
	CreatedAtStr string   `json:"created_at"`
	Entities     entities `json:"entities"`

	// One or both might be present:
	Geo         *geo    `json:"geo"`         // lat, long
	Coordinates *coords `json:"coordinates"` // geojson: long, lat
}

// zipTweetItem is like apiTweetItem, but twitter is annoying and the schema for the JSON inside zip files is slightly different.
type zipTweetItem struct {
	Id           string `json:"id_str"`
	TextStr      string `json:"text"`
	CreatedAtStr string `json:"created_at"`

	// One or both might be present:
	Geo         *geo        `json:"geo"`         // lat, long
	Coordinates *coords     `json:"coordinates"` // geojson: long, lat
	Entities    zipEntities `json:"entities"`
}

func (t *apiTweetItem) ID() string {
	if t.Id == "" {
		panic("empty id")
	}
	return t.Id
}

func (t *zipTweetItem) ID() string {
	if t.Id == "" {
		panic("empty id")
	}
	return t.Id
}

func (t *apiTweetItem) CreatedAt() string { return t.CreatedAtStr }
func (t *zipTweetItem) CreatedAt() string { return t.CreatedAtStr }

func (t *apiTweetItem) Text() string { return t.TextStr }
func (t *zipTweetItem) Text() string { return t.TextStr }

func (t *apiTweetItem) LatLong() (lat, long float64, ok bool) {
	return latLong(t.Geo, t.Coordinates)
}

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

func (t *apiTweetItem) Media() (ret []tweetMedia) {
	for _, m := range t.Entities.Media {
		ret = append(ret, m)
	}
	ret = append(ret, getImagesFromURLs(t.Entities.URLs)...)
	return
}

type geo struct {
	Coordinates []float64 `json:"coordinates"` // lat,long
}

type coords struct {
	Coordinates []float64 `json:"coordinates"` // long,lat
}

type entities struct {
	Media []*media     `json:"media"`
	URLs  []*urlEntity `json:"urls"`
}

type zipEntities struct {
	Media []*zipMedia  `json:"media"`
	URLs  []*urlEntity `json:"urls"`
}

// e.g.  {
//   "indices" : [ 105, 125 ],
//   "url" : "http:\/\/t.co\/gbGO8Qep",
//   "expanded_url" : "http:\/\/twitpic.com\/6mdqac",
//   "display_url" : "twitpic.com\/6mdqac"
// }
type urlEntity struct {
	URL         string `json:"url"`
	ExpandedURL string `json:"expanded_url"`
	DisplayURL  string `json:"display_url"`
}

var (
	twitpicRx = regexp.MustCompile(`\btwitpic\.com/(\w\w\w+)`)
	imgurRx   = regexp.MustCompile(`\bimgur\.com/(\w\w\w+)`)
)

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

// The Media entity from the Rest API. See also: zipMedia.
type media struct {
	Id            string               `json:"id_str"`
	IdNum         int64                `json:"id"`
	MediaURL      string               `json:"media_url"`
	MediaURLHTTPS string               `json:"media_url_https"`
	Sizes         map[string]mediaSize `json:"sizes"`
	Type_         string               `json:"type"`
}

// The Media entity from the zip file JSON. Similar but different to
// media. Thanks, Twitter.
type zipMedia struct {
	Id            string      `json:"id_str"`
	IdNum         int64       `json:"id"`
	MediaURL      string      `json:"media_url"`
	MediaURLHTTPS string      `json:"media_url_https"`
	Sizes         []mediaSize `json:"sizes"` // without a key! useless.
}

func (m *media) URLs() []string {
	u := m.baseURL()
	if u == "" {
		return nil
	}
	return []string{u + m.largestMediaSuffix(), u}
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

func (m *media) baseURL() string {
	if v := m.MediaURLHTTPS; v != "" {
		return v
	}
	return m.MediaURL
}

func (m *zipMedia) baseURL() string {
	if v := m.MediaURLHTTPS; v != "" {
		return v
	}
	return m.MediaURL
}

func (m *media) BaseFilename() string {
	return path.Base(m.baseURL())
}

func (m *zipMedia) BaseFilename() string {
	return path.Base(m.baseURL())
}

func (m *media) largestMediaSuffix() string {
	bestPixels := 0
	bestSuffix := ""
	for k, sz := range m.Sizes {
		if px := sz.W * sz.H; px > bestPixels {
			bestPixels = px
			bestSuffix = ":" + k
		}
	}
	return bestSuffix
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
