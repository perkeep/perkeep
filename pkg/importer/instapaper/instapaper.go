/*
Copyright 2018 The Perkeep Authors

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

// Package instapaper implements a instapaper.com importer.
package instapaper // import "perkeep.org/pkg/importer/instapaper"

import (
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/garyburd/go-oauth/oauth"
	"go4.org/ctxutil"
	"go4.org/syncutil"

	"perkeep.org/internal/httputil"
	"perkeep.org/pkg/importer"
	"perkeep.org/pkg/schema"
	"perkeep.org/pkg/schema/nodeattr"
	"perkeep.org/pkg/search"
)

func init() {
	importer.Register("instapaper", &imp{})
}

type user struct {
	UserId   int    `json:"user_id"`
	Username string `json:"username"`
}

type folder struct {
	Title    string
	FolderId json.Number `json:"folder_id"`
}

type bookmark struct {
	Hash              string  `json:"hash"`
	Description       string  `json:"description"`
	BookmarkId        int     `json:"bookmark_id"`
	PrivateSource     string  `json:"private_source"`
	Title             string  `json:"title"`
	Url               string  `json:"url"`
	ProgressTimestamp int     `json:"progress_timestamp"`
	Time              int     `json:"time"`
	Progress          float64 `json:"progress"`
	Starred           string  `json:"starred"`
}

type highlight struct {
	HighlightId int    `json:"highlight_id"`
	Text        string `json:"text"`
	Note        string `json:"note"`
	BookmarkId  int    `json:"bookmark_id"`
	Time        int    `json:"time"`
	Position    int    `json:"position"`
}

const (
	// Import Types
	nodeTypeBookmark  = "instapaper.com:bookmark"
	nodeTypeHighlight = "instapaper.com:highlight"

	// Import Attributes
	attrBookmarkId = "instapaper.com:bookmarkId"
	attrUrl        = "instapaper.com:url"
	// Progress is the amount of the bookmark text Instapaper says you've already read.
	attrProgress = "instapaper.com:progress"
	// ProgressTimestamp is the date/time a user last read a portion or all of the bookmark's text.
	attrProgressTimestamp = "instapaper.com:progressTimestamp"

	requestLimit       = "500" // max number of bookmarks that Instapaper will return
	bookmarksAtOnce    = 20    // how many bookmarks to import at once
	runCompleteVersion = "1"

	// API URLs
	tokenRequestURL         = "https://www.instapaper.com/api/1/oauth/access_token"
	verifyUserRequestURL    = "https://www.instapaper.com/api/1/account/verify_credentials"
	bookmarkListRequestURL  = "https://www.instapaper.com/api/1/bookmarks/list"
	bookmarkTextRequestURL  = "https://www.instapaper.com/api/1/bookmarks/get_text"
	foldersListRequestURL   = "https://www.instapaper.com/api/1.1/folders/list"
	highlightListRequestURL = "https://www.instapaper.com/api/1.1/bookmarks/%d/highlights"
)

var (
	logger = log.New(os.Stderr, "instapaper.com: ", log.LstdFlags)
)

type imp struct {
	importer.OAuth1 // for CallbackRequestAccount and CallbackURLParameters
}

func (*imp) Properties() importer.Properties {
	return importer.Properties{
		Title:               "Instapaper",
		Description:         "Import full text bookmarks and highlights from an Instapaper account",
		NeedsAPIKey:         true,
		SupportsIncremental: true,
	}
}

func (*imp) IsAccountReady(acct *importer.Object) (ready bool, err error) {
	return acct.Attr(importer.AcctAttrAccessToken) != "" && acct.Attr(importer.AcctAttrUserID) != "", nil
}

func (*imp) SummarizeAccount(acct *importer.Object) string {
	userID := acct.Attr(importer.AcctAttrUserID)
	if userID == "" {
		return "Not configured"
	}
	return userID
}

func (imp) ServeSetup(w http.ResponseWriter, r *http.Request, ctx *importer.SetupContext) error {
	return tmpl.ExecuteTemplate(w, "serveSetup", ctx)
}

var tmpl = template.Must(template.New("root").Parse(`
{{define "serveSetup"}}
<h1>Configuring Instapaper Account</h1>
<h3>If your Instapaper account does not have a password, leave that field blank. However, a username is required. Passwords are not stored at all and are only used to retrieve an access token.</h3>
<form method="get" action="{{.CallbackURL}}">
  <input type="hidden" name="acct" value="{{.AccountNode.PermanodeRef}}">
  <table border=0 cellpadding=3>
  <tr><td align=right>Username</td><td><input name="username" size=50 required></td></tr>
  <tr><td align=right>Password</td><td><input name="password" type="password" size=50></td></tr>
  <tr><td align=right></td><td><input type="submit" value="Add"></td></tr>
  </table>
</form>
{{end}}
`))

var _ importer.ImporterSetupHTMLer = (*imp)(nil)

func (im *imp) AccountSetupHTML(host *importer.Host) string {
	return "<h1>Configuring Instapaper</h1><p>To get an OAuth client ID and secret, <a target=\"_blank\" href=\"https://www.instapaper.com/main/request_oauth_consumer_token\">fill this out</a>. You should receive an email response from Instapaper with the Client ID and Client Secret that you should use in the form above.</p>"
}

func (im *imp) ServeCallback(w http.ResponseWriter, r *http.Request, ctx *importer.SetupContext) {
	username := r.FormValue("username")
	password := r.FormValue("password")

	// We have to assume password can be blank as Instapaper does not require a password
	if username == "" {
		httputil.BadRequestError(w, "Expected a username")
		return
	}

	clientID, secret, err := ctx.Credentials()
	if err != nil {
		httputil.ServeError(w, r, fmt.Errorf("Credentials error: %v", err))
		return
	}

	oauthClient := &oauth.Client{
		TokenRequestURI: tokenRequestURL,
		Credentials: oauth.Credentials{
			Token:  clientID,
			Secret: secret,
		},
	}
	creds, _, err := oauthClient.RequestTokenXAuth(ctxutil.Client(ctx), nil, username, password)
	if err != nil {
		httputil.ServeError(w, r, fmt.Errorf("Failed to get access token: %v", err))
		return
	}

	user, err := getUserInfo(importer.OAuthContext{Ctx: ctx.Context, Client: oauthClient, Creds: creds})
	if err != nil {
		httputil.ServeError(w, r, fmt.Errorf("Failed to verify credentials: %v", err))
		return
	}

	if err := ctx.AccountNode.SetAttrs(
		nodeattr.Title, fmt.Sprintf("Instapaper account: %s", user.Username),
		importer.AcctAttrAccessToken, creds.Token,
		importer.AcctAttrAccessTokenSecret, creds.Secret,
		importer.AcctAttrUserName, user.Username,
		importer.AcctAttrUserID, fmt.Sprint(user.UserId),
	); err != nil {
		httputil.ServeError(w, r, fmt.Errorf("Error setting attributes: %v", err))
		return
	}
	http.Redirect(w, r, ctx.AccountURL(), http.StatusFound)
}

func (im *imp) Run(ctx *importer.RunContext) (err error) {
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
	userID := acctNode.Attr(importer.AcctAttrUserID)
	if userID == "" {
		return errors.New("userID hasn't been set by account setup")
	}
	r := &run{
		RunContext:  ctx,
		im:          im,
		incremental: acctNode.Attr(importer.AcctAttrCompletedVersion) == runCompleteVersion,
		oauthClient: &oauth.Client{
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
	folders, err := r.getFolders()
	if err != nil {
		return err
	}
	if err := r.importBookmarks(userID, folders); err != nil {
		return err
	}

	return acctNode.SetAttrs(importer.AcctAttrCompletedVersion, runCompleteVersion)
}

type run struct {
	*importer.RunContext
	im          *imp
	incremental bool

	oauthClient *oauth.Client
	accessCreds *oauth.Credentials

	mu      sync.Mutex
	txtReqs []txtReq
}

func getUserInfo(ctx importer.OAuthContext) (*user, error) {
	var ui []user
	if err := ctx.PopulateJSONFromURL(&ui, http.MethodPost, verifyUserRequestURL); err != nil {
		return nil, err
	}
	if ui[0].UserId == 0 {
		return nil, errors.New("no user returned")
	}
	return &ui[0], nil
}

func parseFilename(t string, id string) string {
	return fmt.Sprintf("%v_%v.html", strings.Replace(t, "/", "-", -1), id)
}

func (r *run) findExistingBookmark(bookmarkId string) (*importer.Object, error) {
	res, err := r.Host.Searcher().Query(r.Context(), &search.SearchQuery{
		Constraint: &search.Constraint{
			Permanode: &search.PermanodeConstraint{
				Attr:  attrBookmarkId,
				Value: bookmarkId,
			},
		},
		Describe: &search.DescribeRequest{
			Depth: 1,
		},
	})
	if err != nil {
		return nil, err
	}
	if res.Describe == nil {
		return nil, os.ErrNotExist
	}
	for _, resBlob := range res.Blobs {
		br := resBlob.Blob
		desBlob, ok := res.Describe.Meta[br.String()]
		if !ok || desBlob.Permanode == nil {
			continue
		}
		return r.Host.ObjectFromRef(br)
	}
	return nil, os.ErrNotExist
}

func (r *run) getFolders() ([]folder, error) {
	var folders []folder
	if err := r.doAPI(&folders, foldersListRequestURL); err != nil {
		return nil, err
	}
	return append(folders,
		folder{Title: "Unread", FolderId: "unread"},
		folder{Title: "Starred", FolderId: "starred"},
		folder{Title: "Archive", FolderId: "archive"},
	), nil
}

type txtReq struct {
	bmNode *importer.Object
	bm     *bookmark
}

func (r *run) importBookmarks(userID string, folders []folder) error {
	bsParent, err := r.getTopLevelNode("bookmarks")
	if err != nil {
		return err
	}
	hsParent, err := r.getTopLevelNode("highlights")
	if err != nil {
		return err
	}

	var (
		gate = syncutil.NewGate(bookmarksAtOnce)
		grp  syncutil.Group
	)

	for fi := range folders {
		f := folders[fi]
		var bList []*bookmark

		err := r.doAPI(&bList, bookmarkListRequestURL, "limit", requestLimit, "folder_id", f.FolderId.String())
		if err != nil {
			return err
		}

		for bi := range bList {
			select {
			case <-r.Context().Done():
				logger.Printf("importer interrupted")
				return r.Context().Err()
			default:
			}

			b := bList[bi]
			if b.BookmarkId == 0 {
				continue // ignore non-bookmark objects included in the response
			}

			gate.Start()
			grp.Go(func() error {
				defer gate.Done()
				bNode, dup, err := r.importBookmark(bsParent, b, f.Title)
				if err != nil {
					logger.Printf("error importing bookmark %d %v", b.BookmarkId, err)
					return err
				}
				if !r.incremental || !dup {
					r.mu.Lock()
					r.txtReqs = append(r.txtReqs, txtReq{bmNode: bNode, bm: b})
					r.mu.Unlock()
				}
				return r.importHighlights(hsParent, bNode, b)
			})
		}
	}

	err = grp.Err()
	if err != nil {
		return err
	}

	// Process requests for bookmark text serially because
	// Instapaper's API TOS specify that /get_text requests must be performed in series.
	// All other API requests can happen in parallel.
	for _, req := range r.txtReqs {
		if err := r.importBookmarkText(req); err != nil {
			return err
		}
	}
	return nil
}

func (r *run) importBookmark(parent *importer.Object, b *bookmark, folder string) (*importer.Object, bool, error) {
	// Find an existing permanode by camliPath:{filename} on the parent node.
	// If one doesn't exist, try searching for any permanode that has a
	// matching instapaper.com:bookmarkId attribute in case the title, which
	// is mutable, was changed.
	bmNode, err := parent.ChildPathObjectOrFunc(parseFilename(b.Title, fmt.Sprint(b.BookmarkId)),
		func() (*importer.Object, error) {
			found, err := r.findExistingBookmark(fmt.Sprint(b.BookmarkId))
			if err != nil {
				if err != os.ErrNotExist {
					return nil, fmt.Errorf("searching for node with %v %v: %v", attrBookmarkId, b.BookmarkId, err)
				}
				return r.Host.NewObject()
			}
			// If an existing permanode was found by BookmarkId, that means the
			// bookmark's title was changed. So, delete the old camliPath which
			// was based on the old title so we don't have two camliPaths on
			// the parent pointing to the same permanode.
			oldTitle := parseFilename(found.Attr(nodeattr.Title), fmt.Sprint(b.BookmarkId))
			if err := parent.DelAttr(fmt.Sprintf("camliPath:%s", oldTitle), ""); err != nil {
				return nil, err
			}
			return found, nil
		})
	if err != nil {
		return nil, false, err
	}

	instapaperUrl := fmt.Sprintf("https://www.instapaper.com/read/%v", b.BookmarkId)
	attrs := []string{
		attrBookmarkId, fmt.Sprint(b.BookmarkId),
		nodeattr.Type, nodeTypeBookmark,
		nodeattr.DateCreated, schema.RFC3339FromTime(time.Unix(int64(b.Time), 0)),
		nodeattr.Title, b.Title,
		nodeattr.Description, b.Description,
		nodeattr.URL, b.Url,
		attrUrl, instapaperUrl,
		attrProgress, fmt.Sprint(b.Progress),
		attrProgressTimestamp, schema.RFC3339FromTime(time.Unix(int64(b.ProgressTimestamp), 0)),
		nodeattr.Starred, b.Starred,
		nodeattr.Folder, folder,
	}

	changes, err := bmNode.SetAttrs2(attrs...)
	if err == nil && changes {
		logger.Printf("imported bookmark %s", b.Url)
	}
	return bmNode, !changes, nil
}

func (r *run) importBookmarkText(req txtReq) error {
	filename := parseFilename(req.bm.Title, fmt.Sprint(req.bm.BookmarkId))
	form := url.Values{}
	form.Add("bookmark_id", fmt.Sprint(req.bm.BookmarkId))
	resp, err := importer.OAuthContext{
		Ctx:    r.Context(),
		Client: r.oauthClient,
		Creds:  r.accessCreds}.POST(bookmarkTextRequestURL, form)
	if err != nil {
		if resp != nil && resp.StatusCode == http.StatusBadRequest {
			// Ignore 400 Bad Request HTTP response codes for bookmark text given some bookmarks won't have full text available but we do not
			// know which ones until we make the /get_text request and the call fails with a 400 status.
			logger.Printf("no text available for %v: %v", req.bm.Url, err)
			return nil
		}
		return err
	}
	defer resp.Body.Close()
	fileRef, err := schema.WriteFileFromReader(r.Context(), r.Host.Target(), filename, resp.Body)
	if err != nil {
		return fmt.Errorf("error storing bookmark content: %v", err)
	}
	err = req.bmNode.SetAttr("camliContent", fileRef.String())
	if err == nil {
		logger.Printf("imported text for %s", req.bm.Url)
	}
	return err
}

func (r *run) importHighlights(parent *importer.Object, bNode *importer.Object, b *bookmark) error {
	var hList []*highlight
	err := r.doAPI(&hList, fmt.Sprintf(highlightListRequestURL, b.BookmarkId))
	if err != nil {
		return err
	}

	// Given Instapaper's API returns highlights sorted by Time in ASC, we need to sort by Time DESC to make the newest
	// highlights show up first so we can quit importing early on incremental runs.
	sort.Slice(hList, func(i, j int) bool {
		return hList[i].Time > hList[j].Time
	})

	for hi := range hList {
		h := hList[hi]
		dup, err := r.importHighlight(parent, bNode, h)
		if err != nil {
			logger.Printf("error importing highlight %d %v", h.HighlightId, err)
		}
		if dup && r.incremental {
			logger.Printf("incremental highlights import found end batch")
			break
		}
	}
	return nil
}

func (r *run) importHighlight(parent *importer.Object, bNode *importer.Object, h *highlight) (bool, error) {
	hNode, err := parent.ChildPathObject(fmt.Sprint(h.HighlightId))
	if err != nil {
		return false, err
	}

	attrs := []string{
		nodeattr.Type, nodeTypeHighlight,
		nodeattr.DateCreated, schema.RFC3339FromTime(time.Unix(int64(h.Time), 0)),
		nodeattr.Title, bNode.Attr(nodeattr.Title),
		nodeattr.Content, h.Text,
		nodeattr.Description, h.Note,
		attrBookmarkId, fmt.Sprint(h.BookmarkId),
	}

	changes, err := hNode.SetAttrs2(attrs...)
	return !changes, err
}

func (r *run) getTopLevelNode(path string) (*importer.Object, error) {
	acctNode := r.AccountNode()
	root := r.RootNode()
	username := acctNode.Attr(importer.AcctAttrUserName)
	rootTitle := fmt.Sprintf("Instapaper Data for %s", username)
	if err := root.SetAttrs(nodeattr.Title, rootTitle, "camliImportRoot", "instapaper-"+username); err != nil {
		return nil, err
	}

	obj, err := root.ChildPathObject(path)
	if err != nil {
		return nil, err
	}

	var title string
	switch path {
	case "bookmarks":
		title = fmt.Sprintf("Bookmarks for %s", acctNode.Attr(importer.AcctAttrUserName))
	case "highlights":
		title = fmt.Sprintf("Highlights for %s", acctNode.Attr(importer.AcctAttrUserName))
	}
	return obj, obj.SetAttr(nodeattr.Title, title)
}

func (r *run) doAPI(result any, apiUrl string, keyval ...string) error {
	return importer.OAuthContext{
		Ctx:    r.Context(),
		Client: r.oauthClient,
		Creds:  r.accessCreds}.PopulateJSONFromURL(result, http.MethodPost, apiUrl, keyval...)
}
