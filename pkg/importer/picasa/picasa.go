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
package picasa

import (
	"fmt"
	"log"
	"net/http"
	"path/filepath"
	"strings"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/context"
	"camlistore.org/pkg/importer"
	"camlistore.org/pkg/schema"

	"camlistore.org/third_party/code.google.com/p/goauth2/oauth"
	"camlistore.org/third_party/github.com/tgulacsi/picago"
)

const (
	apiURL   = "https://api.picasa.com/v2/"
	authURL  = "https://accounts.google.com/o/oauth2/auth"
	tokenURL = "https://accounts.google.com/o/oauth2/token"
	scopeURL = "https://picasaweb.google.com/data/"
)

func init() {
	importer.Register("picasa", newImporter())
}

var _ importer.ImporterSetupHTMLer = (*imp)(nil)

type imp struct {
	importer.ExtendedOAuth2
}

var baseOAuthConfig = oauth.Config{
	AuthURL:  authURL,
	TokenURL: tokenURL,
	Scope:    scopeURL,

	// AccessType needs to be "offline", as the user is not here all the time;
	// ApprovalPrompt needs to be "force" to be able to get a RefreshToken
	// everytime, even for Re-logins, too.
	//
	// Source: https://developers.google.com/youtube/v3/guides/authentication#server-side-apps
	AccessType:     "offline",
	ApprovalPrompt: "force",
}

func newImporter() *imp {
	return &imp{
		importer.NewExtendedOAuth2(
			baseOAuthConfig,
			func(ctx *context.Context, accessToken string) (*importer.UserInfo, error) {
				u, err := getUserInfo(ctx, accessToken)
				if err != nil {
					return nil, err
				}
				firstName, lastName := u.Name, ""
				i := strings.LastIndex(u.Name, " ")
				if i >= 0 {
					firstName, lastName = u.Name[:i], u.Name[i+1:]
				}
				return &importer.UserInfo{
					ID:        u.ID,
					FirstName: firstName,
					LastName:  lastName,
				}, nil
			}),
	}
}

func (im imp) AccountSetupHTML(host *importer.Host) string {
	base := host.ImporterBaseURL() + "picasa"
	return fmt.Sprintf(`
<h1>Configuring Picasa</h1>
<p>Visit <a href='https://console.developers.google.com/'>https://console.developers.google.com/</a>
and click "CREATE PROJECT".</p>
<p>Then under "APIs & auth" click on "Credentials", then "CREATE NEW CLIENT ID".</p>
<p>Use the following settings:</p>
<ul>
  <li>Web application</li>
  <li>Authorized JavaScript origins: <b>%s</b></li>
  <li>Authorized Redirect URI: <b>%s</b></li>
</ul>
<p>Click "Create Client ID".  Copy the "Client ID" and "Client Secret" into the boxes above.</p>
`, base, base+"/callback")
}

// A run is our state for a given run of the importer.
type run struct {
	*importer.RunContext
	im *imp
}

func (im *imp) Run(ctx *importer.RunContext) error {
	clientId, secret, err := ctx.Credentials()
	if err != nil {
		return err
	}
	ocfg := baseOAuthConfig
	ocfg.ClientId, ocfg.ClientSecret = clientId, secret
	token := importer.DecodeToken(ctx.AccountNode().Attr(importer.AcctAttrOAuthToken))
	transport := &oauth.Transport{
		Config:    &ocfg,
		Token:     &token,
		Transport: importer.NotOAuthTransport(ctx.HTTPClient()),
	}
	ctx.Context = ctx.Context.New(context.WithHTTPClient(transport.Client()))
	r := &run{RunContext: ctx, im: im}
	if err := r.importAlbums(); err != nil {
		return err
	}
	return nil
}

func (r *run) importAlbums() error {
	albums, err := picago.GetAlbums(r.HTTPClient(), "default")
	if err != nil {
		return fmt.Errorf("importAlbums: error listing albums: %v", err)
	}
	albumsNode, err := r.getTopLevelNode("albums", "Albums")
	for _, album := range albums {
		if r.Context.IsCanceled() {
			return context.ErrCanceled
		}
		if err := r.importAlbum(albumsNode, album, r.HTTPClient()); err != nil {
			return fmt.Errorf("picasa importer: error importing album %s: %v", album, err)
		}
	}
	return nil
}

func (r *run) importAlbum(albumsNode *importer.Object, album picago.Album, client *http.Client) error {
	albumNode, err := albumsNode.ChildPathObject(album.Name)
	if err != nil {
		return fmt.Errorf("importAlbum: error listing album: %v", err)
	}

	// Data reference: https://developers.google.com/picasa-web/docs/2.0/reference
	// TODO(tgulacsi): add more album info
	if err = albumNode.SetAttrs(
		"picasaId", album.ID,
		"camliNodeType", "picasaweb.google.com:album",
		importer.AttrTitle, album.Title,
		importer.AttrLocationText, album.Location,
	); err != nil {
		return fmt.Errorf("error setting album attributes: %v", err)
	}

	photos, err := picago.GetPhotos(client, "default", album.ID)
	if err != nil {
		return err
	}

	log.Printf("Importing %d photos from album %q (%s)", len(photos), albumNode.Attr("title"),
		albumNode.PermanodeRef())

	for _, photo := range photos {
		if r.Context.IsCanceled() {
			return context.ErrCanceled
		}
		// TODO(tgulacsi): check when does the photo.ID changes

		attr := "camliPath:" + photo.ID + "-" + photo.Filename()
		if refString := albumNode.Attr(attr); refString != "" {
			// Check the photoNode's modtime - skip only if it hasn't changed.
			if photoRef, ok := blob.Parse(refString); !ok {
				log.Printf("error parsing attr %s (%s) as ref: %v", attr, refString, err)
			} else {
				photoNode, err := r.Host.ObjectFromRef(photoRef)
				if err != nil {
					log.Printf("error getting object %s: %v", refString, err)
				} else {
					modtime := photoNode.Attr("dateModified")
					switch modtime {
					case "": // no modtime to check against - import again
						log.Printf("No dateModified on %s, re-import.", refString)
					case schema.RFC3339FromTime(photo.Updated):
						// Assume we have this photo already and don't need to refetch.
						log.Printf("Skipping photo with %s, we already have it.", attr)
						continue
					default: // modtimes differ - import again
						switch filepath.Ext(photo.Filename()) {
						case ".mp4", ".m4v":
							log.Printf("photo %s is a video, cannot rely on its modtime, so not importing again.",
								attr[10:])
							continue
						default:
							log.Printf("photo %s imported(%s) != remote(%s), so importing again",
								attr[10:], modtime, schema.RFC3339FromTime(photo.Updated))
						}
					}
				}
			}
		}

		log.Printf("importing %s", attr[10:])
		photoNode, err := r.importPhoto(albumNode, photo, client)
		if err != nil {
			log.Printf("error importing photo %s: %v", photo.URL, err)
			continue
		}
		err = albumNode.SetAttr(attr, photoNode.PermanodeRef().String())
		if err != nil {
			log.Printf("Error adding photo to album: %v", err)
			continue
		}
	}

	return nil
}

func (r *run) importPhoto(albumNode *importer.Object, photo picago.Photo, client *http.Client) (*importer.Object, error) {
	body, err := picago.DownloadPhoto(client, photo.URL)
	if err != nil {
		return nil, fmt.Errorf("importPhoto: DownloadPhoto error: %v", err)
	}
	fileRef, err := schema.WriteFileFromReader(
		r.Host.Target(),
		fmt.Sprintf("%s--%s-%s",
			strings.Replace(albumNode.Attr("name"), "/", "--", -1),
			photo.ID,
			photo.Filename()),
		body)
	if err != nil {
		return nil, fmt.Errorf("error writing file: %v", err)
	}
	if !fileRef.Valid() {
		return nil, fmt.Errorf("Error slurping photo: %s", photo.URL)
	}
	photoNode, err := r.Host.NewObject()
	if err != nil {
		return nil, fmt.Errorf("error creating photo permanode %s under %s: %v",
			photo.Filename(), albumNode.Attr("name"), err)
	}

	// TODO(tgulacsi): add more attrs (comments ?)
	// for names, see http://schema.org/ImageObject and http://schema.org/CreativeWork
	if err := photoNode.SetAttrs(
		"camliContent", fileRef.String(),
		"picasaId", photo.ID,
		importer.AttrTitle, photo.Title,
		"caption", photo.Summary,
		importer.AttrDescription, photo.Description,
		importer.AttrLocationText, photo.Location,
		"latitude", fmt.Sprintf("%f", photo.Latitude),
		"longitude", fmt.Sprintf("%f", photo.Longitude),
		"dateModified", schema.RFC3339FromTime(photo.Updated),
		"datePublished", schema.RFC3339FromTime(photo.Published),
	); err != nil {
		return nil, fmt.Errorf("error adding file to photo node: %v", err)
	}
	if err := photoNode.SetAttrValues("tag", photo.Keywords); err != nil {
		return nil, fmt.Errorf("error setting photoNode's tags: %v", err)
	}

	return photoNode, nil
}

func (r *run) getTopLevelNode(path string, title string) (*importer.Object, error) {
	childObject, err := r.RootNode().ChildPathObject(path)
	if err != nil {
		return nil, err
	}

	if err := childObject.SetAttr("title", title); err != nil {
		return nil, err
	}
	return childObject, nil
}

func getUserInfo(ctx *context.Context, accessToken string) (picago.User, error) {
	return picago.GetUser(ctx.HTTPClient(), "")
}
