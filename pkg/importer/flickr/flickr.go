/*
Copyright 2013 The Camlistore Authors

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

// Package flickr implements an importer for flickr.com accounts.
package flickr

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"camlistore.org/pkg/httputil"
	"camlistore.org/pkg/importer"
	"camlistore.org/pkg/schema"
	"camlistore.org/pkg/schema/nodeattr"

	"camlistore.org/third_party/github.com/garyburd/go-oauth/oauth"

	"go4.org/ctxutil"
)

const (
	apiURL                        = "https://api.flickr.com/services/rest/"
	temporaryCredentialRequestURL = "https://www.flickr.com/services/oauth/request_token"
	resourceOwnerAuthorizationURL = "https://www.flickr.com/services/oauth/authorize"
	tokenRequestURL               = "https://www.flickr.com/services/oauth/access_token"

	photosetsAPIPath = "flickr.photosets.getList"
	photosetAPIPath  = "flickr.photosets.getPhotos"
	photosAPIPath    = "flickr.people.getPhotos"

	attrFlickrId = "flickrId"
)

var oAuthURIs = importer.OAuthURIs{
	TemporaryCredentialRequestURI: temporaryCredentialRequestURL,
	ResourceOwnerAuthorizationURI: resourceOwnerAuthorizationURL,
	TokenRequestURI:               tokenRequestURL,
}

func init() {
	importer.Register("flickr", imp{})
}

var _ importer.ImporterSetupHTMLer = imp{}

type imp struct {
	importer.OAuth1 // for CallbackRequestAccount and CallbackURLParameters
}

func (imp) NeedsAPIKey() bool { return true }

func (imp) SupportsIncremental() bool { return false }

func (imp) IsAccountReady(acctNode *importer.Object) (ok bool, err error) {
	return acctNode.Attr(importer.AcctAttrUserName) != "" && acctNode.Attr(importer.AcctAttrAccessToken) != "", nil
}

func (im imp) SummarizeAccount(acct *importer.Object) string {
	ok, err := im.IsAccountReady(acct)
	if err != nil || !ok {
		return ""
	}
	return acct.Attr(importer.AcctAttrUserName)
}

func (imp) AccountSetupHTML(host *importer.Host) string {
	base := host.ImporterBaseURL() + "flickr"
	return fmt.Sprintf(`
<h1>Configuring Flickr</h1>
<p>Visit <a href='http://www.flickr.com/services/apps/create/noncommercial/'>http://www.flickr.com/services/apps/create/noncommercial/</a>, fill out whatever's needed, and click on SUBMIT.</p>
<p>From your newly created app's main page, go to "Edit the authentication flow", use the following settings:</p>
<ul>
  <li>App Type: Web Application</li>
  <li>Callback URL: <b>%s</b></li>
</ul>
<p> and SAVE CHANGES </p>
<p>Then go to "View the API Key for this app", and copy the "Key" and "Secret" into the "Client ID" and "Client Secret" boxes above.</p>
`, base+"/callback")
}

// A run is our state for a given run of the importer.
type run struct {
	userID string
	*importer.RunContext
	oauthClient *oauth.Client      // No need to guard, used read-only.
	accessCreds *oauth.Credentials // No need to guard, used read-only.

	// primaryPhoto maps an album id to the id of its primary photo.
	// If some concurrency is added to some of the importing routines,
	// it will need some guarding.
	primaryPhoto map[string]string
}

func (imp) Run(ctx *importer.RunContext) error {
	clientID, secret, err := ctx.Credentials()
	if err != nil {
		return fmt.Errorf("no API credentials: %v", err)
	}
	accountNode := ctx.AccountNode()
	accessToken := accountNode.Attr(importer.AcctAttrAccessToken)
	accessSecret := accountNode.Attr(importer.AcctAttrAccessTokenSecret)
	if accessToken == "" || accessSecret == "" {
		return errors.New("access credentials not found")
	}
	userID := ctx.AccountNode().Attr(importer.AcctAttrUserID)
	if userID == "" {
		return errors.New("UserID hasn't been set by account setup.")
	}
	r := &run{
		userID:     userID,
		RunContext: ctx,
		oauthClient: &oauth.Client{
			TemporaryCredentialRequestURI: temporaryCredentialRequestURL,
			ResourceOwnerAuthorizationURI: resourceOwnerAuthorizationURL,
			TokenRequestURI:               tokenRequestURL,
			Credentials: oauth.Credentials{
				Token:  clientID,
				Secret: secret,
			},
		},
		accessCreds: &oauth.Credentials{
			Token:  accessToken,
			Secret: accessSecret,
		},
		primaryPhoto: make(map[string]string),
	}

	if err := r.importPhotosets(); err != nil {
		return err
	}
	if err := r.importPhotos(); err != nil {
		return err
	}
	return nil
}

type photosetList struct {
	Page     jsonInt
	Pages    jsonInt
	PerPage  jsonInt
	Photoset []*photosetInfo
}

type photosetInfo struct {
	Id             string `json:"id"`
	PrimaryPhotoId string `json:"primary"`
	Title          contentString
	Description    contentString
}

type photosetItems struct {
	Id    string `json:"id"`
	Page  jsonInt
	Pages jsonInt
	Photo []struct {
		Id             string
		OriginalFormat string
	}
}

func (r *run) importPhotosets() error {
	resp := struct {
		Photosets photosetList
	}{}
	if err := r.flickrAPIRequest(&resp,
		photosetsAPIPath, "user_id", r.userID); err != nil {
		return err
	}

	setsNode, err := r.getTopLevelNode("sets", "Sets")
	if err != nil {
		return err
	}
	log.Printf("Importing %d sets", len(resp.Photosets.Photoset))

	for _, item := range resp.Photosets.Photoset {
		select {
		case <-r.Done():
			log.Printf("Flickr importer: interrupted")
			return r.Err()
		default:
		}
		for page := 1; page >= 1; {
			page, err = r.importPhotoset(setsNode, item, page)
			if err != nil {
				log.Printf("Flickr importer: error importing photoset %s: %s", item.Id, err)
				continue
			}
		}
	}
	return nil
}

func (r *run) importPhotoset(parent *importer.Object, photoset *photosetInfo, page int) (int, error) {
	photosetNode, err := parent.ChildPathObject(photoset.Id)
	if err != nil {
		return 0, err
	}

	if err := photosetNode.SetAttrs(
		attrFlickrId, photoset.Id,
		nodeattr.Title, photoset.Title.Content,
		nodeattr.Description, photoset.Description.Content); err != nil {
		return 0, err
	}
	// keep track of primary photo so we can set the fileRef of the photo as CamliContentImage
	// on photosetNode when we eventually know that fileRef.
	r.primaryPhoto[photoset.Id] = photoset.PrimaryPhotoId

	resp := struct {
		Photoset photosetItems
	}{}
	if err := r.flickrAPIRequest(&resp, photosetAPIPath, "user_id", r.userID,
		"page", fmt.Sprintf("%d", page), "photoset_id", photoset.Id, "extras", "original_format"); err != nil {
		return 0, err
	}

	log.Printf("Importing page %d from photoset %s", page, photoset.Id)

	photosNode, err := r.getPhotosNode()
	if err != nil {
		return 0, err
	}

	for _, item := range resp.Photoset.Photo {
		filename := fmt.Sprintf("%s.%s", item.Id, item.OriginalFormat)
		photoNode, err := photosNode.ChildPathObject(filename)
		if err != nil {
			log.Printf("Flickr importer: error finding photo node %s for addition to photoset %s: %s",
				item.Id, photoset.Id, err)
			continue
		}
		if err := photosetNode.SetAttr("camliPath:"+filename, photoNode.PermanodeRef().String()); err != nil {
			log.Printf("Flickr importer: error adding photo %s to photoset %s: %s",
				item.Id, photoset.Id, err)
		}
	}

	if resp.Photoset.Page < resp.Photoset.Pages {
		return page + 1, nil
	} else {
		return 0, nil
	}
}

type photosSearch struct {
	Photos struct {
		Page    jsonInt
		Pages   jsonInt
		Perpage jsonInt
		Total   jsonInt
		Photo   []*photosSearchItem
	}

	Stat string
}

type photosSearchItem struct {
	Id             string `json:"id"`
	Title          string
	IsPublic       jsonInt
	IsFriend       jsonInt
	IsFamily       jsonInt
	Description    contentString
	DateUpload     string // Unix timestamp, in GMT.
	DateTaken      string // formatted as "2006-01-02 15:04:05", so no timezone info.
	OriginalFormat string
	LastUpdate     string // Unix timestamp.
	Latitude       jsonFloat
	Longitude      jsonFloat
	Tags           string
	MachineTags    string `json:"machine_tags"`
	Views          string
	Media          string
	URL            string `json:"url_o"`
}

type contentString struct {
	Content string `json:"_content"`
}

// jsonInt is for unmarshaling quoted and unquoted integers ("0" and 0), too.
type jsonInt int

func (jf jsonInt) MarshalJSON() ([]byte, error) {
	return json.Marshal(int(jf))
}
func (jf *jsonInt) UnmarshalJSON(p []byte) error {
	return json.Unmarshal(bytes.Trim(p, `"`), (*int)(jf))
}

// jsonFloat is for unmarshaling quoted and unquoted numbers ("0" and 0), too.
type jsonFloat float32

func (jf jsonFloat) MarshalJSON() ([]byte, error) {
	return json.Marshal(float32(jf))
}
func (jf *jsonFloat) UnmarshalJSON(p []byte) error {
	if len(p) == 1 && p[0] == '0' { // shortcut
		*jf = 0
		return nil
	}
	return json.Unmarshal(bytes.Trim(p, `"`), (*float32)(jf))
}

func (r *run) importPhotos() error {
	for page := 1; page >= 1; {
		var err error
		page, err = r.importPhotosPage(page)
		if err != nil {
			return err
		}
	}
	return nil
}

func (r *run) importPhotosPage(page int) (int, error) {
	resp := photosSearch{}
	if err := r.flickrAPIRequest(&resp, photosAPIPath, "user_id", r.userID, "page", fmt.Sprintf("%d", page),
		"extras", "description,date_upload,date_taken,original_format,last_update,geo,tags,machine_tags,views,media,url_o"); err != nil {
		return 0, err
	}

	photosNode, err := r.getPhotosNode()
	if err != nil {
		return 0, err
	}
	log.Printf("Importing %d photos on page %d of %d", len(resp.Photos.Photo), page, resp.Photos.Pages)

	for _, item := range resp.Photos.Photo {
		if err := r.importPhoto(photosNode, item); err != nil {
			log.Printf("Flickr importer: error importing %s: %s", item.Id, err)
			continue
		}
	}

	if resp.Photos.Pages > resp.Photos.Page {
		return page + 1, nil
	} else {
		return 0, nil
	}
}

// TODO(aa):
// * Parallelize: http://golang.org/doc/effective_go.html#concurrency
// * Do more than one "page" worth of results
// * Report progress and errors back through host interface
// * All the rest of the metadata (see photoMeta)
// * Conflicts: For all metadata changes, prefer any non-imported claims
// * Test!
func (r *run) importPhoto(parent *importer.Object, photo *photosSearchItem) error {
	filename := fmt.Sprintf("%s.%s", photo.Id, photo.OriginalFormat)
	photoNode, err := parent.ChildPathObject(filename)
	if err != nil {
		return err
	}

	// https://www.flickr.com/services/api/misc.dates.html
	dateTaken, err := time.ParseInLocation("2006-01-02 15:04:05", photo.DateTaken, schema.UnknownLocation)
	if err != nil {
		// default to the published date otherwise
		log.Printf("Flickr importer: problem with date taken of photo %v, defaulting to published date instead.", photo.Id)
		seconds, err := strconv.ParseInt(photo.DateUpload, 10, 64)
		if err != nil {
			return fmt.Errorf("could not parse date upload time %q for image %v: %v", photo.DateUpload, photo.Id, err)
		}
		dateTaken = time.Unix(seconds, 0)
	}

	attrs := []string{
		attrFlickrId, photo.Id,
		nodeattr.DateCreated, schema.RFC3339FromTime(dateTaken),
		nodeattr.Description, photo.Description.Content,
	}
	if schema.IsInterestingTitle(photo.Title) {
		attrs = append(attrs, nodeattr.Title, photo.Title)
	}
	// Import all the metadata. SetAttrs() is a no-op if the value hasn't changed, so there's no cost to doing these on every run.
	// And this way if we add more things to import, they will get picked up.
	if err := photoNode.SetAttrs(attrs...); err != nil {
		return err
	}

	// Import the photo itself. Since it is expensive to fetch the image, we store its lastupdate and only refetch if it might have changed.
	// lastupdate is a Unix timestamp according to https://www.flickr.com/services/api/flickr.photos.getInfo.html
	seconds, err := strconv.ParseInt(photo.LastUpdate, 10, 64)
	if err != nil {
		return fmt.Errorf("could not parse lastupdate time for image %v: %v", photo.Id, err)
	}
	lastUpdate := time.Unix(seconds, 0)
	if lastUpdateString := photoNode.Attr(nodeattr.DateModified); lastUpdateString != "" {
		oldLastUpdate, err := time.Parse(time.RFC3339, lastUpdateString)
		if err != nil {
			return fmt.Errorf("could not parse last stored update time for image %v: %v", photo.Id, err)
		}
		if lastUpdate.Equal(oldLastUpdate) {
			if err := r.updatePrimaryPhoto(photoNode); err != nil {
				return err
			}
			return nil
		}
	}
	form := url.Values{}
	form.Set("user_id", r.userID)
	res, err := r.fetch(photo.URL, form)
	if err != nil {
		log.Printf("Flickr importer: Could not fetch %s: %s", photo.URL, err)
		return err
	}
	defer res.Body.Close()

	fileRef, err := schema.WriteFileFromReader(r.Host.Target(), filename, res.Body)
	if err != nil {
		return err
	}
	if err := photoNode.SetAttr(nodeattr.CamliContent, fileRef.String()); err != nil {
		return err
	}
	if err := r.updatePrimaryPhoto(photoNode); err != nil {
		return err
	}
	// Write lastupdate last, so that if any of the preceding fails, we will try again next time.
	if err := photoNode.SetAttr(nodeattr.DateModified, schema.RFC3339FromTime(lastUpdate)); err != nil {
		return err
	}

	return nil
}

// updatePrimaryPhoto uses the camliContent of photoNode to set the
// camliContentImage of any album for which photoNode is the primary photo.
func (r *run) updatePrimaryPhoto(photoNode *importer.Object) error {
	photoId := photoNode.Attr(attrFlickrId)
	for album, photo := range r.primaryPhoto {
		if photoId != photo {
			continue
		}
		setsNode, err := r.getTopLevelNode("sets", "Sets")
		if err != nil {
			return fmt.Errorf("could not set %v as primary photo of %v, no root sets: %v", photoId, album, err)
		}
		setNode, err := setsNode.ChildPathObject(album)
		if err != nil {
			return fmt.Errorf("could not set %v as primary photo of %v, no album: %v", photoId, album, err)
		}
		fileRef := photoNode.Attr(nodeattr.CamliContent)
		if fileRef == "" {
			return fmt.Errorf("could not set %v as primary photo of %v: fileRef of photo is unknown", photoId, album)
		}
		if err := setNode.SetAttr(nodeattr.CamliContentImage, fileRef); err != nil {
			return fmt.Errorf("could not set %v as primary photo of %v: %v", photoId, album, err)
		}
		delete(r.primaryPhoto, album)
	}
	return nil
}

func (r *run) getPhotosNode() (*importer.Object, error) {
	return r.getTopLevelNode("photos", "Photos")
}

func (r *run) getTopLevelNode(path string, title string) (*importer.Object, error) {
	photos, err := r.RootNode().ChildPathObject(path)
	if err != nil {
		return nil, err
	}

	if err := photos.SetAttr(nodeattr.Title, title); err != nil {
		return nil, err
	}
	return photos, nil
}

func (r *run) flickrAPIRequest(result interface{}, method string, keyval ...string) error {
	keyval = append([]string{"method", method, "format", "json", "nojsoncallback", "1"}, keyval...)
	return importer.OAuthContext{
		r.Context,
		r.oauthClient,
		r.accessCreds}.PopulateJSONFromURL(result, apiURL, keyval...)
}

func (r *run) fetch(url string, form url.Values) (*http.Response, error) {
	return importer.OAuthContext{
		r.Context,
		r.oauthClient,
		r.accessCreds}.Get(url, form)
}

// TODO(mpl): same in twitter. refactor. Except for the additional perms in AuthorizationURL call.
func (imp) ServeSetup(w http.ResponseWriter, r *http.Request, ctx *importer.SetupContext) error {
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

	authURL := oauthClient.AuthorizationURL(tempCred, url.Values{"perms": {"read"}})
	http.Redirect(w, r, authURL, http.StatusFound)
	return nil
}

func (imp) ServeCallback(w http.ResponseWriter, r *http.Request, ctx *importer.SetupContext) {
	tempToken := ctx.AccountNode.Attr(importer.AcctAttrTempToken)
	tempSecret := ctx.AccountNode.Attr(importer.AcctAttrTempSecret)
	if tempToken == "" || tempSecret == "" {
		log.Printf("flicker: no temp creds in callback")
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
	userID := vals.Get("user_nsid")
	if userID == "" {
		httputil.ServeError(w, r, fmt.Errorf("Couldn't get user id: %v", err))
		return
	}
	username := vals.Get("username")
	if username == "" {
		httputil.ServeError(w, r, fmt.Errorf("Couldn't get user name: %v", err))
		return
	}

	// TODO(mpl): get a few more bits of info (first name, last name etc) like I did for twitter, if possible.
	if err := ctx.AccountNode.SetAttrs(
		importer.AcctAttrAccessToken, tokenCred.Token,
		importer.AcctAttrAccessTokenSecret, tokenCred.Secret,
		importer.AcctAttrUserID, userID,
		importer.AcctAttrUserName, username,
	); err != nil {
		httputil.ServeError(w, r, fmt.Errorf("Error setting basic account attributes: %v", err))
		return
	}
	http.Redirect(w, r, ctx.AccountURL(), http.StatusFound)
}
