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

// Package foursquare implements an importer for foursquare.com accounts.
package foursquare

import (
	"fmt"
	"log"
	"net/http"
	"net/url"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/httputil"
	"camlistore.org/pkg/importer"
	"camlistore.org/pkg/schema"
	"camlistore.org/pkg/schema/nodeattr"

	"camlistore.org/third_party/code.google.com/p/goauth2/oauth"

	"go4.org/ctxutil"
	"golang.org/x/net/context"
)

const (
	apiURL   = "https://api.foursquare.com/v2/"
	authURL  = "https://foursquare.com/oauth2/authenticate"
	tokenURL = "https://foursquare.com/oauth2/access_token"

	apiVersion      = "20140225"
	checkinsAPIPath = "users/self/checkins"

	// runCompleteVersion is a cache-busting version number of the
	// importer code. It should be incremented whenever the
	// behavior of this importer is updated enough to warrant a
	// complete run.  Otherwise, if the importer runs to
	// completion, this version number is recorded on the account
	// permanode and subsequent importers can stop early.
	runCompleteVersion = "1"

	// Permanode attributes on account node:
	acctAttrUserId      = "foursquareUserId"
	acctAttrUserFirst   = "foursquareFirstName"
	acctAttrUserLast    = "foursquareLastName"
	acctAttrAccessToken = "oauthAccessToken"

	checkinsRequestLimit = 100 // max number of checkins we will ask for in a checkins list request
	photosRequestLimit   = 5

	attrFoursquareId             = "foursquareId"
	attrFoursquareVenuePermanode = "foursquareVenuePermanode"
	attrFoursquareCategoryName   = "foursquareCategoryName"
)

func init() {
	importer.Register("foursquare", &imp{
		imageFileRef: make(map[string]blob.Ref),
	})
}

var _ importer.ImporterSetupHTMLer = (*imp)(nil)

type imp struct {
	mu           sync.Mutex          // guards following
	imageFileRef map[string]blob.Ref // url to file schema blob

	importer.OAuth2 // for CallbackRequestAccount and CallbackURLParameters
}

func (im *imp) NeedsAPIKey() bool         { return true }
func (im *imp) SupportsIncremental() bool { return true }

func (im *imp) IsAccountReady(acctNode *importer.Object) (ok bool, err error) {
	if acctNode.Attr(acctAttrUserId) != "" && acctNode.Attr(acctAttrAccessToken) != "" {
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
	if acct.Attr(acctAttrUserFirst) == "" && acct.Attr(acctAttrUserLast) == "" {
		return fmt.Sprintf("userid %s", acct.Attr(acctAttrUserId))
	}
	return fmt.Sprintf("userid %s (%s %s)", acct.Attr(acctAttrUserId),
		acct.Attr(acctAttrUserFirst), acct.Attr(acctAttrUserLast))
}

func (im *imp) AccountSetupHTML(host *importer.Host) string {
	base := host.ImporterBaseURL() + "foursquare"
	return fmt.Sprintf(`
<h1>Configuring Foursquare</h1>
<p>Visit <a href='https://foursquare.com/developers/apps'>https://foursquare.com/developers/apps</a> and click "Create a new app".</p>
<p>Use the following settings:</p>
<ul>
  <li>Download / welcome page url: <b>%s</b></li>
  <li>Your privacy policy url: <b>%s</b></li>
  <li>Redirect URI(s): <b>%s</b></li>
</ul>
<p>Click "SAVE CHANGES".  Copy the "Client ID" and "Client Secret" into the boxes above.</p>
`, base, base+"/privacy", base+"/callback")
}

// A run is our state for a given run of the importer.
type run struct {
	*importer.RunContext
	im          *imp
	incremental bool // whether we've completed a run in the past

	mu     sync.Mutex // guards anyErr
	anyErr bool
}

func (r *run) token() string {
	return r.RunContext.AccountNode().Attr(acctAttrAccessToken)
}

func (im *imp) Run(ctx *importer.RunContext) error {
	r := &run{
		RunContext:  ctx,
		im:          im,
		incremental: ctx.AccountNode().Attr(importer.AcctAttrCompletedVersion) == runCompleteVersion,
	}

	if err := r.importCheckins(); err != nil {
		return err
	}

	r.mu.Lock()
	anyErr := r.anyErr
	r.mu.Unlock()

	if !anyErr {
		if err := r.AccountNode().SetAttrs(importer.AcctAttrCompletedVersion, runCompleteVersion); err != nil {
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

// urlFileRef slurps urlstr from the net, writes to a file and returns its
// fileref or "" on error
func (r *run) urlFileRef(urlstr, filename string) string {
	im := r.im
	im.mu.Lock()
	if br, ok := im.imageFileRef[urlstr]; ok {
		im.mu.Unlock()
		return br.String()
	}
	im.mu.Unlock()

	res, err := ctxutil.Client(r).Get(urlstr)
	if err != nil {
		log.Printf("couldn't get image: %v", err)
		return ""
	}
	defer res.Body.Close()

	fileRef, err := schema.WriteFileFromReader(r.Host.Target(), filename, res.Body)
	if err != nil {
		r.errorf("couldn't write file: %v", err)
		return ""
	}

	im.mu.Lock()
	defer im.mu.Unlock()
	im.imageFileRef[urlstr] = fileRef
	return fileRef.String()
}

type byCreatedAt []*checkinItem

func (s byCreatedAt) Less(i, j int) bool { return s[i].CreatedAt < s[j].CreatedAt }
func (s byCreatedAt) Len() int           { return len(s) }
func (s byCreatedAt) Swap(i, j int)      { s[i], s[j] = s[j], s[i] }

func (r *run) importCheckins() error {
	limit := checkinsRequestLimit
	offset := 0
	continueRequests := true

	for continueRequests {
		resp := checkinsList{}
		if err := r.im.doAPI(r.Context, r.token(), &resp, checkinsAPIPath, "limit", strconv.Itoa(limit), "offset", strconv.Itoa(offset)); err != nil {
			return err
		}

		itemcount := len(resp.Response.Checkins.Items)
		log.Printf("foursquare: importing %d checkins (offset %d)", itemcount, offset)
		if itemcount < limit {
			continueRequests = false
		} else {
			offset += itemcount
		}

		checkinsNode, err := r.getTopLevelNode("checkins", "Checkins")
		if err != nil {
			return err
		}

		placesNode, err := r.getTopLevelNode("places", "Places")
		if err != nil {
			return err
		}

		sort.Sort(byCreatedAt(resp.Response.Checkins.Items))
		sawOldItem := false
		for _, checkin := range resp.Response.Checkins.Items {
			placeNode, err := r.importPlace(placesNode, &checkin.Venue)
			if err != nil {
				r.errorf("Foursquare importer: error importing place %s %v", checkin.Venue.Id, err)
				continue
			}

			_, dup, err := r.importCheckin(checkinsNode, checkin, placeNode.PermanodeRef())
			if err != nil {
				r.errorf("Foursquare importer: error importing checkin %s %v", checkin.Id, err)
				continue
			}

			if dup {
				sawOldItem = true
			}

			err = r.importPhotos(placeNode, dup)
			if err != nil {
				r.errorf("Foursquare importer: error importing photos for checkin %s %v", checkin.Id, err)
				continue
			}
		}
		if sawOldItem && r.incremental {
			break
		}
	}

	return nil
}

func (r *run) importPhotos(placeNode *importer.Object, checkinWasDup bool) error {
	photosNode, err := placeNode.ChildPathObject("photos")
	if err != nil {
		return err
	}

	if err := photosNode.SetAttrs(
		nodeattr.Title, "Photos of "+placeNode.Attr("title"),
		nodeattr.DefaultVisibility, "hide"); err != nil {
		return err
	}

	nHave := 0
	photosNode.ForeachAttr(func(key, value string) {
		if strings.HasPrefix(key, "camliPath:") {
			nHave++
		}
	})
	nWant := photosRequestLimit
	if checkinWasDup {
		nWant = 1
	}
	if nHave >= nWant {
		return nil
	}

	resp := photosList{}
	if err := r.im.doAPI(r.Context, r.token(), &resp,
		"venues/"+placeNode.Attr(attrFoursquareId)+"/photos",
		"limit", strconv.Itoa(nWant)); err != nil {
		return err
	}

	var need []*photoItem
	for _, photo := range resp.Response.Photos.Items {
		attr := "camliPath:" + photo.Id + filepath.Ext(photo.Suffix)
		if photosNode.Attr(attr) == "" {
			need = append(need, photo)
		}
	}

	if len(need) > 0 {
		venueTitle := placeNode.Attr(nodeattr.Title)
		log.Printf("foursquare: importing %d photos for venue %s", len(need), venueTitle)
		for _, photo := range need {
			attr := "camliPath:" + photo.Id + filepath.Ext(photo.Suffix)
			if photosNode.Attr(attr) != "" {
				continue
			}
			url := photo.Prefix + "original" + photo.Suffix
			log.Printf("foursquare: importing photo for venue %s: %s", venueTitle, url)
			ref := r.urlFileRef(url, "")
			if ref == "" {
				r.errorf("Error slurping photo: %s", url)
				continue
			}
			if err := photosNode.SetAttr(attr, ref); err != nil {
				r.errorf("Error adding venue photo: %#v", err)
			}
		}
	}

	return nil
}

func (r *run) importCheckin(parent *importer.Object, checkin *checkinItem, placeRef blob.Ref) (checkinNode *importer.Object, dup bool, err error) {
	checkinNode, err = parent.ChildPathObject(checkin.Id)
	if err != nil {
		return
	}

	title := fmt.Sprintf("Checkin at %s", checkin.Venue.Name)
	dup = checkinNode.Attr(nodeattr.StartDate) != ""
	if err := checkinNode.SetAttrs(
		attrFoursquareId, checkin.Id,
		attrFoursquareVenuePermanode, placeRef.String(),
		nodeattr.Type, "foursquare.com:checkin",
		nodeattr.StartDate, schema.RFC3339FromTime(time.Unix(checkin.CreatedAt, 0)),
		nodeattr.Title, title); err != nil {
		return nil, false, err
	}
	return checkinNode, dup, nil
}

func (r *run) importPlace(parent *importer.Object, place *venueItem) (*importer.Object, error) {
	placeNode, err := parent.ChildPathObject(place.Id)
	if err != nil {
		return nil, err
	}

	catName := ""
	if cat := place.primaryCategory(); cat != nil {
		catName = cat.Name
	}

	icon := place.icon()
	if err := placeNode.SetAttrs(
		attrFoursquareId, place.Id,
		nodeattr.Type, "foursquare.com:venue",
		nodeattr.CamliContentImage, r.urlFileRef(icon, path.Base(icon)),
		attrFoursquareCategoryName, catName,
		nodeattr.Title, place.Name,
		nodeattr.StreetAddress, place.Location.Address,
		nodeattr.AddressLocality, place.Location.City,
		nodeattr.PostalCode, place.Location.PostalCode,
		nodeattr.AddressRegion, place.Location.State,
		nodeattr.AddressCountry, place.Location.Country,
		nodeattr.Latitude, fmt.Sprint(place.Location.Lat),
		nodeattr.Longitude, fmt.Sprint(place.Location.Lng)); err != nil {
		return nil, err
	}

	return placeNode, nil
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

func (im *imp) getUserInfo(ctx context.Context, accessToken string) (user, error) {
	var ui userInfo
	if err := im.doAPI(ctx, accessToken, &ui, "users/self"); err != nil {
		return user{}, err
	}
	if ui.Response.User.Id == "" {
		return user{}, fmt.Errorf("No userid returned")
	}
	return ui.Response.User, nil
}

func (im *imp) doAPI(ctx context.Context, accessToken string, result interface{}, apiPath string, keyval ...string) error {
	if len(keyval)%2 == 1 {
		panic("Incorrect number of keyval arguments")
	}

	form := url.Values{}
	form.Set("v", apiVersion) // 4sq requires this to version their API
	form.Set("oauth_token", accessToken)
	for i := 0; i < len(keyval); i += 2 {
		form.Set(keyval[i], keyval[i+1])
	}

	fullURL := apiURL + apiPath
	res, err := doGet(ctx, fullURL, form)
	if err != nil {
		return err
	}
	err = httputil.DecodeJSON(res, result)
	if err != nil {
		log.Printf("Error parsing response for %s: %v", fullURL, err)
	}
	return err
}

func doGet(ctx context.Context, url string, form url.Values) (*http.Response, error) {
	requestURL := url + "?" + form.Encode()
	req, err := http.NewRequest("GET", requestURL, nil)
	if err != nil {
		return nil, err
	}
	res, err := ctxutil.Client(ctx).Do(req)
	if err != nil {
		log.Printf("Error fetching %s: %v", url, err)
		return nil, err
	}
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Get request on %s failed with: %s", requestURL, res.Status)
	}
	return res, nil
}

// auth returns a new oauth.Config
func auth(ctx *importer.SetupContext) (*oauth.Config, error) {
	clientId, secret, err := ctx.Credentials()
	if err != nil {
		return nil, err
	}
	return &oauth.Config{
		ClientId:     clientId,
		ClientSecret: secret,
		AuthURL:      authURL,
		TokenURL:     tokenURL,
		RedirectURL:  ctx.CallbackURL(),
	}, nil
}

func (im *imp) ServeSetup(w http.ResponseWriter, r *http.Request, ctx *importer.SetupContext) error {
	oauthConfig, err := auth(ctx)
	if err != nil {
		return err
	}
	oauthConfig.RedirectURL = im.RedirectURL(im, ctx)
	state, err := im.RedirectState(im, ctx)
	if err != nil {
		return err
	}
	http.Redirect(w, r, oauthConfig.AuthCodeURL(state), http.StatusFound)
	return nil
}

func (im *imp) ServeCallback(w http.ResponseWriter, r *http.Request, ctx *importer.SetupContext) {
	oauthConfig, err := auth(ctx)
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
	transport := &oauth.Transport{Config: oauthConfig}
	token, err := transport.Exchange(code)
	log.Printf("Token = %#v, error %v", token, err)
	if err != nil {
		log.Printf("Token Exchange error: %v", err)
		http.Error(w, "token exchange error", 500)
		return
	}

	u, err := im.getUserInfo(ctx.Context, token.AccessToken)
	if err != nil {
		log.Printf("Couldn't get username: %v", err)
		http.Error(w, "can't get username", 500)
		return
	}
	if err := ctx.AccountNode.SetAttrs(
		acctAttrUserId, u.Id,
		acctAttrUserFirst, u.FirstName,
		acctAttrUserLast, u.LastName,
		acctAttrAccessToken, token.AccessToken,
	); err != nil {
		httputil.ServeError(w, r, fmt.Errorf("Error setting attribute: %v", err))
		return
	}
	http.Redirect(w, r, ctx.AccountURL(), http.StatusFound)

}
