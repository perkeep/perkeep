/*
Copyright 2026 The Perkeep Authors

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

package importer

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"golang.org/x/time/rate"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/filesource"
	"perkeep.org/pkg/filesource/dropbox"
	"perkeep.org/pkg/filesource/gdrive"
	"perkeep.org/pkg/filesource/local"
	"perkeep.org/pkg/schema/nodeattr"
	"perkeep.org/pkg/search"
)

// File sources are places that importers read files from, such as a
// directory of the server's disk or a Google Drive, which the owner sets
// up on the pages at /importer/sources/. Each is a permanode with
// camliNodeType fileSource. Its credentials are in the server's
// credential store, as an importer account's are, never in its claims.
const (
	// fileSourcesName is the path of the file sources' pages, beneath the
	// importer handler, where an importer's would otherwise be.
	fileSourcesName = "sources"

	nodeTypeFileSource = "fileSource"

	attrFileSourceType      = "fileSourceType"      // fileSourceLocal, fileSourceGoogleDrive or fileSourceDropbox
	attrFileSourceRoot      = "fileSourceRoot"      // a local source's absolute directory path
	attrFileSourceOAuthKind = "fileSourceOAuthKind" // the kind of its OAuth client, such as oauthKindWeb

	// Credentials, which are in secretAttributes.
	attrFileSourceOAuthClient  = "fileSourceOAuthClient"  // a Google OAuth client's JSON, as downloaded, or a Dropbox app's key
	attrFileSourceRefreshToken = "fileSourceRefreshToken" // an OAuth refresh token
	attrFileSourceOAuthPending = "fileSourceOAuthPending" // a pendingAuthorization's JSON

	fileSourceLocal       = "local"
	fileSourceGoogleDrive = "gdrive"
	fileSourceDropbox     = "dropbox"

	// oauthKindWeb is an OAuth client of the kind Google calls a "Web
	// application", which redirects the browser back to the server.
	oauthKindWeb = "web"
	// oauthKindDesktop is an OAuth client of the kind Google calls a
	// "Desktop app", which can only redirect the browser to the local
	// machine, from which the owner pastes the URL back to the server.
	oauthKindDesktop = "desktop"
	// oauthKindCode is an OAuth client, such as a Dropbox app, whose
	// authorization, without a redirect, shows a code, which the owner
	// pastes back to the server.
	oauthKindCode = "code"
)

// desktopRedirectURI is where a desktop OAuth client's authorization
// sends the browser. Nothing serves it, so the page fails to load, and
// the owner pastes its URL, which has the authorization code, from the
// browser's address bar into the source's page.
const desktopRedirectURI = "http://127.0.0.1/"

// pendingAuthorizationLifetime is how long the owner has to authorize a
// file source, once started.
const pendingAuthorizationLifetime = 10 * time.Minute

// driveRate is the most requests a second that a Google Drive file
// source makes. Drive limits requests per minute, rejecting those
// beyond its quota without saying how long to wait.
const driveRate = 10

// dropboxRate is the most requests a second that a Dropbox file source
// makes. Dropbox says how long to wait when it's over its limits, which
// it doesn't publish.
const dropboxRate = 10

// ErrNeedsAuthorization is returned by the Sources of [Host.FileSource]
// whose authorization is missing, or has been revoked or has expired,
// until the owner authorizes them again on their page.
var ErrNeedsAuthorization = errors.New("importer: file source needs authorizing")

// pendingAuthorization is the state of a file source's authorization
// that's been started: the OAuth state the authorization must come back
// with, and the verifier of its PKCE challenge.
type pendingAuthorization struct {
	State    string `json:"state"`
	Verifier string `json:"verifier"`
	Expires  int64  `json:"expires"` // Unix seconds
}

// A fileSource is a file source's permanode, for its pages.
type fileSource struct {
	h *Host
	o *Object
}

func (s fileSource) Ref() blob.Ref { return s.o.PermanodeRef() }
func (s fileSource) URL() string {
	return s.h.FileSourcePath(s.Ref())
}
func (s fileSource) Type() string { return s.o.Attr(attrFileSourceType) }
func (s fileSource) Root() string { return s.o.Attr(attrFileSourceRoot) }

// OAuthKind returns the kind of the source's OAuth client, if it has one:
// oauthKindWeb or oauthKindDesktop, for a Google Drive, or oauthKindCode.
func (s fileSource) OAuthKind() string { return s.o.Attr(attrFileSourceOAuthKind) }

// TitleAttr returns the source's title, which may be empty.
func (s fileSource) TitleAttr() string { return s.o.Attr(nodeattr.Title) }

// Title returns the source's title, or else a description of it.
func (s fileSource) Title() string {
	if t := s.TitleAttr(); t != "" {
		return t
	}
	if s.Type() == fileSourceLocal {
		return s.Root()
	}
	return s.TypeTitle()
}

// TypeTitle describes the source's type.
func (s fileSource) TypeTitle() string {
	switch s.Type() {
	case fileSourceLocal:
		return "Directory on this server"
	case fileSourceGoogleDrive:
		return "Google Drive"
	case fileSourceDropbox:
		return "Dropbox"
	}
	return s.Type()
}

// UsesOAuth reports whether the source needs authorizing, with OAuth.
func (s fileSource) UsesOAuth() bool {
	return s.Type() == fileSourceGoogleDrive || s.Type() == fileSourceDropbox
}

// Authorized reports whether the source has the authorization that it
// needs, if any.
func (s fileSource) Authorized() bool {
	return !s.UsesOAuth() || s.o.Attr(attrFileSourceRefreshToken) != ""
}

// fileSourceCallbackURL returns the URL that a Google web OAuth client
// must allow redirects to.
func (h *Host) fileSourceCallbackURL() string {
	return h.ImporterBaseURL() + fileSourcesName + "/callback"
}

// fileSources returns the file sources, by title.
func (h *Host) fileSources(ctx context.Context) ([]fileSource, error) {
	if h.search == nil {
		return nil, nil
	}
	res, err := h.search.Query(ctx, &search.SearchQuery{
		Sort:       search.Unsorted,
		Limit:      -1,
		Expression: fmt.Sprintf("attr:%s:%s", attrNodeType, nodeTypeFileSource),
	})
	if err != nil {
		return nil, err
	}
	var srcs []fileSource
	for _, b := range res.Blobs {
		s, err := h.fileSource(b.Blob)
		if err != nil {
			return nil, err
		}
		srcs = append(srcs, s)
	}
	slices.SortFunc(srcs, func(a, b fileSource) int {
		if c := strings.Compare(a.Title(), b.Title()); c != 0 {
			return c
		}
		return strings.Compare(a.Ref().String(), b.Ref().String())
	})
	return srcs, nil
}

// errNotFileSource is the error of a ref that's not of a file source.
var errNotFileSource = errors.New("importer: not a file source")

// FileSourceInfo describes a file source, for importers' pages.
type FileSourceInfo struct {
	Ref        blob.Ref
	Title      string
	Type       string // "local", "gdrive" or "dropbox"
	TypeTitle  string // such as "Google Drive"
	Authorized bool   // whether it has the authorization it needs, if any
}

// FileSources returns the file sources, by title.
func (h *Host) FileSources(ctx context.Context) ([]FileSourceInfo, error) {
	srcs, err := h.fileSources(ctx)
	if err != nil {
		return nil, err
	}
	infos := make([]FileSourceInfo, len(srcs))
	for i, s := range srcs {
		infos[i] = s.info()
	}
	return infos, nil
}

// FileSourceInfo returns the description of the file source ref.
func (h *Host) FileSourceInfo(ref blob.Ref) (FileSourceInfo, error) {
	s, err := h.fileSource(ref)
	if err != nil {
		return FileSourceInfo{}, err
	}
	return s.info(), nil
}

func (s fileSource) info() FileSourceInfo {
	return FileSourceInfo{Ref: s.Ref(), Title: s.Title(), Type: s.Type(), TypeTitle: s.TypeTitle(), Authorized: s.Authorized()}
}

// FileSourcesPath returns the URL path of the file sources' pages.
func (h *Host) FileSourcesPath() string { return h.ImporterBasePath() + fileSourcesName + "/" }

// FileSourcePath returns the URL path of the page of the file source ref.
func (h *Host) FileSourcePath(ref blob.Ref) string { return h.FileSourcesPath() + ref.String() }

// fileSource returns the file source of the permanode ref.
func (h *Host) fileSource(ref blob.Ref) (fileSource, error) {
	o, err := h.ObjectFromRef(ref)
	if err != nil {
		return fileSource{}, err
	}
	if o.Attr(attrNodeType) != nodeTypeFileSource {
		return fileSource{}, errNotFileSource
	}
	return fileSource{h, o}, nil
}

// FileSource returns the file source of the permanode ref, set up on the
// file sources' pages, ready to read. Reads of a Google Drive or Dropbox
// whose authorization is missing, revoked or expired fail with an error
// matching [ErrNeedsAuthorization].
func (h *Host) FileSource(ctx context.Context, ref blob.Ref) (filesource.Source, error) {
	s, err := h.fileSource(ref)
	if err != nil {
		return nil, err
	}
	switch s.Type() {
	case fileSourceLocal:
		return local.New(s.Root())
	case fileSourceGoogleDrive, fileSourceDropbox:
		conf, err := s.oauthConfig()
		if err != nil {
			return nil, err
		}
		rt, err := s.o.SecretAttr(ctx, attrFileSourceRefreshToken)
		if err != nil {
			return nil, err
		}
		if rt == "" {
			return nil, ErrNeedsAuthorization
		}
		// The Source outlives ctx; its tokens are refreshed until the
		// Host is closed.
		tctx := h.oauthContext(h.ctx)
		ts := reauthTokenSource{conf.TokenSource(tctx, &oauth2.Token{RefreshToken: rt})}
		client := oauth2.NewClient(tctx, ts)
		if s.Type() == fileSourceDropbox {
			return &dropbox.Source{Client: client, Limiter: rate.NewLimiter(dropboxRate, 1)}, nil
		}
		return &gdrive.Source{Client: client, Limiter: rate.NewLimiter(driveRate, 1)}, nil
	}
	return nil, fmt.Errorf("importer: unknown file source type %q", s.Type())
}

// oauthContext returns ctx, with the Host's HTTP client for the oauth2
// package to use.
func (h *Host) oauthContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, oauth2.HTTPClient, h.HTTPClient())
}

// reauthTokenSource is an oauth2.TokenSource whose errors for a refresh
// token that's been revoked or has expired match ErrNeedsAuthorization.
type reauthTokenSource struct{ ts oauth2.TokenSource }

func (s reauthTokenSource) Token() (*oauth2.Token, error) {
	t, err := s.ts.Token()
	if re, ok := errors.AsType[*oauth2.RetrieveError](err); ok && re.ErrorCode == "invalid_grant" {
		return nil, fmt.Errorf("%w: %v", ErrNeedsAuthorization, err)
	}
	return t, err
}

// dropboxAppKeyRx matches Dropbox apps' keys.
var dropboxAppKeyRx = regexp.MustCompile(`^[a-z0-9]{8,32}$`)

// parseOAuthClient parses the OAuth client of a source of type typ: for a
// Google Drive, the client's JSON, as downloaded from the Google Cloud
// console; for a Dropbox, its app's key. It returns the client's kind,
// such as oauthKindWeb.
func parseOAuthClient(typ, client string) (conf *oauth2.Config, kind string, err error) {
	if typ == fileSourceDropbox {
		if !dropboxAppKeyRx.MatchString(client) {
			return nil, "", errors.New("not a Dropbox app's key")
		}
		// No scopes: a scoped app is granted those of its permissions, and
		// an app from before scopes, which Dropbox gives all of its access,
		// rejects any. (finishAuthorization checks those granted, if any.)
		return &oauth2.Config{ClientID: client, Endpoint: dropbox.Endpoint}, oauthKindCode, nil
	}
	conf, err = google.ConfigFromJSON([]byte(client), gdrive.Scope)
	if err != nil {
		return nil, "", fmt.Errorf("not a Google OAuth client's JSON: %w", err)
	}
	var keys struct{ Web json.RawMessage }
	if err := json.Unmarshal([]byte(client), &keys); err != nil {
		return nil, "", err
	}
	if keys.Web != nil {
		return conf, oauthKindWeb, nil
	}
	return conf, oauthKindDesktop, nil
}

// oauthConfig returns the configuration of the source's OAuth client,
// redirecting to where its kind of client must, if anywhere.
func (s fileSource) oauthConfig() (*oauth2.Config, error) {
	client, err := s.o.SecretAttr(context.TODO(), attrFileSourceOAuthClient)
	if err != nil {
		return nil, err
	}
	if client == "" {
		return nil, errors.New("importer: file source has no OAuth client")
	}
	conf, kind, err := parseOAuthClient(s.Type(), client)
	if err != nil {
		return nil, err
	}
	switch kind {
	case oauthKindWeb:
		conf.RedirectURL = s.h.fileSourceCallbackURL()
	case oauthKindDesktop:
		conf.RedirectURL = desktopRedirectURI
	}
	return conf, nil
}

// requiredScopes returns the OAuth scopes that the source needs.
func (s fileSource) requiredScopes() []string {
	if s.Type() == fileSourceDropbox {
		return dropbox.Scopes
	}
	return []string{gdrive.Scope}
}

// startAuthorization starts authorizing s, returning the URL of the page
// at Google or Dropbox for the owner to allow it on.
func (s fileSource) startAuthorization() (string, error) {
	conf, err := s.oauthConfig()
	if err != nil {
		return "", err
	}
	var nonce [32]byte
	rand.Read(nonce[:])
	// The state names the source, for the callback to find it.
	p := pendingAuthorization{
		State:    "src:" + s.Ref().String() + ":" + hex.EncodeToString(nonce[:]),
		Verifier: oauth2.GenerateVerifier(),
		Expires:  time.Now().Add(pendingAuthorizationLifetime).Unix(),
	}
	pj, err := json.Marshal(p)
	if err != nil {
		return "", err
	}
	s.h.fileSourceMu.Lock()
	defer s.h.fileSourceMu.Unlock()
	if err := s.o.SetAttr(attrFileSourceOAuthPending, string(pj)); err != nil {
		return "", err
	}
	if s.Type() == fileSourceDropbox {
		return conf.AuthCodeURL(p.State, oauth2.SetAuthURLParam("token_access_type", "offline"), oauth2.S256ChallengeOption(p.Verifier)), nil
	}
	// ApprovalForce, so that there's a refresh token even if the client
	// was allowed already.
	return conf.AuthCodeURL(p.State, oauth2.AccessTypeOffline, oauth2.ApprovalForce, oauth2.S256ChallengeOption(p.Verifier)), nil
}

// fileSourceOfState returns the file source named by the state of its
// authorization's redirect.
func (h *Host) fileSourceOfState(state string) (fileSource, error) {
	rest, ok := strings.CutPrefix(state, "src:")
	refStr, nonce, ok2 := strings.Cut(rest, ":")
	ref, ok3 := blob.Parse(refStr)
	if !ok || !ok2 || !ok3 || len(nonce) != 64 {
		return fileSource{}, errors.New("invalid OAuth state")
	}
	return h.fileSource(ref)
}

// finishAuthorization finishes authorizing s, with q, the query
// parameters of the redirect from Google, or for a client of
// oauthKindCode, the code pasted, getting a refresh token to keep. The
// authorization that was started is used up, even if this fails, so that
// a redirect can't be replayed. A pasted code needs no state, as only the
// verifier of the authorization started can use it.
func (s fileSource) finishAuthorization(ctx context.Context, q url.Values) error {
	s.h.fileSourceMu.Lock()
	defer s.h.fileSourceMu.Unlock()
	pj, err := s.o.SecretAttr(ctx, attrFileSourceOAuthPending)
	if err != nil {
		return err
	}
	var p pendingAuthorization
	if pj == "" || json.Unmarshal([]byte(pj), &p) != nil || p.State == "" {
		return errors.New("no authorization was started; start it again")
	}
	if s.OAuthKind() != oauthKindCode && subtle.ConstantTimeCompare([]byte(p.State), []byte(q.Get("state"))) != 1 {
		return errors.New("that's not the latest authorization started; start it again")
	}
	if err := s.o.DelAttr(attrFileSourceOAuthPending, ""); err != nil {
		return err
	}
	switch {
	case time.Now().Unix() >= p.Expires:
		return errors.New("the authorization took too long; start it again")
	case q.Get("error") != "":
		return fmt.Errorf("authorization failed: %s", q.Get("error"))
	case q.Get("code") == "":
		return errors.New("the authorization has no code")
	}
	conf, err := s.oauthConfig()
	if err != nil {
		return err
	}
	tok, err := conf.Exchange(s.h.oauthContext(ctx), q.Get("code"), oauth2.VerifierOption(p.Verifier))
	if err != nil {
		return fmt.Errorf("getting a token: %w", err)
	}
	if tok.RefreshToken == "" {
		return fmt.Errorf("no refresh token from %s", s.TypeTitle())
	}
	if scope, _ := tok.Extra("scope").(string); scope != "" {
		for _, want := range s.requiredScopes() {
			if !slices.Contains(strings.Fields(scope), want) {
				return fmt.Errorf("reading %s wasn't allowed: no %s permission", s.TypeTitle(), want)
			}
		}
	}
	return s.o.SetAttr(attrFileSourceRefreshToken, tok.RefreshToken)
}

// serveFileSources serves the file sources' pages, which only the owner
// may see, as they show the server's paths and lead to its credentials:
//
//	/importer/sources/: the sources, and a form to add one
//	/importer/sources/callback: where Google returns from authorizing one
//	/importer/sources/sha224-...: a source, to change, test, authorize or delete
func (h *Host) serveFileSources(w http.ResponseWriter, r *http.Request, rest string) {
	if !h.allowCredentialSetup(w, r) {
		return
	}
	switch rest {
	case "":
		if r.Method == http.MethodPost {
			h.serveNewFileSource(w, r)
			return
		}
		srcs, err := h.fileSources(r.Context())
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		h.execTemplate(w, r, fileSourcesPage{
			Title: "File sources",
			Body:  fileSourcesBody{Host: h, Sources: srcs},
		})
	case "callback":
		h.serveFileSourceCallback(w, r)
	default:
		ref, ok := blob.Parse(rest)
		if !ok {
			http.NotFound(w, r)
			return
		}
		s, err := h.fileSource(ref)
		if errors.Is(err, errNotFileSource) {
			http.NotFound(w, r)
			return
		}
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		h.serveFileSource(w, r, s)
	}
}

// localRoot returns the directory of a local file source, from a form.
func localRoot(dir string) (string, error) {
	if !filepath.IsAbs(dir) {
		return "", errors.New("the directory must be an absolute path")
	}
	dir = filepath.Clean(dir)
	if _, err := local.New(dir); err != nil {
		return "", err
	}
	return dir, nil
}

// serveNewFileSource adds a file source, from the form on the sources'
// page.
func (h *Host) serveNewFileSource(w http.ResponseWriter, r *http.Request) {
	attrs := []KeyValue{
		KV(attrNodeType, nodeTypeFileSource),
		KV(nodeattr.Title, strings.TrimSpace(r.FormValue("title"))),
	}
	switch typ := r.FormValue("type"); typ {
	case fileSourceLocal:
		dir, err := localRoot(strings.TrimSpace(r.FormValue("dir")))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		attrs = append(attrs, KV(attrFileSourceType, typ), KV(attrFileSourceRoot, dir))
	case fileSourceGoogleDrive, fileSourceDropbox:
		client := strings.TrimSpace(r.FormValue("client"))
		_, kind, err := parseOAuthClient(typ, client)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		attrs = append(attrs,
			KV(attrFileSourceType, typ),
			KV(attrFileSourceOAuthKind, kind),
			KV(attrFileSourceOAuthClient, client),
		)
	default:
		http.Error(w, "unknown file source type", http.StatusBadRequest)
		return
	}
	o, err := h.newPendingObject()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := o.SetAttrs(attrs...); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, fileSource{h, o}.URL(), http.StatusSeeOther)
}

// serveFileSourceCallback serves the redirect from Google of a web OAuth
// client's authorization of a Google Drive.
func (h *Host) serveFileSourceCallback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "invalid callback method", http.StatusMethodNotAllowed)
		return
	}
	s, err := h.fileSourceOfState(r.FormValue("state"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.finishAuthorization(r.Context(), r.URL.Query()); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, s.URL(), http.StatusSeeOther)
}

// serveFileSource serves a file source's page, and its forms.
func (h *Host) serveFileSource(w http.ResponseWriter, r *http.Request, s fileSource) {
	body := fileSourceBody{Host: h, Source: s}
	if s.Type() == fileSourceGoogleDrive && s.OAuthKind() == oauthKindWeb {
		body.CallbackURL = h.fileSourceCallbackURL()
	}
	if r.Method == http.MethodPost {
		switch r.FormValue("mode") {
		case "save":
			attrs := []KeyValue{KV(nodeattr.Title, strings.TrimSpace(r.FormValue("title")))}
			if s.Type() == fileSourceLocal {
				dir, err := localRoot(strings.TrimSpace(r.FormValue("dir")))
				if err != nil {
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}
				attrs = append(attrs, KV(attrFileSourceRoot, dir))
			}
			var deletes []string
			if client := strings.TrimSpace(r.FormValue("client")); client != "" && s.UsesOAuth() {
				_, kind, err := parseOAuthClient(s.Type(), client)
				if err != nil {
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}
				// The authorization was the old client's.
				deletes = []string{attrFileSourceRefreshToken, attrFileSourceOAuthPending}
				attrs = append(attrs,
					KV(attrFileSourceOAuthKind, kind),
					KV(attrFileSourceOAuthClient, client),
				)
			}
			if err := s.o.SetAttrsWithDeletes(deletes, attrs...); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			http.Redirect(w, r, s.URL(), http.StatusSeeOther)
			return
		case "delete":
			// What was imported from it stays; only its setup goes, with
			// its credentials.
			deletes := []string{attrFileSourceOAuthClient, attrFileSourceRefreshToken, attrFileSourceOAuthPending}
			if err := s.o.SetAttrsWithDeletes(deletes, KV(attrNodeType, nodeTypeFileSource+"-deleted")); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			http.Redirect(w, r, h.ImporterBasePath()+fileSourcesName+"/", http.StatusSeeOther)
			return
		case "authorize":
			if !s.UsesOAuth() {
				http.Error(w, "this file source needs no authorization", http.StatusBadRequest)
				return
			}
			authURL, err := s.startAuthorization()
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			if s.OAuthKind() == oauthKindWeb {
				http.Redirect(w, r, authURL, http.StatusSeeOther)
				return
			}
			// A desktop client's or a Dropbox app's authorization can't
			// come back here, so the page has a form to paste where it
			// went, or the code it showed.
			body.AuthURL = authURL
		case "code":
			code := strings.TrimSpace(r.FormValue("code"))
			if s.OAuthKind() != oauthKindCode || code == "" {
				http.Error(w, "paste the code shown once authorized", http.StatusBadRequest)
				return
			}
			if err := s.finishAuthorization(r.Context(), url.Values{"code": {code}}); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			http.Redirect(w, r, s.URL(), http.StatusSeeOther)
			return
		case "paste":
			u, err := url.Parse(strings.TrimSpace(r.FormValue("url")))
			if err != nil || !u.Query().Has("state") {
				http.Error(w, "that's not the URL of the page that failed to load; paste the whole URL from the browser's address bar", http.StatusBadRequest)
				return
			}
			if err := s.finishAuthorization(r.Context(), u.Query()); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			http.Redirect(w, r, s.URL(), http.StatusSeeOther)
			return
		case "test":
			body.Tested = true
			body.Listing, body.TestError = h.testFileSource(r.Context(), s)
		default:
			http.Error(w, "unknown mode", http.StatusBadRequest)
			return
		}
	}
	h.execTemplate(w, r, fileSourcePage{Title: s.Title(), Body: body})
}

// testFileSource lists the top of s, to see that it can be read, returning
// some of what's there, or an error to show.
func (h *Host) testFileSource(ctx context.Context, s fileSource) (files []listedFile, errText string) {
	src, err := h.FileSource(ctx, s.Ref())
	if err != nil {
		return nil, err.Error()
	}
	root, err := src.Root(ctx)
	if err != nil {
		return nil, err.Error()
	}
	all, err := src.List(ctx, root)
	if err != nil {
		return nil, err.Error()
	}
	const max = 50
	files = listedFiles(all)
	return files[:min(len(files), max)], ""
}

// A listedFile is a file or directory as a page lists it.
type listedFile struct {
	filesource.File

	// Ambiguous is whether others listed with it have its name, as
	// Google Drive allows, so that the page shows its ID too.
	Ambiguous bool
}

// listedFiles returns files for listing on a page, by name.
func listedFiles(files []filesource.File) []listedFile {
	named := make(map[string]int)
	for _, f := range files {
		named[f.Name]++
	}
	listed := make([]listedFile, len(files))
	for i, f := range files {
		listed[i] = listedFile{f, named[f.Name] > 1}
	}
	slices.SortStableFunc(listed, func(a, b listedFile) int { return strings.Compare(a.Name, b.Name) })
	return listed
}

type fileSourcesPage struct {
	Title string
	Body  fileSourcesBody
}

type fileSourcesBody struct {
	Host    *Host
	Sources []fileSource
}

// CallbackURL returns the URL that a Google web OAuth client must allow
// redirects to.
func (b fileSourcesBody) CallbackURL() string { return b.Host.fileSourceCallbackURL() }

type fileSourcePage struct {
	Title string
	Body  fileSourceBody
}

type fileSourceBody struct {
	Host        *Host
	Source      fileSource
	CallbackURL string // for a web OAuth client, where it must allow redirects to

	AuthURL string // of a desktop OAuth client's authorization, started, to open

	Tested    bool         // whether the source was tested
	Listing   []listedFile // of the top of the source, tested
	TestError string       // of the test
}

const fileSourcesTmpl = `
{{define "fileSourcesPage"}}
  {{template "pageTop" .}}
  {{template "fileSourcesBody" .Body}}
  {{template "pageBottom"}}
{{end}}

{{define "fileSourcesBody"}}
{{$base := .Host.ImporterBasePath}}
<nav class="crumbs"><a href="{{$base}}">Importers</a> › File sources</nav>
<h1>File sources</h1>
<p class="lede">Places that importers read files from, such as Google Takeout exports' zip files: a directory on this server, a Google Drive, or a Dropbox.</p>

<section class="card">
  <h2>Sources</h2>
  {{if .Sources}}
  <table class="list">
    <thead><tr><th>Source</th><th>Type</th><th>Status</th></tr></thead>
    <tbody>
    {{range .Sources}}
      <tr>
        <td><a href="{{.URL}}">{{.Title}}</a></td>
        <td>{{.TypeTitle}}</td>
        <td{{if not .Authorized}} class="failed"{{end}}>{{if .Authorized}}Ready{{else}}Needs authorizing{{end}}</td>
      </tr>
    {{end}}
    </tbody>
  </table>
  {{else}}
  <p class="muted">No file sources yet.</p>
  {{end}}
</section>

<section class="card">
  <h2>Add a directory on this server</h2>
  <form method="post">
    <input type="hidden" name="type" value="local">
    <dl class="props">
      <dt><label for="local-dir">Directory</label></dt><dd><input id="local-dir" name="dir" type="text" size="50" placeholder="/home/me/Downloads" required></dd>
      <dt><label for="local-title">Title</label></dt><dd><input id="local-title" name="title" type="text" size="30" placeholder="(the directory)"></dd>
    </dl>
    <p class="muted">Importers can read any file beneath it that the server can.</p>
    <div class="actions"><button class="primary" type="submit">Add</button></div>
  </form>
</section>

<section class="card">
  <h2>Add a Google Drive</h2>
  <p>Reading Google Drive needs an OAuth client of your own, from the <a href="https://console.cloud.google.com/apis/credentials">Google Cloud console</a>:</p>
  <ol>
    <li>In a project with the Google Drive API enabled, set up the OAuth consent screen, with you as a test user, and the scope <code>.../auth/drive.readonly</code>.</li>
    <li>Create an OAuth client ID, either of type <b>Web application</b>, allowing the redirect URI <code>{{.CallbackURL}}</code>, which must be HTTPS and reach this page; or of type <b>Desktop app</b>, for which you'll paste a URL back here instead.</li>
    <li>Download its JSON, and paste it here.</li>
  </ol>
  <p class="muted">While the consent screen's app is in testing, Google expires its authorization after 7 days; authorize the source again then.</p>
  <form method="post">
    <input type="hidden" name="type" value="gdrive">
    <dl class="props">
      <dt><label for="gdrive-client">Client JSON</label></dt><dd><textarea id="gdrive-client" name="client" rows="4" cols="60" autocomplete="off" required></textarea></dd>
      <dt><label for="gdrive-title">Title</label></dt><dd><input id="gdrive-title" name="title" type="text" size="30" placeholder="Google Drive"></dd>
    </dl>
    <p class="muted">It's stored in this server's credential store, and never shown again.</p>
    <div class="actions"><button class="primary" type="submit">Add</button></div>
  </form>
</section>

<section class="card">
  <h2>Add a Dropbox</h2>
  <p>Reading Dropbox needs an app of your own, from the <a href="https://www.dropbox.com/developers/apps">Dropbox App Console</a>:</p>
  <ol>
    <li>Create an app with <b>Scoped access</b> and <b>Full Dropbox</b> access, as Google Takeout saves exports in its own app's folder, Apps/Google Download Your Data.</li>
    <li>On its Permissions tab, check <code>files.metadata.read</code> and <code>files.content.read</code>, and submit.</li>
    <li>On its Settings tab, allow public clients (Implicit Grant &amp; PKCE), and paste its App key here. Its secret isn't needed, nor a redirect URI: you'll paste a code from Dropbox back here instead.</li>
  </ol>
  <form method="post">
    <input type="hidden" name="type" value="dropbox">
    <dl class="props">
      <dt><label for="dropbox-client">App key</label></dt><dd><input id="dropbox-client" name="client" type="text" size="30" autocomplete="off" required></dd>
      <dt><label for="dropbox-title">Title</label></dt><dd><input id="dropbox-title" name="title" type="text" size="30" placeholder="Dropbox"></dd>
    </dl>
    <p class="muted">It's stored in this server's credential store.</p>
    <div class="actions"><button class="primary" type="submit">Add</button></div>
  </form>
</section>
{{end}}

{{define "fileSourcePage"}}
  {{template "pageTop" .}}
  {{template "fileSourceBody" .Body}}
  {{template "pageBottom"}}
{{end}}

{{define "fileSourceBody"}}
{{$base := .Host.ImporterBasePath}}
{{$src := .Source}}
<nav class="crumbs"><a href="{{$base}}">Importers</a> › <a href="{{$base}}sources/">File sources</a></nav>
<h1>{{$src.Title}}</h1>

<section class="card">
  <dl class="props">
    <dt>Type</dt><dd>{{$src.TypeTitle}}</dd>
    {{if eq $src.Type "local"}}<dt>Directory</dt><dd class="ref">{{$src.Root}}</dd>{{end}}
    {{if $src.UsesOAuth}}
    <dt>OAuth client</dt><dd>{{if eq $src.OAuthKind "web"}}Web application, redirecting to <code>{{.CallbackURL}}</code>{{else if eq $src.OAuthKind "desktop"}}Desktop app{{else}}App{{end}}</dd>
    <dt>Status</dt><dd{{if not $src.Authorized}} class="failed"{{end}}>{{if $src.Authorized}}Authorized{{else}}Needs authorizing{{end}}</dd>
    {{end}}
    <dt>Permanode</dt><dd class="ref">{{bloblink $src.Ref}}</dd>
  </dl>
  <div class="actions">
    {{if $src.UsesOAuth}}
    <form method="post" class="inline">
      <input type="hidden" name="mode" value="authorize">
      <button{{if not $src.Authorized}} class="primary"{{end}} type="submit">{{if $src.Authorized}}Authorize again{{else}}Authorize{{end}}</button>
    </form>
    {{end}}
    <form method="post" class="inline">
      <input type="hidden" name="mode" value="test">
      <button type="submit">Test</button>
    </form>
    <form method="post" class="inline">
      <input type="hidden" name="mode" value="delete">
      <button class="danger" type="submit" onclick="return confirm('Delete this file source? What was imported from it stays in Perkeep.')">Delete</button>
    </form>
  </div>
</section>

{{with .AuthURL}}
<section class="card">
  <h2>Authorize</h2>
  {{if eq $src.OAuthKind "code"}}
  <ol>
    <li><a href="{{.}}" target="_blank" rel="noopener">Open {{$src.TypeTitle}}'s page</a>, and allow reading your files.</li>
    <li>It then shows a code. Paste it here:</li>
  </ol>
  <form method="post">
    <input type="hidden" name="mode" value="code">
    <input name="code" type="text" size="50" autocomplete="off" required>
    <div class="actions"><button class="primary" type="submit">Finish</button></div>
  </form>
  {{else}}
  <ol>
    <li><a href="{{.}}" target="_blank" rel="noopener">Open Google's page</a>, and allow reading Google Drive.</li>
    <li>Google then sends the browser to a page at <code>http://127.0.0.1/</code>, which fails to load. Paste its whole URL, from the browser's address bar, here:</li>
  </ol>
  <form method="post">
    <input type="hidden" name="mode" value="paste">
    <input name="url" type="text" size="70" autocomplete="off" required>
    <div class="actions"><button class="primary" type="submit">Finish</button></div>
  </form>
  {{end}}
</section>
{{end}}

{{if .Tested}}
<section class="card">
  <h2>Test</h2>
  {{if .TestError}}
  <p class="failed">{{.TestError}}</p>
  {{else if .Listing}}
  <p>It has, at the top:</p>
  <ul>{{range .Listing}}<li>{{.Name}}{{if .Dir}}/{{end}}{{if .Ambiguous}} <span class="muted ref">{{.ID}}</span>{{end}}</li>{{end}}</ul>
  {{else}}
  <p>It works, but it's empty.</p>
  {{end}}
</section>
{{end}}

<section class="card">
  <h2>Change</h2>
  <form method="post">
    <input type="hidden" name="mode" value="save">
    <dl class="props">
      <dt><label for="title">Title</label></dt><dd><input id="title" name="title" type="text" size="30" value="{{$src.TitleAttr}}" placeholder="{{$src.Title}}"></dd>
      {{if eq $src.Type "local"}}
      <dt><label for="dir">Directory</label></dt><dd><input id="dir" name="dir" type="text" size="50" value="{{$src.Root}}" required></dd>
      {{end}}
      {{if eq $src.Type "gdrive"}}
      <dt><label for="client">New client JSON</label></dt><dd><textarea id="client" name="client" rows="3" cols="60" autocomplete="off"></textarea></dd>
      {{else if eq $src.Type "dropbox"}}
      <dt><label for="client">New app key</label></dt><dd><input id="client" name="client" type="text" size="30" autocomplete="off"></dd>
      {{end}}
    </dl>
    {{if $src.UsesOAuth}}<p class="muted">Leave the client blank to keep it. A new client needs authorizing.</p>{{end}}
    <div class="actions"><button type="submit">Save</button></div>
  </form>
</section>
{{end}}
`
