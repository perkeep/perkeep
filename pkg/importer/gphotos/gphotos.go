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

// Package gphotos implements a Google Photos importer, using the Google Drive
// API to access the Google Photos folder.
package gphotos // import "camlistore.org/pkg/importer/gphotos"

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
	"camlistore.org/pkg/importer/picasa"
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
	runCompleteVersion = "0"

	// attrDriveId is the Google Drive object ID of the photo.
	attrDriveId = "driveId"

	// acctAttrOAuthToken stores access + " " + refresh + " " + expiry
	// See encodeToken and decodeToken.
	acctAttrOAuthToken = "oauthToken"

	// acctSinceToken store the GPhotos-returned nextToken
	acctSinceToken = "sinceToken"
)

var (
	logger = log.New(os.Stderr, "gphotos: ", log.LstdFlags)
	logf   = logger.Printf
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
		logf("token exchange error: %v", err)
		httputil.ServeError(w, r, fmt.Errorf("token exchange error: %v", err))
		return
	}

	gphotosCtx := context.WithValue(ctx, ctxutil.HTTPClient, oauthConfig.Client(ctx, token))

	userInfo, err := im.getUserInfo(gphotosCtx)
	if err != nil {
		logf("couldn't get username: %v", err)
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
	// Google Cloud credentials require a URI of the kind scheme://domain for
	// javascript origins, so we strip the path.
	origin := host.ImporterBaseURL()
	if u, err := url.Parse(origin); err == nil {
		u.Path = ""
		origin = u.String()
	}

	callback := host.ImporterBaseURL() + "gphotos/callback"
	return fmt.Sprintf(`
<h1>Configuring Google Photos</h1>
<p>Please note that because of limitations of the Google Photos folder, this importer can only retrieve photos as they were originally uploaded, and not as they currently are in Google Photos, if modified.</p>
<p>First, you need to enable the Google Photos folder in the <a href='https://drive.google.com/'>Google Drive</a> settings.</p>
<p>Then visit <a href='https://console.developers.google.com/'>https://console.developers.google.com/</a>
and create a new project.</p>
<p>Then under "API Manager" in the left sidebar, click on "Credentials", then click the button <b>"Create credentials"</b>, and pick <b>"OAuth client ID"</b>.</p>
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

var forceFullImport, _ = strconv.ParseBool(os.Getenv("CAMLI_GPHOTOS_FULL_IMPORT"))

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
			fmt.Sprintf("%s's Google Photos Data", acctNode.Attr(importer.AcctAttrName)),
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
	photosCh, nextToken, err := r.downloader.photos(ctx, sinceToken)
	if err != nil {
		return fmt.Errorf("gphotos importer: %v", err)
	}
	photosNode, err := r.getTopLevelNode("photos")
	if err != nil {
		return fmt.Errorf("gphotos importer: get top level node: %v", err)
	}
	for batch := range photosCh {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if batch.err != nil {
			return err
		}
		if err := r.importPhotosBatch(ctx, photosNode, batch.photos); err != nil {
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

func (ph photo) filename() string {
	filename := ph.Name
	if filename == "" {
		filename = ph.OriginalFilename
	}
	if filename == "" {
		filename = ph.ID
	}
	return strings.Replace(filename, "/", "-", -1)
}

func orAltAttr(attr, alt string) string {
	if attr != "" {
		return attr
	}
	return alt
}

func (ph photo) title(altTitle string) string {
	title := strings.TrimSpace(ph.Description)
	if title == "" {
		title = orAltAttr(title, altTitle)
	}
	filename := ph.filename()
	if title == "" && schema.IsInterestingTitle(filename) {
		title = filename
	}
	if strings.Contains(title, "\n") {
		title = title[:strings.Index(title, "\n")]
	}
	return title
}

// updatePhoto creates a new permanode with the attributes of photo, or updates
// an existing one if appropriate. It also downloads the photo contents when
// needed. In particular, it reuses a permanode created by the picasa importer if
// that permanode seems to be about the same photo contents, to avoid what would
// look like duplicates. For now, it can handle the following cases:
// 1) No permanode for the photo object exists, and no permanode for the
// contents of the photo exists. So it creates a new one.
// 2) No permanode for the photo object exists, but a picasa permanode for the
// same contents exists. So we reuse the picasa node.
// 3) No permanode for the photo object exists, but a permanode for the same
// contents, and with no conflicting attributes, exists. So we reuse that
// permanode.
// 4) A permanode for the photo object already exists, so we reuse it.
func (r *run) updatePhoto(ctx context.Context, parent *importer.Object, ph photo) (ret error) {
	if ph.ID == "" {
		return errors.New("photo has no ID")
	}

	// fileRefStr, in addition to being used as the camliConent value, is used
	// as a sentinel: if it is still blank after the call to
	// ChildPathObjectOrFunc, it means that a permanode for the photo object
	// already exists.
	var fileRefStr string
	// picasAttrs holds the attributes of the picasa node for the photo, if any is found.
	var picasAttrs url.Values

	filename := ph.filename()

	photoNode, err := parent.ChildPathObjectOrFunc(ph.ID, func() (*importer.Object, error) {
		h := blob.NewHash()
		rc, err := r.downloader.openPhoto(ctx, ph)
		if err != nil {
			return nil, err
		}
		fileRef, err := schema.WriteFileFromReader(r.Host.Target(), filename, io.TeeReader(rc, h))
		rc.Close()
		if err != nil {
			return nil, err
		}
		fileRefStr = fileRef.String()
		wholeRef := blob.RefFromHash(h)
		pn, attrs, err := findExistingPermanode(r.Host.Searcher(), wholeRef)
		if err != nil {
			if err != os.ErrNotExist {
				return nil, fmt.Errorf("could not look for permanode with %v as camliContent : %v", fileRefStr, err)
			}
			return r.Host.NewObject()
		}
		if attrs != nil {
			picasAttrs = attrs
		}
		return r.Host.ObjectFromRef(pn)
	})
	if err != nil {
		if fileRefStr != "" {
			return fmt.Errorf("error getting permanode for photo %q, with content %v: $v", ph.ID, fileRefStr, err)
		}
		return fmt.Errorf("error getting permanode for photo %q: %v", ph.ID, err)
	}

	if fileRefStr == "" {
		// photoNode was created in a previous run, but it is not
		// guaranteed its attributes were set. e.g. the importer might have
		// been interrupted. So we check for an existing camliContent.
		if camliContent := photoNode.Attr(nodeattr.CamliContent); camliContent == "" {
			// looks like an incomplete node, so we need to re-download.
			rc, err := r.downloader.openPhoto(ctx, ph)
			if err != nil {
				return err
			}
			fileRef, err := schema.WriteFileFromReader(r.Host.Target(), filename, rc)
			rc.Close()
			if err != nil {
				return err
			}
			fileRefStr = fileRef.String()
		}
	} else {
		if picasAttrs.Get(nodeattr.CamliContent) != "" {
			// We've just created a new file schema, but we're also recycling a
			// picasa node, and we prefer keeping the existing file schema from the
			// picasa node, because the file from Drive never gets updates
			// (https://productforums.google.com/forum/#!msg/drive/HbNOd1o40CQ/VfIJCncyAAAJ).
			// Thanks to blob deduplication, these two file schemas are most likely
			// the same anyway. If not, the newly created one will/should get GCed
			// eventually.
			fileRefStr = picasAttrs.Get(nodeattr.CamliContent)
		}
	}

	attrs := []string{
		attrDriveId, ph.ID,
		nodeattr.Version, strconv.FormatInt(ph.Version, 10),
		nodeattr.Title, ph.title(picasAttrs.Get(nodeattr.Title)),
		nodeattr.Description, orAltAttr(ph.Description, picasAttrs.Get(nodeattr.Description)),
		nodeattr.DateCreated, schema.RFC3339FromTime(ph.CreatedTime),
		nodeattr.DateModified, orAltAttr(schema.RFC3339FromTime(ph.ModifiedTime), picasAttrs.Get(nodeattr.DateModified)),
		// Even if the node already had some nodeattr.URL picasa attribute, it's
		// ok to overwrite it, because from what I've tested it's useless nowadays
		// (gives a 404 in a browser). Plus, we don't overwrite the actually useful
		// "picasaMediaURL" attribute.
		nodeattr.URL, ph.WebContentLink,
	}

	if ph.Location != nil {
		if ph.Location.Altitude != 0 {
			attrs = append(attrs, nodeattr.Altitude, floatToString(ph.Location.Altitude))
		}
		if ph.Location.Latitude != 0 || ph.Location.Longitude != 0 {
			attrs = append(attrs,
				nodeattr.Latitude, floatToString(ph.Location.Latitude),
				nodeattr.Longitude, floatToString(ph.Location.Longitude),
			)
		}
	}
	if err := photoNode.SetAttrs(attrs...); err != nil {
		return err
	}

	if fileRefStr != "" {
		// camliContent is set last, as its presence defines whether we consider a
		// photo successfully updated.
		if err := photoNode.SetAttr(nodeattr.CamliContent, fileRefStr); err != nil {
			return err
		}
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
	logf("root title = %q; want %q", root.Attr(nodeattr.Title), rootTitle)
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
	attrDriveId,
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
// If it finds a picasa permanode, it is returned immediately,
// as well as the existing attributes on the node, so the caller
// can merge them with whatever new attributes it wants to add to
// the node.
func findExistingPermanode(qs search.QueryDescriber, wholeRef blob.Ref) (pn blob.Ref, picasaAttrs url.Values, err error) {
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
		return pn, nil, os.ErrNotExist
	}
Res:
	for _, resBlob := range res.Blobs {
		br := resBlob.Blob
		desBlob, ok := res.Describe.Meta[br.String()]
		if !ok || desBlob.Permanode == nil {
			continue
		}
		attrs := desBlob.Permanode.Attr
		if attrs.Get(picasa.AttrMediaURL) != "" {
			// If we found a picasa permanode, we're going to reuse it, in order to avoid
			// creating what would look like duplicates. We let the caller deal with merging
			// properly on the node the existing (Picasa) attributes, with the new (Google
			// Photos) attributes.
			return br, attrs, nil
		}
		// otherwise, only keep it if attributes are not conflicting.
		for _, attr := range sensitiveAttrs {
			if attrs.Get(attr) != "" {
				continue Res
			}
		}
		return br, nil, nil
	}
	return pn, nil, os.ErrNotExist
}

func floatToString(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }
