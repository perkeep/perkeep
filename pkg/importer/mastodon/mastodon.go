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

// Package mastodon provides an importer for servers using the Mastodon API.
package mastodon // import "perkeep.org/pkg/importer/mastodon"

import (
	"errors"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"
	"sync"

	"golang.org/x/oauth2"

	"perkeep.org/internal/httputil"
	"perkeep.org/pkg/importer"
	"perkeep.org/pkg/progress"
	"perkeep.org/pkg/schema/nodeattr"
)

const (
	// clientName is the name we report to the server when registering an app
	clientName = "Perkeep"

	// runCompleteVersion is a cache-busting version number of the
	// importer code. It should be incremented whenever the
	// behavior of this importer is updated enough to warrant a
	// complete run.  Otherwise, if the importer runs to
	// completion, this version number is recorded on the account
	// permanode and subsequent importers can stop early.
	runCompleteVersion = "3"

	authorizationPath = "/oauth/authorize"
	tokenPath         = "/oauth/token"

	acctAttrInstanceURL  = "instanceURL"
	acctAttrClientID     = "oauthClientID"
	acctAttrClientSecret = "oauthClientSecret"

	// attrActivityPubID is the ActivityPub ID of a status, boost or
	// account: its globally unique URI, unlike the URL of its web page.
	attrActivityPubID = "activityPubId"

	// attrLegacyURI is the attribute of the ActivityPub IDs of statuses
	// imported by the importer's version 0, deleted from them.
	attrLegacyURI = "uri"

	// content warnings in Mastodon UI, represented as 'summary' in ActivityPub
	attrSpoilerText = "spoilerText"

	// attrID is the ID of a status, medium or account on its server. An
	// own status's is set with its text, from its source, so it marks that
	// it has that.
	attrID = "mastodonId"

	// attrContentHTML is a status's HTML, as its server rendered its text,
	// unless that's just the text's paragraphs and lines (see plainHTML).
	attrContentHTML = "contentHTML"

	attrVisibility         = "mastodonVisibility" // "public", "unlisted", "private" or "direct"
	attrSensitive          = "sensitive"          // "true" if the status's media are marked sensitive
	attrLanguage           = "language"           // ISO 639 code of the status's language
	attrInReplyToID        = "mastodonInReplyToId"
	attrInReplyToAccountID = "mastodonInReplyToAccountId"
	attrLinkURL            = "linkURL"   // of the status's link's preview card
	attrLinkTitle          = "linkTitle" // of the status's link's preview card
	attrInReplyTo          = "inReplyTo" // URI of the status it replies to

	// Of others' statuses: their authors'.
	attrAuthor              = "mastodonAuthor" // user@domain
	attrAuthorName          = "mastodonAuthorName"
	attrAuthorActivityPubID = "mastodonAuthorActivityPubId"

	// attrSubject is the URI of the status of a boost, favourite or
	// bookmark, or the ActivityPub ID of the account of a follow.
	attrSubject = "subject"

	// Of profiles.
	attrAccount     = "mastodonAccount" // user@domain
	attrAvatarURL   = "mastodonAvatarUrl"
	attrHeaderURL   = "mastodonHeaderUrl"
	attrHeader      = "header"         // the file of the account's own profile's header image
	attrFieldPrefix = "mastodonField:" // plus a field's name, of the account's own profile

	nodeTypeStatus        = "mastodon:status"       // the account's own
	nodeTypeLinkedStatus  = "mastodon:linkedStatus" // others', boosted, favourited, bookmarked or replied to
	nodeTypeReblog        = "mastodon:reblog"
	nodeTypeFavourite     = "mastodon:favourite"
	nodeTypeBookmark      = "mastodon:bookmark"
	nodeTypeFollow        = "mastodon:follow"
	nodeTypeProfile       = "mastodon:profile"       // the account's own
	nodeTypeLinkedProfile = "mastodon:linkedProfile" // of an account it follows

	// nodeTypePrefix, plus an attachment's type, such as "image", is the
	// camliNodeType of the node of a status's medium.
	nodeTypePrefix = "mastodon:"

	// Children of the root node.
	nodeStatuses   = "statuses"       // the account's own, keyed by URI
	nodeLinked     = "linkedStatuses" // others', keyed by URI
	nodeReblogs    = "reblogs"        // keyed by the boost's URI
	nodeFavourites = "favourites"     // keyed by the status's URI
	nodeBookmarks  = "bookmarks"      // keyed by the status's URI
	nodeFollowing  = "following"      // keyed by the account's ActivityPub ID
	nodeAccounts   = "accounts"       // profiles of followed accounts, keyed likewise
	nodeProfile    = "profile"        // the account's own

	statusesAtOnce = 10 // number of statuses of a page to prepare at once
)

type imp struct {
	importer.OAuth2
}

func init() {
	importer.Register("mastodon", &imp{})
}

func (*imp) Properties() importer.Properties {
	return importer.Properties{
		Title:       "Mastodon",
		Description: "import posts from a Mastodon or Pleroma account",

		// While the API does use client_id and client_secret, there is an
		// API endpoint for obtaining these automatically
		NeedsAPIKey:         false,
		SupportsIncremental: true,
	}
}

func (im *imp) IsAccountReady(acctNode *importer.Object) (ok bool, err error) {
	if acctNode.Attr(importer.AcctAttrAccessToken) != "" &&
		acctNode.Attr(acctAttrInstanceURL) != "" &&
		acctNode.Attr(acctAttrClientID) != "" &&
		acctNode.Attr(acctAttrClientSecret) != "" {
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

	expandedAddr, err := getExpandedAddress(acct.Attr(importer.AcctAttrUserName), acct.Attr(acctAttrInstanceURL))
	if err != nil {
		return "Misconfigured; error = " + err.Error()
	}

	// "display name (@username@example.com)"
	return fmt.Sprintf("%s (%s)", acct.Attr(importer.AcctAttrName), expandedAddr)
}

var promptURLTmpl = template.Must(template.New("root").Parse(`
{{define "promptURL"}}
<h1>Configuring Mastodon or Pleroma account</h1>
<p>Enter the base URL of your instance.</p>
<form method="post" action="{{ .AccountURL }}">
	<input type="hidden" name="mode" value="login">
	<label>Instance URL <input type="url" name="instanceURL" size="40" placeholder="https://example.com"></label>
	<input type="submit" value="Add">
</form>
{{end}}
`))

func (im *imp) ServeSetup(w http.ResponseWriter, r *http.Request, ctx *importer.SetupContext) error {
	// Since this importer works with arbitrary servers, it needs the user to
	// input an URL before it can send the user off to the OAuth authorization
	// endpoint. To accomplish this, this method is invoked twice during setup.

	instanceURL := r.FormValue("instanceURL")
	if instanceURL == "" {
		// First step: the user hasn't provided an instance URL yet, so we ask
		// them for it and send them back to this method. The template includes
		// mode=login, so that the importer code redirects us back here.

		return promptURLTmpl.ExecuteTemplate(w, "promptURL", ctx)
	}

	// Second step: User just typed in their instance URL

	cl, err := newAPIClient(ctx.Host.HTTPClient(), instanceURL, "")
	if err != nil {
		httputil.ServeError(w, r, err)
		return err
	}
	app, err := cl.registerApp(ctx, "read", im.RedirectURL(im, ctx))
	if err != nil {
		httputil.ServeError(w, r, err)
		return err
	}

	// These aren't enough to log in. We fill in the rest with ServeCallback()
	if err := ctx.AccountNode.SetAttrs(
		importer.KV(acctAttrInstanceURL, instanceURL),
		importer.KV(acctAttrClientID, app.ClientID),
		importer.KV(acctAttrClientSecret, app.ClientSecret),
	); err != nil {
		httputil.ServeError(w, r, err)
		return err
	}

	authConfig, err := im.auth(ctx)
	if err != nil {
		httputil.ServeError(w, r, err)
		return err
	}

	state, err := im.RedirectState(im, ctx)
	if err != nil {
		httputil.ServeError(w, r, err)
		return err
	}

	http.Redirect(w, r, authConfig.AuthCodeURL(state), http.StatusFound)
	return nil
}

func (im *imp) ServeCallback(w http.ResponseWriter, r *http.Request, ctx *importer.SetupContext) {

	code := r.FormValue("code")
	if code == "" {
		http.Error(w, "request contained no code", http.StatusBadRequest)
		return
	}

	auth, err := im.auth(ctx)
	if err != nil {
		httputil.ServeError(w, r, err)
		return
	}

	authToken, err := auth.Exchange(ctx, code)
	if err != nil {
		http.Error(w, "failed to obtain oauth token", http.StatusInternalServerError)
		// Provider errors can contain a response body reflecting credentials.
		log.Printf("Mastodon token exchange failed")
		return
	}

	if err := ctx.AccountNode.SetAttr(importer.AcctAttrAccessToken, authToken.AccessToken); err != nil {
		httputil.ServeError(w, r, err)
		return
	}

	cl, err := newAPIClient(ctx.Host.HTTPClient(), ctx.AccountNode.Attr(acctAttrInstanceURL), authToken.AccessToken)
	if err != nil {
		httputil.ServeError(w, r, err)
		return
	}
	mastoAccount, err := cl.verifyCredentials(ctx)
	if err != nil {
		http.Error(w, "failed to fetch account info", http.StatusInternalServerError)
		return
	}

	userAddress, err := getExpandedAddress(mastoAccount.Acct, ctx.AccountNode.Attr(acctAttrInstanceURL))
	if err != nil {
		http.Error(w, "failed to determine user's address", http.StatusInternalServerError)
		log.Printf("failed to determine user's address: %s", err)
		return
	}

	acctTitle := fmt.Sprintf("%s's Mastodon account", userAddress)

	if err := ctx.AccountNode.SetAttrs(
		importer.KV(importer.AcctAttrUserID, mastoAccount.ID),
		importer.KV(importer.AcctAttrUserName, mastoAccount.Acct),
		importer.KV(importer.AcctAttrName, mastoAccount.DisplayName),
		importer.KV(nodeattr.Title, acctTitle),
	); err != nil {
		httputil.ServeError(w, r, err)
		return
	}

	http.Redirect(w, r, ctx.AccountURL(), http.StatusFound)
}

type run struct {
	*importer.RunContext

	incremental bool // true if importing only part
	api         *apiClient
	userID      string // the account's ID, of its server
	userAddress string // address in the form of user@example.com, used in logs

	// Children of the root node.
	statuses, linked, reblogs, favourites, bookmarks, following, accounts *importer.Object

	mediaOnce sync.Once
	mediaBar  *progress.Bar // of media attachments fetched; made with the first
}

// media returns the bar of media attachments fetched.
func (r *run) media() *progress.Bar {
	r.mediaOnce.Do(func() {
		r.mediaBar = r.Progress().Bar("media", "Fetching media attachments")
	})
	return r.mediaBar
}

var fullImportOverride, _ = strconv.ParseBool(os.Getenv("PERKEEP_MASTODON_FULL_IMPORT"))

func (im *imp) Run(ctx *importer.RunContext) error {
	acct := ctx.AccountNode()
	userAddress, err := getExpandedAddress(acct.Attr(importer.AcctAttrUserName), acct.Attr(acctAttrInstanceURL))
	if err != nil {
		return err
	}
	api, err := newAPIClient(ctx.Host.HTTPClient(), acct.Attr(acctAttrInstanceURL), acct.Attr(importer.AcctAttrAccessToken))
	if err != nil {
		return err
	}
	api.logf = ctx.Logf

	r := &run{
		RunContext:  ctx,
		incremental: !fullImportOverride && acct.Attr(importer.AcctAttrCompletedVersion) == runCompleteVersion,
		api:         api,
		userID:      acct.Attr(importer.AcctAttrUserID),
		userAddress: userAddress,
	}
	if r.userID == "" {
		return errors.New("missing user ID")
	}

	batch := r.Host.NewClaimBatch()
	batch.SetAttrs(r.RootNode(), importer.KV(nodeattr.Title, fmt.Sprintf("%s's Mastodon data", userAddress)))
	children := []struct {
		node        **importer.Object
		path, title string
	}{
		{&r.statuses, nodeStatuses, "Mastodon statuses for %s"},
		{&r.linked, nodeLinked, "Mastodon statuses boosted, favourited, bookmarked or replied to by %s"},
		{&r.reblogs, nodeReblogs, "Mastodon boosts by %s"},
		{&r.favourites, nodeFavourites, "Mastodon favourites of %s"},
		{&r.bookmarks, nodeBookmarks, "Mastodon bookmarks of %s"},
		{&r.following, nodeFollowing, "Mastodon follows of %s"},
		{&r.accounts, nodeAccounts, "Mastodon accounts followed by %s"},
	}
	paths := make([]string, len(children))
	for i, c := range children {
		paths[i] = c.path
	}
	nodes, err := r.childNodes(ctx.Context(), batch, r.RootNode(), paths)
	if err != nil {
		return err
	}
	for i, c := range children {
		batch.SetAttrs(nodes[i], importer.KV(nodeattr.Title, fmt.Sprintf(c.title, userAddress)))
		*c.node = nodes[i]
	}
	if err := batch.Commit(ctx.Context()); err != nil {
		return err
	}

	if err := r.importProfile(); err != nil {
		return fmt.Errorf("importing profile: %w", err)
	}
	for _, l := range []struct {
		name, title string
		first       string // URL of the list's first page
		refType     string // camliNodeType of the list's items' nodes, if not statuses'
		refs        *importer.Object
	}{
		{"statuses", "Importing statuses and boosts", api.statusesURL(r.userID), "", r.reblogs},
		{"favourites", "Importing favourites", api.favouritesURL(), nodeTypeFavourite, r.favourites},
		{"bookmarks", "Importing bookmarks", api.bookmarksURL(), nodeTypeBookmark, r.bookmarks},
	} {
		if err := r.importStatusList(l.name, l.title, l.first, l.refType, l.refs); err != nil {
			return fmt.Errorf("importing %s: %w", l.name, err)
		}
	}
	if err := r.importFollowing(); err != nil {
		return fmt.Errorf("importing follows: %w", err)
	}
	if err := r.Context().Err(); err != nil {
		return err
	}
	if r.mediaBar != nil {
		r.mediaBar.Finish()
	}

	if err := acct.SetAttr(importer.AcctAttrCompletedVersion, runCompleteVersion); err != nil {
		return err
	}

	return nil
}

// qualified returns acct, an account's username, and @domain if it's
// remote, with the domain of the run's server if it's local.
func (r *run) qualified(acct string) string {
	if acct == "" || strings.Contains(acct, "@") {
		return acct
	}
	return acct + "@" + r.api.base.Host
}

// auth returns the appropriate oauth2.Config for this account
func (im *imp) auth(ctx *importer.SetupContext) (*oauth2.Config, error) {
	baseURL, err := url.Parse(ctx.AccountNode.Attr(acctAttrInstanceURL))
	if err != nil {
		return nil, err
	}

	tokenURL := *baseURL
	tokenURL.Path = path.Join(tokenURL.Path, tokenPath)

	authURL := *baseURL
	authURL.Path = path.Join(authURL.Path, authorizationPath)

	return &oauth2.Config{
		ClientID:     ctx.AccountNode.Attr(acctAttrClientID),
		ClientSecret: ctx.AccountNode.Attr(acctAttrClientSecret),
		RedirectURL:  im.RedirectURL(im, ctx),
		Endpoint: oauth2.Endpoint{
			AuthURL:  authURL.String(),
			TokenURL: tokenURL.String(),
		},
	}, nil
}

// getExpandedAddress returns the address for the account in the @user@example.com form
func getExpandedAddress(user, instanceURL string) (string, error) {

	if user == "" || instanceURL == "" {
		return "", errors.New("some required account data is missing")
	}

	parsedURL, err := url.Parse(instanceURL)
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("@%s@%s", user, parsedURL.Host), nil
}
