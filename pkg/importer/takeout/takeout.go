/*
Copyright 2020 The Perkeep Authors

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

// Package takeout implements a Google takeout importer.
package takeout // import "perkeep.org/pkg/importer/takeout"

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync"
	"text/template"
	"time"

	"perkeep.org/internal/httputil"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/importer"
	"perkeep.org/pkg/schema"
	"perkeep.org/pkg/schema/nodeattr"

	"github.com/garyburd/go-oauth/oauth"

	"go4.org/syncutil"
)

const (
	// runCompleteVersion is a cache-busting version number of the
	// importer code. It should be incremented whenever the
	// behavior of this importer is updated enough to warrant a
	// complete run.  Otherwise, if the importer runs to
	// completion, this version number is recorded on the account
	// permanode and subsequent importers can stop early.
	runCompleteVersion = "1"

	// Download a takeout zip file, create an account and add the file ref:
	//   $ pk-put attr <acct-permanode> takeoutArchiveZipFileRef <zip-fileref>
	// ... and re-do an import.
	acctAttrTakeoutZip = "takeoutArchiveZipFileRef"

	// acctAttrZipDoneVersion is updated at the end of a successful zip import and
	// is used to determine whether the zip file needs to be re-imported in a future run.
	acctAttrZipDoneVersion = "takeoutZipDoneVersion" // == "<fileref>:<runCompleteVersion>"

	// A item is stored as a permanode with the "google.com:takeout" camliNodeType value.
	nodeTypeTakeoutItem = "google.com:takeout"

	itemsAtOnce = 20

	attrImportMethod = "takeoutImportMethod"
)

func init() {
	importer.Register("takeout", &imp{})
}

var _ importer.ImporterSetupHTMLer = (*imp)(nil)

type imp struct {
}

func (*imp) Properties() importer.Properties {
	return importer.Properties{
		Title:               "Google Takeout",
		Description:         "import takeout items",
		SupportsIncremental: false,
		NeedsAPIKey:         false,
	}
}

func (im *imp) IsAccountReady(acctNode *importer.Object) (ok bool, err error) {
	if acctNode.Attr(acctAttrTakeoutZip) != "" {
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
	s := fmt.Sprintf("takeout id %s",
		acct.Attr(importer.AcctAttrName),
	)
	if acct.Attr(acctAttrTakeoutZip) != "" {
		s += " + zip file"
	}
	return s
}

func (im *imp) AccountSetupHTML(host *importer.Host) string {
	return fmt.Sprint(`
<h1>Configuring Takeout</h1>
<p>Visit <a href='https://takeout.google.com/'>https://takeout.google.com/</a> and export all Google Producs you are interested in</p>
<p>Add the zip file to Perkeep first then create an account and add the file ref</p>
`)
}

// A run is our state for a given run of the importer.
type run struct {
	*importer.RunContext
	im          *imp
	incremental bool // whether we've completed a run in the past

	oauthClient *oauth.Client      // No need to guard, used read-only.
	accessCreds *oauth.Credentials // No need to guard, used read-only.

	mu     sync.Mutex // guards anyErr
	anyErr bool
}

func (im *imp) Run(ctx *importer.RunContext) error {
	acctNode := ctx.AccountNode()
	r := &run{
		RunContext:  ctx,
		im:          im,
		incremental: false,
	}

	acctNode, err := ctx.Host.ObjectFromRef(acctNode.PermanodeRef())
	if err != nil {
		return fmt.Errorf("error reloading account node: %v", err)
	}

	zipRef := acctNode.Attr(acctAttrTakeoutZip)
	if zipRef == "" {
		return errors.New("takeoutArchiveZipFileRef hasn't been set by account setup")
	}

	zipDoneVal := zipRef + ":" + runCompleteVersion
	if zipRef != "" && !(r.incremental && acctNode.Attr(acctAttrZipDoneVersion) == zipDoneVal) {
		zipbr, ok := blob.Parse(zipRef)
		if !ok {
			return fmt.Errorf("invalid zip file blobref %q", zipRef)
		}
		fr, err := schema.NewFileReader(r.Context(), r.Host.BlobSource(), zipbr)
		if err != nil {
			return fmt.Errorf("error opening zip %v: %v", zipbr, err)
		}
		defer fr.Close()
		zr, err := zip.NewReader(fr, fr.Size())
		if err != nil {
			return fmt.Errorf("Error opening takeout zip file %v: %v", zipRef, err)
		}
		if err := r.importItemsFromZip(zr, acctNode.Attr(importer.AcctAttrName)); err != nil {
			return err
		}
		if err := acctNode.SetAttrs(acctAttrZipDoneVersion, zipDoneVal); err != nil {
			return err
		}
	}

	r.mu.Lock()
	anyErr := r.anyErr
	r.mu.Unlock()

	if !anyErr {
		if err := acctNode.SetAttrs(importer.AcctAttrCompletedVersion, runCompleteVersion); err != nil {
			return err
		}
	}

	return nil
}

func (r *run) errorf(format string, args ...interface{}) {
	log.Printf("takeout: "+format, args...)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.anyErr = true
}

func noteItemFromZipFile(zf *zip.File) (item *noteItem, err error) {
	rc, err := zf.Open()
	if err != nil {
		return nil, err
	}
	slurp, err := ioutil.ReadAll(rc)
	rc.Close()
	if err != nil {
		return nil, err
	}
	i := bytes.IndexByte(slurp, '{')
	if i < 0 {
		return nil, errors.New("No '{' found in zip file")
	}
	slurp = slurp[i:]
	item = &noteItem{}
	if err := json.Unmarshal(slurp, item); err != nil {
		return nil, fmt.Errorf("JSON error: %v", err.Error())
	}

	if item.NoteTitle == "" {
		item.NoteTitle = strings.Replace(path.Base(zf.Name), ".json", "", -1)
	}
	return
}

func (r *run) importItemsFromZip(zr *zip.Reader, account string) error {
	log.Printf("takeout: processing zip file with %d files", len(zr.File))

	var (
		//gate = syncutil.NewGate(itemsAtOnce)
		grp syncutil.Group
	)
	total := 0
	for _, zf := range zr.File {
		if !(strings.HasSuffix(zf.Name, ".json")) {
			continue
		}
		total++

		// service is the folder name
		service := path.Base(path.Dir(zf.Name))

		parent, err := r.getTopLevelNode(service)

		var item item
		// "Notes" in English, Notizen in German. TODO find better ways to determine service properly
		if strings.Contains(service, "Not") {
			item, err = noteItemFromZipFile(zf)
		}

		if err != nil {
			return fmt.Errorf("error reading items from %s: %v", zf.Name, err)
		}

		grp.Go(func() error {
			_, err := r.importItem(parent, item)
			return err
		})
	}
	err := grp.Err()
	log.Printf("zip import of items: %d total, err = %v", total, err)
	return err
}

func (r *run) importItem(parent *importer.Object, item item) (dup bool, err error) {
	select {
	case <-r.Context().Done():
		r.errorf("takeout importer: interrupted")
		return false, r.Context().Err()
	default:
	}
	id := item.Title()
	// duplicate here!!!
	if err != nil {
		return false, err
	}
	itemNode, err := parent.ChildPathObject(id)
	if err != nil {
		return false, err
	}

	createdTime := time.Unix(item.Timestamp(), 0)
	url := "https://takeout.google.com"

	nodeType := nodeTypeTakeoutItem
	attrs := []string{
		nodeattr.Title, id,
		nodeattr.Type, nodeType,
		nodeattr.StartDate, schema.RFC3339FromTime(createdTime),
		nodeattr.Content, item.Content(),
		nodeattr.URL, url,
		"service", item.Service(),
	}
	attrs = append(attrs, attrImportMethod, "zip")

	//TODO annotations and files
	/* for i, m := range item.Media() {
		filename := m.BaseFilename()
		if itemNode.Attr("camliPath:"+filename) != "" && (i > 0 || itemNode.Attr("camliContentImage") != "") {
			// Don't re-import media we've already fetched.
			continue
		}
		tried, gotMedia := 0, false
		for _, mediaURL := range m.URLs() {
			tried++
			res, err := ctxutil.Client(r.Context()).Get(mediaURL)
			if err != nil {
				return false, fmt.Errorf("Error fetching %s for item %s : %v", mediaURL, url, err)
			}
			if res.StatusCode == http.StatusNotFound {
				continue
			}
			if res.StatusCode != 200 {
				return false, fmt.Errorf("HTTP status %d fetching %s for item %s", res.StatusCode, mediaURL, url)
			}
			if !viaAPI {
				log.Printf("takeout: for zip item %s, reading %v", url, mediaURL)
			}
			fileRef, err := schema.WriteFileFromReader(r.Context(), r.Host.Target(), filename, res.Body)
			res.Body.Close()
			if err != nil {
				return false, fmt.Errorf("Error fetching media %s for item %s: %v", mediaURL, url, err)
			}
			attrs = append(attrs, "camliPath:"+filename, fileRef.String())
			if i == 0 {
				attrs = append(attrs, "camliContentImage", fileRef.String())
			}
			log.Printf("takeout: slurped %s as %s for item %s (%v)", mediaURL, fileRef.String(), url, itemNode.PermanodeRef())
			gotMedia = true
			break
		}
		if !gotMedia && tried > 0 {
			return false, fmt.Errorf("All media URLs 404s for item %s", url)
		}
	}
	*/

	changes, err := itemNode.SetAttrs2(attrs...)
	if err == nil && changes {
		log.Printf("takeout: imported item %s", id)
	}
	return !changes, err
}

func (r *run) getTopLevelNode(service string) (*importer.Object, error) {
	acctNode := r.AccountNode()

	root := r.RootNode()
	rootTitle := fmt.Sprintf("Takeout")
	if err := root.SetAttr(nodeattr.Title, rootTitle); err != nil {
		return nil, err
	}

	obj, err := root.ChildPathObject(service)
	if err != nil {
		return nil, err
	}
	var title string
	title = fmt.Sprintf("Takeout: %s (%s)", service, acctNode.Attr(importer.AcctAttrName))
	return obj, obj.SetAttr(nodeattr.Title, title)
}

func (im *imp) ServeSetup(w http.ResponseWriter, r *http.Request, ctx *importer.SetupContext) error {
	return tmpl.ExecuteTemplate(w, "serveSetup", ctx)
}

var tmpl = template.Must(template.New("root").Parse(`
{{define "serveSetup"}}
<h1>Configuring Takeout</h1>
<form method="get" action="{{.CallbackURL}}">
  <input type="hidden" name="acct" value="{{.AccountNode.PermanodeRef}}">
  <table border=0 cellpadding=3>
  <tr><td align=right>Takeout Name</td><td><input name="name" size=100></td></tr>
  <tr><td align=right>Takeout ZIP file ref</td><td><input name="fileRef" size=50></td></tr>
  <tr><td align=right></td><td><input type="submit" value="Add"></td></tr>
  </table>
</form>
{{end}}
`))

func (im *imp) ServeCallback(w http.ResponseWriter, r *http.Request, ctx *importer.SetupContext) {
	n := r.FormValue("name")
	if n == "" {
		http.Error(w, "Expected a name", 400)
		return
	}
	f := r.FormValue("fileRef")
	if f == "" {
		http.Error(w, "Expected a file ref", 400)
		return
	}
	if err := ctx.AccountNode.SetAttrs(
		importer.AcctAttrName, n,
		acctAttrTakeoutZip, f,
	); err != nil {
		httputil.ServeError(w, r, fmt.Errorf("Error setting attribute: %v", err))
		return
	}
	http.Redirect(w, r, ctx.AccountURL(), http.StatusFound)
}

func (im *imp) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	httputil.BadRequestError(w, "Unexpected path: %s", r.URL.Path)
}

func (im *imp) CallbackRequestAccount(r *http.Request) (blob.Ref, error) {
	// We do not actually use OAuth, but this method works for us anyway.
	// Even if your importer implementation does not use OAuth, you can
	// probably just embed importer.OAuth1 in your implementation type.
	// If OAuth2, embedding importer.OAuth2 should work.
	return importer.OAuth1{}.CallbackRequestAccount(r)
}

func (im *imp) CallbackURLParameters(acctRef blob.Ref) url.Values {
	// See comment in CallbackRequestAccount.
	return importer.OAuth1{}.CallbackURLParameters(acctRef)
}

type userInfo struct {
	ID         string `json:"id_str"`
	ScreenName string `json:"screen_name"`
	Name       string `json:"name,omitempty"`
}

func getUserInfo(ctx importer.OAuthContext) (userInfo, error) {
	var ui userInfo
	if ui.ID == "" {
		return ui, fmt.Errorf("No userid returned")
	}
	return ui, nil
}
