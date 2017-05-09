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

// Package picasa implements an importer for picasa.com accounts.
package picasa // import "camlistore.org/pkg/importer/picasa"

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
	"github.com/tgulacsi/picago"
	"go4.org/ctxutil"
	"go4.org/syncutil"
	"golang.org/x/net/context"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

const (
	scopeURL = "https://picasaweb.google.com/data/"

	// runCompleteVersion is a cache-busting version number of the
	// importer code. It should be incremented whenever the
	// behavior of this importer is updated enough to warrant a
	// complete run.  Otherwise, if the importer runs to
	// completion, this version number is recorded on the account
	// permanode and subsequent importers can stop early.
	runCompleteVersion = "4"

	// attrPicasaId is used for both picasa photo IDs and gallery IDs.
	attrPicasaId = "picasaId"

	// acctAttrOAuthToken stores access + " " + refresh + " " + expiry
	// See encodeToken and decodeToken.
	acctAttrOAuthToken = "oauthToken"

	// AttrMediaURL is an attribute set on each picasa photo permanode. It
	// is the public URL for fetching the contents of the photo file.
	AttrMediaURL = "picasaMediaURL"
)

var (
	_ importer.Importer            = imp{}
	_ importer.ImporterSetupHTMLer = imp{}
)

func init() {
	importer.Register("picasa", imp{})
}

// imp is the implementation of the Picasa importer.
type imp struct {
	importer.OAuth2
}

func (imp) SupportsIncremental() bool { return true }

type userInfo struct {
	ID   string // numeric picasa user ID ("11583474931002155675")
	Name string // "Jane Smith"
}

func (imp) getUserInfo(ctx context.Context) (*userInfo, error) {
	u, err := picago.GetUser(ctxutil.Client(ctx), "default")
	if err != nil {
		return nil, err
	}
	return &userInfo{ID: u.ID, Name: u.Name}, nil
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
	if acct.Attr(importer.AcctAttrGivenName) == "" && acct.Attr(importer.AcctAttrFamilyName) == "" {
		return fmt.Sprintf("userid %s", acct.Attr(importer.AcctAttrUserID))
	}
	return fmt.Sprintf("userid %s (%s %s)",
		acct.Attr(importer.AcctAttrUserID),
		acct.Attr(importer.AcctAttrGivenName),
		acct.Attr(importer.AcctAttrFamilyName))
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

// CallbackURLParameters returns the needed callback parameters - empty for Google Picasa.
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
		log.Printf("importer/picasa: token exchange error: %v", err)
		httputil.ServeError(w, r, fmt.Errorf("token exchange error: %v", err))
		return
	}

	log.Printf("importer/picasa: got exhanged token.")
	picagoCtx := context.WithValue(ctx, ctxutil.HTTPClient, oauthConfig.Client(ctx, token))

	userInfo, err := im.getUserInfo(picagoCtx)
	if err != nil {
		log.Printf("Couldn't get username: %v", err)
		httputil.ServeError(w, r, fmt.Errorf("can't get username: %v", err))
		return
	}

	if err := ctx.AccountNode.SetAttrs(
		importer.AcctAttrUserID, userInfo.ID,
		importer.AcctAttrName, userInfo.Name,
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
		Scopes:       []string{scopeURL},
	}
	return conf, nil
}

func (imp) AccountSetupHTML(host *importer.Host) string {
	// Picasa doesn't allow a path in the origin. Remove it.
	origin := host.ImporterBaseURL()
	if u, err := url.Parse(origin); err == nil {
		u.Path = ""
		origin = u.String()
	}

	callback := host.ImporterBaseURL() + "picasa/callback"
	gphotosURL := host.ImporterBaseURL() + "gphotos"
	return fmt.Sprintf(`
<h1>Configuring Picasa</h1>
<p>Please note that because of a bug in the Picasa API, you cannot retrieve more than 10000 photos. If you have more than 10000 photos, you should use the <a href='%s'>Google Photos importer</a> instead.</p>
<p>Visit <a href='https://console.developers.google.com/'>https://console.developers.google.com/</a>
and click <b>"Create Project"</b>.</p>
<p>Then under "APIs & Auth" in the left sidebar, click on "Credentials", then click the button <b>"Create new Client ID"</b>.</p>
<p>Use the following settings:</p>
<ul>
  <li>Web application</li>
  <li>Authorized JavaScript origins: <b>%s</b></li>
  <li>Authorized Redirect URI: <b>%s</b></li>
</ul>
<p>Click "Create Client ID".  Copy the "Client ID" and "Client Secret" into the boxes above.</p>
`, gphotosURL, origin, callback)
}

// A run is our state for a given run of the importer.
type run struct {
	*importer.RunContext
	incremental bool // whether we've completed a run in the past
	photoGate   *syncutil.Gate
}

var forceFullImport, _ = strconv.ParseBool(os.Getenv("CAMLI_PICASA_FULL_IMPORT"))

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
		Scopes:       []string{scopeURL},
	}

	token := decodeToken(acctNode.Attr(acctAttrOAuthToken))
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

	r := &run{
		RunContext:  rctx,
		incremental: !forceFullImport && acctNode.Attr(importer.AcctAttrCompletedVersion) == runCompleteVersion,
		photoGate:   syncutil.NewGate(3),
	}
	if err := r.importAlbums(ctx); err != nil {
		return err
	}

	if err := acctNode.SetAttrs(importer.AcctAttrCompletedVersion, runCompleteVersion); err != nil {
		return err
	}

	return nil
}

func (r *run) importAlbums(ctx context.Context) error {
	albums, err := picago.GetAlbums(ctxutil.Client(ctx), "default")
	if err != nil {
		return fmt.Errorf("importAlbums: error listing albums: %v", err)
	}
	albumsNode, err := r.getTopLevelNode("albums", "Albums")
	for _, album := range albums {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if err := r.importAlbum(ctx, albumsNode, album); err != nil {
			return fmt.Errorf("picasa importer: error importing album %s: %v", album, err)
		}
	}
	return nil
}

func (r *run) importAlbum(ctx context.Context, albumsNode *importer.Object, album picago.Album) (ret error) {
	if album.ID == "" {
		return errors.New("album has no ID")
	}
	albumNode, err := albumsNode.ChildPathObject(album.ID)
	if err != nil {
		return fmt.Errorf("importAlbum: error listing album: %v", err)
	}

	dateMod := schema.RFC3339FromTime(album.Updated)

	// Data reference: https://developers.google.com/picasa-web/docs/2.0/reference
	// TODO(tgulacsi): add more album info
	changes, err := albumNode.SetAttrs2(
		attrPicasaId, album.ID,
		nodeattr.Type, "picasaweb.google.com:album",
		nodeattr.Title, album.Title,
		nodeattr.DatePublished, schema.RFC3339FromTime(album.Published),
		nodeattr.LocationText, album.Location,
		nodeattr.Description, album.Description,
		nodeattr.URL, album.URL,
	)
	if err != nil {
		return fmt.Errorf("error setting album attributes: %v", err)
	}
	if !changes && r.incremental && albumNode.Attr(nodeattr.DateModified) == dateMod {
		return nil
	}
	defer func() {
		// Don't update DateModified on the album node until
		// we've successfully imported all the photos.
		if ret == nil {
			ret = albumNode.SetAttr(nodeattr.DateModified, dateMod)
		}
	}()

	log.Printf("Importing album %v: %v/%v (published %v, updated %v)", album.ID, album.Name, album.Title, album.Published, album.Updated)

	// TODO(bradfitz): GetPhotos does multiple HTTP requests to
	// return a slice of all photos. My "InstantUpload/Auto
	// Backup" album has 6678 photos (and growing) and this
	// currently takes like 40 seconds. Fix.
	photos, err := picago.GetPhotos(ctxutil.Client(ctx), "default", album.ID)
	if err != nil {
		return err
	}

	log.Printf("Importing %d photos from album %q (%s)", len(photos), albumNode.Attr(nodeattr.Title),
		albumNode.PermanodeRef())

	var grp syncutil.Group
	for i := range photos {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		photo := photos[i]
		r.photoGate.Start()
		grp.Go(func() error {
			defer r.photoGate.Done()
			return r.updatePhotoInAlbum(ctx, albumNode, photo)
		})
	}
	return grp.Err()
}

func (r *run) updatePhotoInAlbum(ctx context.Context, albumNode *importer.Object, photo picago.Photo) (ret error) {
	if photo.ID == "" {
		return errors.New("photo has no ID")
	}

	getMediaBytes := func() (io.ReadCloser, error) {
		log.Printf("Importing media from %v", photo.URL)
		resp, err := ctxutil.Client(ctx).Get(photo.URL)
		if err != nil {
			return nil, fmt.Errorf("importing photo %s: %v", photo.ID, err)
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			return nil, fmt.Errorf("importing photo %s: status code = %d", photo.ID, resp.StatusCode)
		}
		return resp.Body, nil
	}

	var fileRefStr string
	idFilename := photo.ID + "-" + photo.Filename
	photoNode, err := albumNode.ChildPathObjectOrFunc(idFilename, func() (*importer.Object, error) {
		h := blob.NewHash()
		rc, err := getMediaBytes()
		if err != nil {
			return nil, err
		}
		fileRef, err := schema.WriteFileFromReader(r.Host.Target(), photo.Filename, io.TeeReader(rc, h))
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
		return err
	}

	if fileRefStr == "" {
		fileRefStr = photoNode.Attr(nodeattr.CamliContent)
		// Only re-download the source photo if its URL has changed.
		// Empirically this seems to work: cropping a photo in the
		// photos.google.com UI causes its URL to change. And it makes
		// sense, looking at the ugliness of the URLs with all their
		// encoded/signed state.
		if !mediaURLsEqual(photoNode.Attr(AttrMediaURL), photo.URL) {
			rc, err := getMediaBytes()
			if err != nil {
				return err
			}
			fileRef, err := schema.WriteFileFromReader(r.Host.Target(), photo.Filename, rc)
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
	if title == "" && schema.IsInterestingTitle(photo.Filename) {
		title = photo.Filename
	}

	// TODO(tgulacsi): add more attrs (comments ?)
	// for names, see http://schema.org/ImageObject and http://schema.org/CreativeWork
	attrs := []string{
		nodeattr.CamliContent, fileRefStr,
		attrPicasaId, photo.ID,
		nodeattr.Title, title,
		nodeattr.Description, photo.Description,
		nodeattr.LocationText, photo.Location,
		nodeattr.DateModified, schema.RFC3339FromTime(photo.Updated),
		nodeattr.DatePublished, schema.RFC3339FromTime(photo.Published),
		nodeattr.URL, photo.PageURL,
	}
	if photo.Latitude != 0 || photo.Longitude != 0 {
		attrs = append(attrs,
			nodeattr.Latitude, fmt.Sprintf("%f", photo.Latitude),
			nodeattr.Longitude, fmt.Sprintf("%f", photo.Longitude),
		)
	}
	if err := photoNode.SetAttrs(attrs...); err != nil {
		return err
	}
	if err := photoNode.SetAttrValues("tag", photo.Keywords); err != nil {
		return err
	}
	if photo.Position > 0 {
		if err := albumNode.SetAttr(
			nodeattr.CamliPathOrderColon+strconv.Itoa(photo.Position-1),
			photoNode.PermanodeRef().String()); err != nil {
			return err
		}
	}

	// Do this last, after we're sure the "camliContent" attribute
	// has been saved successfully, because this is the one that
	// causes us to do it again in the future or not.
	if err := photoNode.SetAttrs(AttrMediaURL, photo.URL); err != nil {
		return err
	}
	return nil
}

func (r *run) getTopLevelNode(path string, title string) (*importer.Object, error) {
	childObject, err := r.RootNode().ChildPathObject(path)
	if err != nil {
		return nil, err
	}

	if err := childObject.SetAttr(nodeattr.Title, title); err != nil {
		return nil, err
	}
	return childObject, nil
}

var sensitiveAttrs = []string{
	nodeattr.Type,
	attrPicasaId,
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
// picasa importer from re-using that permanode for its own use.
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
