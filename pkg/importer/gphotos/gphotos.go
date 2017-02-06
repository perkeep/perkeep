/*
Copyright 2017 The Camlistore Authors

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

// Package gphotos implements an importer for gphotos.com accounts.
package gphotos // import "camlistore.org/pkg/importer/gphotos"

// TODO: removing camliPath from gallery permanode when pic deleted from gallery

import (
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/httputil"
	"camlistore.org/pkg/importer"
	"camlistore.org/pkg/schema"
	"camlistore.org/pkg/schema/nodeattr"
	"camlistore.org/pkg/search"

	"go4.org/ctxutil"
	"go4.org/syncutil"
	"golang.org/x/net/context"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

const (
	// runCompleteVersion is a cache-busting version number of the
	// importer code. It should be incremented whenever the
	// behavior of this importer is updated enough to warrant a
	// complete run.  Otherwise, if the importer runs to
	// completion, this version number is recorded on the account
	// permanode and subsequent importers can stop early.
	runCompleteVersion = "4"

	// attrgphotosId is used for both gphotos photo IDs and gallery IDs.
	attrgphotosId = "gphotosId"

	// acctAttrOAuthToken stores access + " " + refresh + " " + expiry
	// See encodeToken and decodeToken.
	acctAttrOAuthToken = "oauthToken"

	// acctSinceToken store the GPhotos-returned nextToken
	acctSinceToken = "sinceToken"
)

var (
	_ importer.Importer            = imp{}
	_ importer.ImporterSetupHTMLer = imp{}
)

func init() {
	importer.Register("gphotos", imp{})
}

// imp is the implementation of the gphotos importer.
type imp struct {
	importer.OAuth2
}

func (imp) SupportsIncremental() bool { return true }

type userInfo struct {
	ID    string // numeric gphotos user ID ("11583474931002155675")
	Name  string // "Jane Smith"
	Email string // jane.smith@example.com
}

func (imp) getUserInfo(ctx context.Context) (*userInfo, error) {
	u, err := getUser(ctx, ctxutil.Client(ctx))
	if err != nil {
		return nil, err
	}
	return &userInfo{ID: u.PermissionId, Email: u.EmailAddress, Name: u.DisplayName}, nil
}

func (imp) IsAccountReady(acctNode *importer.Object) (ok bool, err error) {
	if acctNode.Attr(importer.AcctAttrUserID) != "" && acctNode.Attr(acctAttrOAuthToken) != "" {
		return true, nil
	}
	return false, nil
}

func (im imp) SummarizeAccount(acct *importer.Object) string {
	ok, err := im.IsAccountReady(acct)
	if err != nil || !ok {
		return ""
	}
	if acct.Attr(importer.AcctAttrUserName) == "" || acct.Attr(importer.AcctAttrName) == "" {
		return fmt.Sprintf("userid %s", acct.Attr(importer.AcctAttrUserID))
	}
	return fmt.Sprintf("%s <%s>, userid %s",
		acct.Attr(importer.AcctAttrName),
		acct.Attr(importer.AcctAttrUserName),
		acct.Attr(importer.AcctAttrUserID),
	)
}

func (im imp) ServeSetup(w http.ResponseWriter, r *http.Request, ctx *importer.SetupContext) error {
	oauthConfig, err := im.auth(ctx)
	if err == nil {
		// we will get back this with the token, so use it for preserving account info
		state := "acct:" + ctx.AccountNode.PermanodeRef().String()
		// AccessType needs to be "offline", as the user is not here all the time;
		// ApprovalPrompt needs to be "force" to be able to get a RefreshToken
		// everytime, even for Re-logins, too.
		//
		// Source: https://developers.google.com/youtube/v3/guides/authentication#server-side-apps
		http.Redirect(w, r, oauthConfig.AuthCodeURL(state, oauth2.AccessTypeOffline, oauth2.ApprovalForce), 302)
	}
	return err
}

// CallbackURLParameters returns the needed callback parameters - empty for Google gphotos.
func (im imp) CallbackURLParameters(acctRef blob.Ref) url.Values {
	return url.Values{}
}

func (im imp) ServeCallback(w http.ResponseWriter, r *http.Request, ctx *importer.SetupContext) {
	oauthConfig, err := im.auth(ctx)
	if err != nil {
		httputil.ServeError(w, r, fmt.Errorf("Error getting oauth config: %v", err))
		return
	}

	if r.Method != "GET" {
		http.Error(w, "Expected a GET", 400)
		return
	}
	code := r.FormValue("code")
	if code == "" {
		http.Error(w, "Expected a code", 400)
		return
	}

	token, err := oauthConfig.Exchange(ctx, code)
	if err != nil {
		log.Printf("importer/gphotos: token exchange error: %v", err)
		httputil.ServeError(w, r, fmt.Errorf("token exchange error: %v", err))
		return
	}

	log.Printf("importer/gphotos: got exhanged token.")
	gphotosCtx := context.WithValue(ctx, ctxutil.HTTPClient, oauthConfig.Client(ctx, token))

	userInfo, err := im.getUserInfo(gphotosCtx)
	if err != nil {
		log.Printf("Couldn't get username: %v", err)
		httputil.ServeError(w, r, fmt.Errorf("can't get username: %v", err))
		return
	}

	if err := ctx.AccountNode.SetAttrs(
		importer.AcctAttrUserID, userInfo.ID,
		importer.AcctAttrName, userInfo.Name,
		importer.AcctAttrUserName, userInfo.Email,
		acctAttrOAuthToken, encodeToken(token),
	); err != nil {
		httputil.ServeError(w, r, fmt.Errorf("Error setting attribute: %v", err))
		return
	}
	http.Redirect(w, r, ctx.AccountURL(), http.StatusFound)
}

// encodeToken encodes the oauth2.Token as
// AccessToken + " " + RefreshToken + " " + Expiry.Unix()
func encodeToken(token *oauth2.Token) string {
	if token == nil {
		return ""
	}
	var seconds int64
	if !token.Expiry.IsZero() {
		seconds = token.Expiry.Unix()
	}
	return token.AccessToken + " " + token.RefreshToken + " " + strconv.FormatInt(seconds, 10)
}

// decodeToken parses an access token, refresh token, and optional
// expiry unix timestamp separated by spaces into an oauth2.Token.
// It returns as much as it can.
func decodeToken(encoded string) *oauth2.Token {
	t := new(oauth2.Token)
	f := strings.Fields(encoded)
	if len(f) > 0 {
		t.AccessToken = f[0]
	}
	if len(f) > 1 {
		t.RefreshToken = f[1]
	}
	if len(f) > 2 && f[2] != "0" {
		sec, err := strconv.ParseInt(f[2], 10, 64)
		if err == nil {
			t.Expiry = time.Unix(sec, 0)
		}
	}
	return t
}

func (im imp) auth(ctx *importer.SetupContext) (*oauth2.Config, error) {
	clientID, secret, err := ctx.Credentials()
	if err != nil {
		return nil, err
	}
	conf := &oauth2.Config{
		Endpoint:     google.Endpoint,
		RedirectURL:  ctx.CallbackURL(),
		ClientID:     clientID,
		ClientSecret: secret,
		Scopes:       scopeURLs,
	}
	return conf, nil
}

func (imp) AccountSetupHTML(host *importer.Host) string {
	// gphotos doesn't allow a path in the origin. Remove it.
	origin := host.ImporterBaseURL()
	if u, err := url.Parse(origin); err == nil {
		u.Path = ""
		origin = u.String()
	}

	callback := host.ImporterBaseURL() + "gphotos/callback"
	return fmt.Sprintf(`
<h1>Configuring gphotos</h1>
<p>Visit <a href='https://console.developers.google.com/'>https://console.developers.google.com/</a>
and click <b>"Create Project"</b>.</p>
<p>Then under "APIs & Auth" in the left sidebar, click on "Credentials", then click the button <b>"Create credentials"</b>, and pick <b>"OAuth client ID"</b>.</p>
<p>Use the following settings:</p>
<ul>
  <li>Web application</li>
  <li>Authorized JavaScript origins: <b>%s</b></li>
  <li>Authorized Redirect URI: <b>%s</b></li>
</ul>
<p>Click "Create Client ID".  Copy the "Client ID" and "Client Secret" into the boxes above.</p>
`, origin, callback)
}

// A run is our state for a given run of the importer.
type run struct {
	*importer.RunContext
	incremental  bool // whether we've completed a run in the past
	photoGate    *syncutil.Gate
	setNextToken func(string) error
	*downloader
}

var forceFullImport, _ = strconv.ParseBool(os.Getenv("CAMLI_gphotos_FULL_IMPORT"))

func (imp) Run(rctx *importer.RunContext) error {
	clientID, secret, err := rctx.Credentials()
	if err != nil {
		return err
	}
	acctNode := rctx.AccountNode()

	ocfg := &oauth2.Config{
		Endpoint:     google.Endpoint,
		ClientID:     clientID,
		ClientSecret: secret,
		Scopes:       scopeURLs,
	}

	token := decodeToken(acctNode.Attr(acctAttrOAuthToken))
	sinceToken := acctNode.Attr(acctSinceToken)
	baseCtx := rctx.Context()
	ctx := context.WithValue(baseCtx, ctxutil.HTTPClient, ocfg.Client(baseCtx, token))

	root := rctx.RootNode()
	if root.Attr(nodeattr.Title) == "" {
		if err := root.SetAttr(
			nodeattr.Title,
			fmt.Sprintf("%s - Google Photos", acctNode.Attr(importer.AcctAttrName)),
		); err != nil {
			return err
		}
	}

	dl, err := newDownloader(ctxutil.Client(ctx))
	if err != nil {
		return err
	}
	r := &run{
		RunContext:   rctx,
		incremental:  !forceFullImport && acctNode.Attr(importer.AcctAttrCompletedVersion) == runCompleteVersion,
		photoGate:    syncutil.NewGate(3),
		setNextToken: func(nextToken string) error { return acctNode.SetAttr(acctSinceToken, nextToken) },
		downloader:   dl,
	}
	if err := r.importPhotos(ctx, sinceToken); err != nil {
		return err
	}

	if err := acctNode.SetAttrs(importer.AcctAttrCompletedVersion, runCompleteVersion); err != nil {
		return err
	}

	return nil
}

func (r *run) importPhotos(ctx context.Context, sinceToken string) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	photos, nextToken, err := r.downloader.photos(ctx, sinceToken)
	if err != nil {
		return fmt.Errorf("gphotos importer: %v", err)
	}
	photosNode, err := r.getTopLevelNode("photos")
	if err != nil {
		return fmt.Errorf("gphotos importer: get top level node: %v", err)
	}
	for batch := range photos {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if err := r.importPhotosBatch(ctx, photosNode, batch.photos); err != nil {
			return err
		}
		if batch.err != nil {
			return err
		}
	}
	if r.setNextToken != nil {
		r.setNextToken(nextToken)
	}
	return nil
}

func (r *run) importPhotosBatch(ctx context.Context, parent *importer.Object, photos []photo) error {
	var grp syncutil.Group
	for _, photo := range photos {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		photo := photo
		r.photoGate.Start()
		grp.Go(func() error {
			defer r.photoGate.Done()
			return r.updatePhoto(ctx, parent, photo)
		})
	}
	return grp.Err()
}

const attrMediaURL = "gphotosMediaURL"

func (r *run) updatePhoto(ctx context.Context, parent *importer.Object, photo photo) (ret error) {
	if photo.ID == "" {
		return errors.New("photo has no ID")
	}

	getMediaBytes := func() (io.ReadCloser, error) { return r.downloader.openPhoto(ctx, photo) }

	// fileRefStr, in addition to being used as the camliConent value, is used
	// as a sentinel below.  If it is not empty after the call to
	// parent.ChildPathObjectOrFunc, it means we've just written the photo
	// contents, and hence there is no need to write them again, or even to
	// check if they have changed since the last import.
	var fileRefStr string

	fn := photo.Name
	if fn == "" {
		if fn = photo.OriginalFilename; fn == "" {
			fn = photo.ID
		}
	}
	purgedFn := strings.Replace(fn, "/", "-", -1)
	photoNode, err := parent.ChildPathObjectOrFunc(photo.ID, func() (*importer.Object, error) {
		h := blob.NewHash()
		rc, err := getMediaBytes()
		if err != nil {
			return nil, err
		}
		fileRef, err := schema.WriteFileFromReader(r.Host.Target(), purgedFn, io.TeeReader(rc, h))
		rc.Close()
		if err != nil {
			return nil, err
		}
		fileRefStr = fileRef.String()
		wholeRef := blob.RefFromHash(h)
		if pn, err := findExistingPermanode(r.Host.Searcher(), wholeRef); err == nil {
			return r.Host.ObjectFromRef(pn)
		}
		return r.Host.NewObject()
	})
	if err != nil {
		if fileRefStr != "" {
			return fmt.Errorf("error getting permanode for photo %q, with content %v: $v", photo.ID, fileRefStr, err)
		}
		return fmt.Errorf("error getting permanode for photo %q: %v", photo.ID, err)
	}

	// fileRefStr == "" means the photo contents were downloaded in a previous
	// import run, or even by another tool. So we re-download them below, if
	// necessary.
	if fileRefStr == "" {
		fileRefStr = photoNode.Attr(nodeattr.CamliContent)
		// Only re-download the source photo if its URL has changed.
		// Empirically this seems to work: cropping a photo in the
		// photos.google.com UI causes its URL to change. And it makes
		// sense, looking at the ugliness of the URLs with all their
		// encoded/signed state.
		if !mediaURLsEqual(photoNode.Attr(attrMediaURL), photo.WebContentLink) {
			rc, err := getMediaBytes()
			if err != nil {
				return err
			}
			fileRef, err := schema.WriteFileFromReader(r.Host.Target(), purgedFn, rc)
			rc.Close()
			if err != nil {
				return err
			}
			fileRefStr = fileRef.String()
		}
	}

	title := strings.TrimSpace(photo.Description)
	if strings.Contains(title, "\n") {
		title = title[:strings.Index(title, "\n")]
	}
	if title == "" && schema.IsInterestingTitle(fn) {
		title = fn
	}

	// TODO(tgulacsi): add more attrs (comments ?)
	// for names, see http://schema.org/ImageObject and http://schema.org/CreativeWork
	attrs := []string{
		nodeattr.CamliContent, fileRefStr,
		attrgphotosId, photo.ID,
		nodeattr.Version, strconv.FormatInt(photo.Version, 10),
		nodeattr.Title, title,
		nodeattr.Description, photo.Description,
		nodeattr.DateCreated, schema.RFC3339FromTime(photo.CreatedTime),
		nodeattr.DateModified, schema.RFC3339FromTime(photo.ModifiedTime),
		nodeattr.URL, photo.WebContentLink,
	}
	if photo.Location != nil {
		if photo.Location.Altitude != 0 {
			attrs = append(attrs, nodeattr.Altitude, floatToString(photo.Location.Altitude))
		}
		if photo.Location.Latitude != 0 || photo.Location.Longitude != 0 {
			attrs = append(attrs,
				nodeattr.Latitude, floatToString(photo.Location.Latitude),
				nodeattr.Longitude, floatToString(photo.Location.Longitude),
			)
		}
	}
	if err := photoNode.SetAttrs(attrs...); err != nil {
		return err
	}

	// Do this last, after we're sure the "camliContent" attribute
	// has been saved successfully, because this is the one that
	// causes us to do it again in the future or not.
	if err := photoNode.SetAttrs(attrMediaURL, photo.WebContentLink); err != nil {
		return err
	}
	return nil
}

func (r *run) displayName() string {
	acctNode := r.AccountNode()

	// e.g. "Jane Smith"
	if name := acctNode.Attr(importer.AcctAttrName); name != "" {
		// Keep only the given name, for a shorter title.
		// e.g. "Jane"
		return strings.Fields(name)[0]
	}

	// e.g. "jane.smith@gmail.com"
	if name := acctNode.Attr(importer.AcctAttrUserName); name != "" {
		// Keep only the first part of the e-mail address, for a shorter title.
		// e.g. "jane"
		return strings.SplitN(name, ".", 2)[0]
	}

	// e.g. 08054589012345261101
	return acctNode.Attr(importer.AcctAttrUserID)
}

func (r *run) getTopLevelNode(path string) (*importer.Object, error) {
	root := r.RootNode()
	name := r.displayName()
	rootTitle := fmt.Sprintf("%s's Google Photos Data", name)
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
	case "photos":
		title = fmt.Sprintf("%s's Google Photos", name)
	}
	return obj, obj.SetAttr(nodeattr.Title, title)
}

var sensitiveAttrs = []string{
	nodeattr.Type,
	attrgphotosId,
	nodeattr.Title,
	nodeattr.DateModified,
	nodeattr.DatePublished,
	nodeattr.Latitude,
	nodeattr.Longitude,
	nodeattr.Description,
}

// findExistingPermanode finds an existing permanode that has a
// camliContent pointing to a file with the provided wholeRef and
// doesn't have any conflicting attributes that would prevent the
// gphotos importer from re-using that permanode for its own use.
func findExistingPermanode(qs search.QueryDescriber, wholeRef blob.Ref) (pn blob.Ref, err error) {
	res, err := qs.Query(&search.SearchQuery{
		Constraint: &search.Constraint{
			Permanode: &search.PermanodeConstraint{
				Attr: "camliContent",
				ValueInSet: &search.Constraint{
					File: &search.FileConstraint{
						WholeRef: wholeRef,
					},
				},
			},
		},
		Describe: &search.DescribeRequest{
			Depth: 1,
		},
	})
	if err != nil {
		return
	}
	if res.Describe == nil {
		return pn, os.ErrNotExist
	}
Res:
	for _, resBlob := range res.Blobs {
		br := resBlob.Blob
		desBlob, ok := res.Describe.Meta[br.String()]
		if !ok || desBlob.Permanode == nil {
			continue
		}
		attrs := desBlob.Permanode.Attr
		for _, attr := range sensitiveAttrs {
			if attrs.Get(attr) != "" {
				continue Res
			}
		}
		return br, nil
	}
	return pn, os.ErrNotExist
}

func mediaURLsEqual(a, b string) bool {
	const sub = ".googleusercontent.com/"
	ai := strings.Index(a, sub)
	bi := strings.Index(b, sub)
	if ai >= 0 && bi >= 0 {
		return a[ai:] == b[bi:]
	}
	return a == b
}

func floatToString(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }
