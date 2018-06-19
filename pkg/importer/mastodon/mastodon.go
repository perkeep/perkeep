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
	"strconv"
	"strings"
	"sync"

	"perkeep.org/internal/httputil"
	"perkeep.org/pkg/importer"
	"perkeep.org/pkg/schema"
	"perkeep.org/pkg/schema/nodeattr"

	"github.com/mattn/go-mastodon"
	"go4.org/ctxutil"
	"go4.org/syncutil"
	"golang.org/x/oauth2"
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
	runCompleteVersion = "0"

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

	// Name of the child node which contains references to all the statuses
	nodeStatuses = "statuses"

	importAtOnce = 10 // number of statuses to import at once

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

	app, err := mastodon.RegisterApp(ctx, &mastodon.AppConfig{
		Server:       instanceURL,
		ClientName:   clientName,
		Scopes:       "read",
		RedirectURIs: im.RedirectURL(im, ctx),
	})
	if err != nil {
		httputil.ServeError(w, r, err)
		return err
	}

	// These aren't enough to log in. We fill in the rest with ServeCallback()
	if err := ctx.AccountNode.SetAttrs(
		acctAttrInstanceURL, instanceURL,
		acctAttrClientID, app.ClientID,
		acctAttrClientSecret, app.ClientSecret,
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
		log.Printf("Mastodon token exchange failed with error: %s", err)
		return
	}

	if err := ctx.AccountNode.SetAttr(importer.AcctAttrAccessToken, authToken.AccessToken); err != nil {
		httputil.ServeError(w, r, err)
		return
	}

	cl := createMastodonClient(ctx.Context, ctx.AccountNode)
	mastoAccount, err := cl.GetAccountCurrentUser(ctx)
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
		importer.AcctAttrUserID, string(mastoAccount.ID),
		importer.AcctAttrUserName, mastoAccount.Acct,
		importer.AcctAttrName, mastoAccount.DisplayName,
		nodeattr.Title, acctTitle,
	); err != nil {
		httputil.ServeError(w, r, err)
		return
	}

	http.Redirect(w, r, ctx.AccountURL(), http.StatusFound)
}

type run struct {
	*importer.RunContext
	im *imp

	incremental bool // true if importing only part
	cl          *mastodon.Client

	userAddress string // address in the form of user@example.com, used in logs
}

var fullImportOverride, _ = strconv.ParseBool(os.Getenv("PERKEEP_MASTODON_FULL_IMPORT"))

func (im *imp) Run(ctx *importer.RunContext) error {
	acct := ctx.AccountNode()
	userAddress, err := getExpandedAddress(acct.Attr(importer.AcctAttrUserName), acct.Attr(acctAttrInstanceURL))
	if err != nil {
		return err
	}

	r := &run{
		RunContext:  ctx,
		incremental: !fullImportOverride && acct.Attr(importer.AcctAttrCompletedVersion) == runCompleteVersion,
		cl:          createMastodonClient(ctx.Context(), acct),
		userAddress: userAddress,
	}

	rootTitle := fmt.Sprintf("%s's Mastodon data", userAddress)
	if err := r.RootNode().SetAttr(nodeattr.Title, rootTitle); err != nil {
		return err
	}

	userID := mastodon.ID(acct.Attr(importer.AcctAttrUserID))
	if userID == "" {
		return errors.New("missing user ID")
	}

	if err := r.importStatuses(userID); err != nil {
		return err
	}

	if err := acct.SetAttr(importer.AcctAttrCompletedVersion, runCompleteVersion); err != nil {
		return err
	}

	return nil
}

// importStatuses imports statuses for the given user into the store
func (r *run) importStatuses(userID mastodon.ID) error {
	statusesNode, err := r.RootNode().ChildPathObject(nodeStatuses)
	if err != nil {
		return err
	}

	nodeTitle := fmt.Sprintf("Mastodon statuses for %s", r.userAddress)
	if err := statusesNode.SetAttr(nodeattr.Title, nodeTitle); err != nil {
		return err
	}

	log.Printf("mastodon: Beginning statuses import for %s", r.userAddress)

	var pg mastodon.Pagination

	for {
		select {
		case <-r.Context().Done():
			return r.Context().Err()
		default:
		}

		if pg.MaxID != "" {
			log.Printf("mastodon: fetching batch for %s, from %s", r.userAddress, pg.MaxID)
		} else {
			log.Printf("mastodon: fetching batch for %s", r.userAddress)
		}

		batch, err := r.cl.GetAccountStatuses(r.Context(), userID, &pg)
		if err != nil {
			return err
		}

		if len(batch) == 0 {
			log.Printf("mastodon: got empty batch, assuming end of statuses for %s", r.userAddress)
			return nil
		}

		gate := syncutil.NewGate(importAtOnce)
		var grp syncutil.Group
		allReblogs := true
		anyNew := false
		var anyNewMu sync.Mutex

		for i := range batch {
			st := batch[i]

			// If an entry is a reblog, we ignore it and move on. However, the
			// whole batch being all reblogs does not mean there is nothing new
			// on the next page. If everything on this page was a reblog, we
			// move on to the next page regardless.
			if st.Reblog != nil {
				continue
			}

			allReblogs = false

			gate.Start()
			grp.Go(func() error {
				defer gate.Done()
				alreadyHad, err := r.importStatus(statusesNode, st)
				if err != nil {
					return fmt.Errorf("error importing status %s: %v", st.URI, err)
				}

				if !alreadyHad {
					anyNewMu.Lock()
					anyNew = true
					anyNewMu.Unlock()
				}

				return nil

			})
		}

		if err := grp.Err(); err != nil {
			return err
		}

		if !anyNew && !allReblogs {
			log.Printf("mastodon: reached the end for incremental import for %s", r.userAddress)
			return nil
		}

		if pg.MaxID == "" {
			log.Printf("mastodon: reached the end of statuses for %s", r.userAddress)
			return nil
		}

	}
}

// importStatus imports a single status, also adding it to the statuses node.
// Returns true if we already had the status in the database.
func (r *run) importStatus(listNode *importer.Object, st *mastodon.Status) (bool, error) {
	select {
	case <-r.Context().Done():
		return false, r.Context().Err()
	default:
	}

	// We store child nodes by their URI, since the URI is supposed to be an
	// unchanging, globally unique identifier for the status
	statusNode, err := listNode.ChildPathObject(st.URI)
	if err != nil {
		return false, err
	}

	if r.incremental && statusNode.Attr(attrURI) == st.URI {
		return true, nil
	}

	attrs := []string{
		nodeattr.Type, "mastodon:status",
		attrURI, st.URI,
		nodeattr.URL, st.URL,
		nodeattr.Content, st.Content,
		nodeattr.StartDate, schema.RFC3339FromTime(st.CreatedAt),
	}

	if st.SpoilerText != "" {
		attrs = append(attrs, attrSpoilerText, st.SpoilerText)
	}

	filenames := make(map[string]int)

	for i, att := range st.MediaAttachments {
		// All media for a local user will be local
		resp, err := ctxutil.Client(r.Context()).Get(att.URL)
		if err != nil {
			return false, err
		}

		if resp.StatusCode != http.StatusOK {
			return false, fmt.Errorf("failed fetching attachment %s with HTTP status %s", att.URL, resp.Status)
		}

		fileRef, err := schema.WriteFileFromReader(r.Context(), r.Host.Target(), "", resp.Body)
		resp.Body.Close()
		if err != nil {
			return false, err
		}

		filename := path.Base(att.URL)
		filenames[filename]++

		// A status can have several different attachments with the same
		// filename. We add numbers to the path to diffirentiate them if that's
		// the case
		if filenames[filename] > 1 {
			ext := path.Ext(filename)
			filename = fmt.Sprintf("%s%d%s", strings.TrimSuffix(filename, ext), filenames[filename], ext)
		}

		attrs = append(attrs, fmt.Sprintf("camliPath:%v", filename), fileRef.String())

		// The first image gets to be the preview image for the node
		if i == 0 {
			attrs = append(attrs, "camliContentImage", fileRef.String())
		}

		log.Printf("mastodon: adding attachment %s to permanode %s for status %s", fileRef.String(), statusNode.PermanodeRef(), st.URI)

	}

	changed, err := statusNode.SetAttrs2(attrs...)
	if err == nil && changed {
		log.Printf("mastodon: Imported status %s to %s", st.URI, statusNode.PermanodeRef())
	}

	return !changed, err

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

// createMastodonClient returns a new Client configured for the provided
// account. It does not check if the account has the needed fields filled.
func createMastodonClient(ctx context.Context, acct *importer.Object) *mastodon.Client {

	// Although the client can take client_id and client_secret, we won't need
	// those for token auth
	cl := mastodon.NewClient(&mastodon.Config{
		Server:      acct.Attr(acctAttrInstanceURL),
		AccessToken: acct.Attr(importer.AcctAttrAccessToken),
	})

	cl.Client = *ctxutil.Client(ctx)
	return cl
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
