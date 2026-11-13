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
	"context"
	"errors"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"os"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"

	"golang.org/x/oauth2"
	"golang.org/x/sync/errgroup"

	"perkeep.org/internal/httputil"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/importer"
	"perkeep.org/pkg/progress"
	"perkeep.org/pkg/schema"
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
	runCompleteVersion = "1"

	authorizationPath = "/oauth/authorize"
	tokenPath         = "/oauth/token"

	acctAttrInstanceURL  = "instanceURL"
	acctAttrClientID     = "oauthClientID"
	acctAttrClientSecret = "oauthClientSecret"

	// status URI. This is an ActivityPub globally unique identifier. May or may
	// not be the same as the URL for the human-readable version of the status.
	attrURI = "uri"

	// content warnings in Mastodon UI, represented as 'summary' in ActivityPub
	attrSpoilerText = "spoilerText"

	// attrID is the ID of a status or medium on its server. A status's is
	// set with its text, from its source, so it marks that it has that.
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

	nodeTypeStatus = "mastodon:status"

	// nodeTypePrefix, plus an attachment's type, such as "image", is the
	// camliNodeType of the node of a status's medium.
	nodeTypePrefix = "mastodon:"

	// Name of the child node which contains references to all the statuses
	nodeStatuses = "statuses"

	statusesAtOnce = 10 // number of statuses of a page to prepare at once
)

// statusOptionalAttrs are the attributes of a status's node that it may
// lack, deleted from the node if it no longer has them, as when the status
// was edited. Its "camliPath:" attributes, of its media, are too.
var statusOptionalAttrs = []string{
	nodeattr.Title,
	nodeattr.Content,
	nodeattr.DateModified,
	nodeattr.CamliContentImage,
	attrContentHTML,
	attrSpoilerText,
	attrSensitive,
	attrLanguage,
	attrInReplyToID,
	attrInReplyToAccountID,
	attrLinkURL,
	attrLinkTitle,
}

// mediumOptionalAttrs are the attributes of a medium's node that it may
// lack, as statusOptionalAttrs are of a status's.
var mediumOptionalAttrs = []string{
	nodeattr.Description,
	nodeattr.CamliContentImage,
}

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

	userAddress string // address in the form of user@example.com, used in logs

	// statusBar counts the statuses imported, or skipped, as unchanged.
	// The account's statuses_count isn't its total, as it counts
	// reblogs, which aren't imported, and an incremental run stops at
	// the first page it has.
	statusBar *progress.Bar

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
		userAddress: userAddress,
	}

	rootTitle := fmt.Sprintf("%s's Mastodon data", userAddress)
	if err := r.RootNode().SetAttr(nodeattr.Title, rootTitle); err != nil {
		return err
	}

	userID := acct.Attr(importer.AcctAttrUserID)
	if userID == "" {
		return errors.New("missing user ID")
	}

	r.statusBar = r.Progress().Bar("statuses", "Importing statuses")
	if err := r.importStatuses(userID); err != nil {
		return err
	}
	if err := r.Context().Err(); err != nil {
		return err
	}
	r.statusBar.Finish()
	if r.mediaBar != nil {
		r.mediaBar.Finish()
	}

	if err := acct.SetAttr(importer.AcctAttrCompletedVersion, runCompleteVersion); err != nil {
		return err
	}

	return nil
}

// importStatuses imports the statuses of the account of ID userID, a
// page at a time, newest first, stopping at a page that's unchanged if
// the run's incremental.
func (r *run) importStatuses(userID string) error {
	statusesNode, err := r.RootNode().ChildPathObject(nodeStatuses)
	if err != nil {
		return err
	}

	nodeTitle := fmt.Sprintf("Mastodon statuses for %s", r.userAddress)
	if err := statusesNode.SetAttr(nodeattr.Title, nodeTitle); err != nil {
		return err
	}

	r.Logf("Beginning statuses import for %s", r.userAddress)

	for page := r.api.statusesURL(userID); page != ""; {
		if err := r.Context().Err(); err != nil {
			return err
		}
		sts, next, err := r.api.statuses(r.Context(), page)
		if err != nil {
			return err
		}
		if len(sts) == 0 {
			break
		}
		changed, err := r.importPage(statusesNode, sts)
		if err != nil {
			return err
		}
		if r.incremental && !changed {
			r.Logf("reached the end for incremental import for %s", r.userAddress)
			return nil
		}
		page = next
	}
	r.Logf("reached the end of statuses for %s", r.userAddress)
	return nil
}

// A statusImport is a status to import, with what's fetched of it.
type statusImport struct {
	st    *status
	node  *importer.Object
	text  string // of its source
	media []*mediumImport
}

// A mediumImport is a status's medium to import.
type mediumImport struct {
	att  attachment
	path string           // of the medium's node, a child of its status's
	node *importer.Object // nil until made, if the status had none of it

	file    blob.Ref // of the medium
	preview blob.Ref // of the medium's preview image, if it's not one
}

// importPage imports sts, statuses of a page, as children of parent, and
// reports whether it changed any. Their changes are set with as few
// claims as hold them, after the permanodes of all they lack.
func (r *run) importPage(parent *importer.Object, sts []*status) (changed bool, err error) {
	ctx := r.Context()
	sts = slices.DeleteFunc(sts, func(st *status) bool { return st.Reblog != nil || st.URI == "" })
	uris := make([]string, len(sts))
	for i, st := range sts {
		uris[i] = st.URI
	}
	nodes, err := parent.ChildPathObjects(ctx, uris)
	if err != nil {
		return false, err
	}

	imps := make([]*statusImport, len(sts))
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(statusesAtOnce)
	for i, st := range sts {
		g.Go(func() error {
			var err error
			imps[i], err = r.prepareStatus(gctx, nodes[i], st)
			if err != nil {
				return fmt.Errorf("importing status %s: %w", st.URI, err)
			}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return false, err
	}

	var made []*mediumImport
	for _, si := range imps {
		if si == nil {
			continue
		}
		for _, m := range si.media {
			if m.node == nil {
				made = append(made, m)
			}
		}
	}
	if len(made) > 0 {
		objs, err := r.Host.NewObjects(ctx, len(made))
		if err != nil {
			return false, err
		}
		for i, m := range made {
			m.node = objs[i]
		}
	}

	batch := r.Host.NewClaimBatch()
	for _, si := range imps {
		if si != nil {
			si.addTo(batch)
		}
	}
	if err := batch.Commit(ctx); err != nil {
		return false, err
	}

	for _, si := range imps {
		if si == nil {
			continue
		}
		ch := batch.Changed(si.node)
		for _, m := range si.media {
			ch = ch || batch.Changed(m.node)
		}
		if ch {
			changed = true
			r.statusBar.Add(1)
			r.Logf("Imported status %s to %s", si.st.URI, si.node.PermanodeRef())
		} else {
			r.statusBar.Skip(1)
		}
	}
	return changed, nil
}

// prepareStatus fetches what st, of node n, needs that n lacks: its
// source's text, unless n has it of st as last edited, and its media.
// It returns nil, without error, if the status isn't to be imported.
func (r *run) prepareStatus(ctx context.Context, n *importer.Object, st *status) (*statusImport, error) {
	si := &statusImport{st: st, node: n}
	// The node's new, or of an import before statuses' sources and their
	// media's nodes were.
	legacy := n.Attr(attrID) == ""
	if !legacy && n.Attr(attrID) == st.ID && n.Attr(nodeattr.DateModified) == editedAt(st) {
		si.text = n.Attr(nodeattr.Content)
	} else {
		src, err := r.api.statusSource(ctx, st.ID)
		if isNotFound(err) {
			r.Logf("skipping status %s, of no source: %v", st.URI, err)
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		si.text = src.Text
	}
	for i, att := range st.MediaAttachments {
		m, err := r.prepareMedium(ctx, n, legacy, i, att)
		if err != nil {
			return nil, err
		}
		if m != nil {
			si.media = append(si.media, m)
		}
	}
	return si, nil
}

// prepareMedium returns the import of att, the medium of index i of the
// status of node n, which is legacy if it was imported before its media
// had nodes, fetching what n's child of it lacks. It returns nil, without
// error, if the medium's gone.
func (r *run) prepareMedium(ctx context.Context, n *importer.Object, legacy bool, i int, att attachment) (*mediumImport, error) {
	if att.URL == "" {
		return nil, nil
	}
	kind := att.Type
	if kind == "" {
		kind = "unknown"
	}
	m := &mediumImport{att: att, path: fmt.Sprintf("%s%d", kind, i+1)}
	if v := n.Attr("camliPath:" + m.path); v != "" {
		br, ok := blob.Parse(v)
		if !ok {
			return nil, fmt.Errorf("invalid blobref %q at camliPath:%s", v, m.path)
		}
		var err error
		if m.node, err = r.Host.ObjectFromRef(br); err != nil {
			return nil, err
		}
		if m.node.Attr(attrID) == att.ID {
			m.file, _ = blob.Parse(m.node.Attr(nodeattr.CamliContent))
			m.preview, _ = blob.Parse(m.node.Attr(nodeattr.CamliContentImage))
		}
	}
	if !m.file.Valid() && legacy {
		// The status's node has the file, named for its URL.
		m.file, _ = blob.Parse(n.Attr("camliPath:" + path.Base(att.URL)))
	}
	if !m.file.Valid() {
		var err error
		m.file, err = r.fetchFile(ctx, att.URL)
		if isNotFound(err) {
			r.Logf("skipping medium %s of status %s: %v", att.ID, n.PermanodeRef(), err)
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		r.media().Add(1)
	}
	if att.Type != "image" && att.PreviewURL != "" && !m.preview.Valid() {
		var err error
		m.preview, err = r.fetchFile(ctx, att.PreviewURL)
		if isNotFound(err) {
			r.Logf("skipping the preview of medium %s of status %s: %v", att.ID, n.PermanodeRef(), err)
		} else if err != nil {
			return nil, err
		}
	}
	return m, nil
}

// fetchFile fetches the file at rawURL and stores it, named for its URL.
func (r *run) fetchFile(ctx context.Context, rawURL string) (blob.Ref, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", rawURL, nil)
	if err != nil {
		return blob.Ref{}, err
	}
	res, err := r.api.hc.Do(req)
	if err != nil {
		return blob.Ref{}, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return blob.Ref{}, &apiError{URL: req.URL.Redacted(), StatusCode: res.StatusCode}
	}
	return schema.WriteFileFromReader(ctx, r.Host.Target(), path.Base(req.URL.Path), res.Body)
}

// addTo adds setting the attributes of the status, and of its media, to
// batch.
func (si *statusImport) addTo(batch *importer.ClaimBatch) {
	st := si.st
	attrs := []importer.KeyValue{
		importer.KV(nodeattr.Type, nodeTypeStatus),
		importer.KV(attrID, st.ID),
		importer.KV(attrURI, st.URI),
		importer.KV(nodeattr.URL, st.URL),
		importer.KV(nodeattr.StartDate, schema.RFC3339FromTime(st.CreatedAt)),
		importer.KV(nodeattr.DateModified, editedAt(st)),
		importer.KV(nodeattr.Title, si.text),
		importer.KV(nodeattr.Content, si.text),
		importer.KV(attrSpoilerText, st.SpoilerText),
		importer.KV(attrVisibility, st.Visibility),
		importer.KV(attrLanguage, st.Language),
		importer.KV(attrInReplyToID, st.InReplyToID),
		importer.KV(attrInReplyToAccountID, st.InReplyToAccountID),
	}
	if st.Content != plainHTML(si.text) {
		attrs = append(attrs, importer.KV(attrContentHTML, st.Content))
	}
	if st.Sensitive {
		attrs = append(attrs, importer.KV(attrSensitive, "true"))
	}
	if c := st.Card; c != nil {
		attrs = append(attrs,
			importer.KV(attrLinkURL, c.URL),
			importer.KV(attrLinkTitle, c.Title),
		)
	}
	var image blob.Ref
	for _, m := range si.media {
		attrs = append(attrs, importer.KV("camliPath:"+m.path, m.node.PermanodeRef().String()))
		if !image.Valid() {
			if m.att.Type == "image" {
				image = m.file
			} else {
				image = m.preview
			}
		}
		mattrs := []importer.KeyValue{
			importer.KV(nodeattr.Type, nodeTypePrefix+m.att.Type),
			importer.KV(attrID, m.att.ID),
			importer.KV(nodeattr.CamliContent, m.file.String()),
			importer.KV(nodeattr.Description, m.att.Description),
		}
		if m.preview.Valid() {
			mattrs = append(mattrs, importer.KV(nodeattr.CamliContentImage, m.preview.String()))
		}
		setAttrs(batch, m.node, mattrs, mediumOptionalAttrs)
	}
	if image.Valid() {
		attrs = append(attrs, importer.KV(nodeattr.CamliContentImage, image.String()))
	}
	setAttrs(batch, si.node, attrs, statusOptionalAttrs)
}

// setAttrs adds setting n's attributes of attrs that aren't empty to
// batch, and deleting those of n's of optional, and its "camliPath:"
// attributes, that attrs lacks.
func setAttrs(batch *importer.ClaimBatch, n *importer.Object, attrs []importer.KeyValue, optional []string) {
	set := make(map[string]bool)
	attrs = slices.DeleteFunc(attrs, func(kv importer.KeyValue) bool { return kv.Value == "" })
	for _, kv := range attrs {
		set[kv.Key] = true
	}
	var dels []string
	for _, key := range optional {
		if !set[key] {
			dels = append(dels, key)
		}
	}
	n.ForeachAttr(func(key, _ string) {
		if strings.HasPrefix(key, "camliPath:") && !set[key] && !slices.Contains(dels, key) {
			dels = append(dels, key)
		}
	})
	batch.DelAttrs(n, dels...)
	batch.SetAttrs(n, attrs...)
}

// editedAt returns when st was last edited, as stored, or the empty
// string if it wasn't.
func editedAt(st *status) string {
	if st.EditedAt == nil {
		return ""
	}
	return schema.RFC3339FromTime(*st.EditedAt)
}

var (
	paragraphBreaks = regexp.MustCompile(`\n\n+`)

	// htmlEscaper escapes text as Mastodon (and Ruby's ERB) does.
	htmlEscaper = strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&quot;",
		"'", "&#39;",
	)
)

// plainHTML returns the HTML that a Mastodon server renders of a status's
// text when it has no links, mentions, hashtags or markup: its paragraphs,
// between blank lines, in p elements, and its other line breaks as br
// elements. A status's HTML that's that of its text isn't stored, as
// plainHTML gives it.
func plainHTML(text string) string {
	if strings.TrimSpace(text) == "" {
		return ""
	}
	var b strings.Builder
	for _, p := range paragraphBreaks.Split(text, -1) {
		b.WriteString("<p>")
		b.WriteString(strings.ReplaceAll(htmlEscaper.Replace(p), "\n", "<br />"))
		b.WriteString("</p>")
	}
	return b.String()
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
