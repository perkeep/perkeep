package raindrop // import "perkeep.org/pkg/importer/raindrop"

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"perkeep.org/internal/httputil"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/client"
	"perkeep.org/pkg/importer"
	"perkeep.org/pkg/schema"
	"perkeep.org/pkg/schema/nodeattr"

	"go4.org/ctxutil"
)

func init() {
	cl, err := client.New()
	if err != nil {
		panic(err)
	}
	importer.Register("raindrop", imp{cl: cl})
}

const (
	fullUrl        = "https://api.raindrop.io/rest/v1/raindrops/0?page=%d&perpage=50"
	incrementalUrl = "https://api.raindrop.io/rest/v1/raindrops/0?page=%d&perpage=50&search=lastUpdate%%3A%%3E%s"
	userUrl        = "https://api.raindrop.io/rest/v1/user"

	timeFormat   = "2006-01-02T15:04:05.000Z"
	cursorFormat = "2006-01-02"

	attrAuthToken        = "authToken"
	attrLastUpdateCursor = "lastUpdateCursor"

	runCompleteVersion = "1"
)

type imp struct {
	cl *client.Client
}

func (im imp) CallbackRequestAccount(r *http.Request) (blob.Ref, error) {
	return importer.OAuth1{}.CallbackRequestAccount(r)
}

func (im imp) CallbackURLParameters(acctRef blob.Ref) url.Values {
	return importer.OAuth1{}.CallbackURLParameters(acctRef)
}

func (im imp) Properties() importer.Properties {
	return importer.Properties{
		Title:               "Raindrop",
		Description:         "import your raindrop.io bookmarks",
		SupportsIncremental: true,
		NeedsAPIKey:         false,
	}
}

func (im imp) IsAccountReady(acct *importer.Object) (ready bool, err error) {
	ready = acct.Attr(attrAuthToken) != ""
	return ready, nil
}

func (im imp) SummarizeAccount(acct *importer.Object) string {
	ok, err := im.IsAccountReady(acct)
	if err != nil {
		return "Not configured; error = " + err.Error()
	}
	if !ok {
		return "Not configured"
	}
	return fmt.Sprintf("Raindrop account for %s", acct.Attr(importer.AcctAttrUserName))
}

func (im imp) ServeSetup(w http.ResponseWriter, r *http.Request, ctx *importer.SetupContext) error {
	return tmpl.ExecuteTemplate(w, "serveSetup", ctx)
}

var tmpl = template.Must(template.New("root").Parse(`
{{define "serveSetup"}}
	<h1>Configuring Raindrop Account</h1>
	<form method="get" action="{{.CallbackURL}}">
		<input type="hidden" name="acct" value="{{.AccountNode.PermanodeRef}}">
		<table border=0 cellpadding=3>
		<tr><td align=right>API token</td><td><input name="apiToken" size=50> (You can find it <a href="https://app.raindrop.io/settings/integrations">here</a>)</td></tr>
		<tr><td align=right></td><td><input type="submit" value="Add"></td></tr>
		</table>
	</form>
{{end}}
`))

func (im imp) ServeCallback(w http.ResponseWriter, r *http.Request, ctx *importer.SetupContext) {
	t := r.FormValue("apiToken")
	if t == "" {
		http.Error(w, "**************Expected an API Token", http.StatusBadRequest)
		return
	}

	body, err := fetch(ctx, t, userUrl)
	if err != nil {
		httputil.ServeError(w, r, fmt.Errorf("Error fetching user info: %v", err))
		return
	}

	var res UserResponse
	if err := json.Unmarshal(body, &res); err != nil {
		httputil.ServeError(w, r, fmt.Errorf("Error unmarshalling user info: %v", err))
		return
	}

	ures, err := im.cl.Upload(ctx, client.NewUploadHandleFromString(string(body)))
	if err != nil {
		httputil.ServeError(w, r, fmt.Errorf("Error uploading user info: %v", err))
		return
	}
	if err := ctx.AccountNode.SetAttrs(
		attrAuthToken, t,
		importer.AcctAttrUserName, res.User.Name,
		nodeattr.CamliContent, ures.BlobRef.String(),
	); err != nil {
		httputil.ServeError(w, r, fmt.Errorf("Error setting attribute: %v", err))
		return
	}
	http.Redirect(w, r, ctx.AccountURL(), http.StatusFound)
}

func (im imp) Run(ctx *importer.RunContext) (err error) {
	lastUpdateCursor := ctx.AccountNode().Attr(attrLastUpdateCursor)

	log.Printf("raindrop: Running importer.")
	r := &run{
		RunContext:       ctx,
		im:               im,
		lastUpdateCursor: lastUpdateCursor,

		incremental: ctx.AccountNode().Attr(importer.AcctAttrCompletedVersion) == runCompleteVersion &&
			lastUpdateCursor != "",
	}
	err = r.importBookmarks()
	log.Printf("raindrop: Importer returned %v.", err)
	if err != nil {
		return err
	}

	return r.AccountNode().SetAttrs(
		importer.AcctAttrCompletedVersion, runCompleteVersion,
		attrLastUpdateCursor, time.Now().Add(-24*time.Hour).UTC().Format(cursorFormat),
	)
}

func (im imp) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	httputil.BadRequestError(w, "Unexpected path: %s", r.URL.Path)
}

type run struct {
	*importer.RunContext
	im               imp
	lastUpdateCursor string
	incremental      bool
}

func (r *run) getBookmarksNode() (*importer.Object, error) {
	username := r.AccountNode().Attr(importer.AcctAttrUserName)
	root := r.RootNode()
	rootTitle := fmt.Sprintf("%s's Raindrop Account", username)
	log.Printf("raindrop: root title = %q; want %q.", root.Attr(nodeattr.Title), rootTitle)
	if err := root.SetAttr(nodeattr.Title, rootTitle); err != nil {
		return nil, err
	}
	obj, err := root.ChildPathObject("bookmarks")
	if err != nil {
		return nil, err
	}
	title := fmt.Sprintf("%s's Bookmarks", username)
	return obj, obj.SetAttr(nodeattr.Title, title)
}

func (r *run) importBookmarks() error {
	authToken := r.AccountNode().Attr(attrAuthToken)
	parent, err := r.getBookmarksNode()
	if err != nil {
		return err
	}

	var page int

	for {
		if more, err := r.importBatch(page, authToken, parent); err != nil {
			return err
		} else if !more {
			break
		}
		page++
	}

	return nil
}

func fetch(ctx context.Context, authToken, url string, parts ...any) ([]byte, error) {
	u := fmt.Sprintf(url, parts...)
	cl := ctxutil.Client(ctx)

	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+authToken)
	resp, err := cl.Do(req)
	if err != nil {
		return nil, err
	}

	defer resp.Body.Close()
	switch {
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("Unexpected status code %v fetching %v", resp.StatusCode, u)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	return body, nil
}

// cloudflareFetch fetches a URL in a way that doesn't trigger cloudflare's captcha.
func cloudflareFetch(ctx context.Context, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("raindrop: error creating request for: %v %s", err, url)
	}

	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10.15; rv:138.0) Gecko/20100101 Firefox/138.0")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.5")
	// req.Header.Set("Accept-Encoding", "gzip, deflate, br, zstd")
	req.Header.Set("Sec-GPC", "1")
	req.Header.Set("Connection", "keep-alive")
	req.Header.Set("Upgrade-Insecure-Requests", "1")
	req.Header.Set("Sec-Fetch-Dest", "document")
	req.Header.Set("Sec-Fetch-Mode", "navigate")
	req.Header.Set("Sec-Fetch-Site", "cross-site")

	tr := &http.Transport{
		ForceAttemptHTTP2: false,
		TLSNextProto:      map[string]func(string, *tls.Conn) http.RoundTripper{},
	}

	client := &http.Client{Transport: tr}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("raindrop: error fetching: %v %s", err, url)
	}

	return resp, nil
}

func (r *run) importBatch(page int, authToken string, parent *importer.Object) (bool, error) {
	// Check if context is done before proceeding
	if ctx := r.Context(); ctx.Err() != nil {
		log.Printf("raindrop: Importer interrupted.")
		return false, ctx.Err()
	}
	start := time.Now()

	var body []byte
	var err error
	if r.incremental {
		body, err = fetch(r.Context(), authToken, incrementalUrl, page, r.lastUpdateCursor)
	} else {
		body, err = fetch(r.Context(), authToken, fullUrl, page)
	}

	if err != nil {
		return false, err
	}

	var bookmarkBatch CollectionResponse
	if err = json.Unmarshal(body, &bookmarkBatch); err != nil {
		return false, err
	}

	count := len(bookmarkBatch.Items)
	if count == 0 {
		// we are done!
		return false, nil
	}

	log.Printf("raindrop: Importing %d bookmarks...", count)
	for _, bookmark := range bookmarkBatch.Items {
		if err := r.importBookmark(&bookmark, parent); err != nil {
			return false, err
		}
	}

	log.Printf("raindrop: Imported batch of %d bookmarks in %s.", count, time.Since(start))

	return true, nil
}

func (r *run) importBookmark(bookmark *Bookmark, parent *importer.Object) error {
	log.Printf("raindrop: Importing bookmark %s...", bookmark.Link)
	bookmarkNode, err := parent.ChildPathObject(fmt.Sprint(bookmark.ID))
	if err != nil {
		return err
	}

	// Check for duplicates
	if bookmarkNode.Attr(nodeattr.DateModified) == schema.RFC3339FromTime(bookmark.LastUpdate) {
		return nil
	}

	json, err := json.MarshalIndent(bookmark, "", "  ")
	if err != nil {
		return err
	}

	ures, err := r.im.cl.Upload(r.Context(), client.NewUploadHandleFromString(string(json)))
	if err != nil {
		return err
	}

	attrs := []string{
		nodeattr.Type, "raindrop.io:bookmark",
		nodeattr.DateCreated, schema.RFC3339FromTime(bookmark.Created),
		nodeattr.DateModified, schema.RFC3339FromTime(bookmark.LastUpdate),
		nodeattr.Title, bookmark.Title,
		nodeattr.URL, bookmark.Link,
		nodeattr.CamliContent, ures.BlobRef.String(),
	}

	coverURL := bookmark.Cover
	if coverURL != "" {
		resp, err := cloudflareFetch(r.Context(), coverURL)
		if resp != nil {
			defer resp.Body.Close()
		}

		if (err != nil || resp.StatusCode != http.StatusOK) && !strings.HasPrefix(coverURL, "https://rdl.ink/") {
			if resp != nil {
				_ = resp.Body.Close()
			}
			coverURL = "https://rdl.ink/render/" + url.QueryEscape(coverURL)
			rresp, err := cloudflareFetch(r.Context(), coverURL)
			if err != nil {
				return err
			}

			resp = rresp
		} else if err != nil {
			return err
		}

		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("raindrop: unexpected status code %v fetching cover image %s for %s", resp.StatusCode, coverURL, bookmark.Link)
		}

		coverRef, err := r.im.cl.UploadFile(r.Context(), coverURL, resp.Body, nil)
		if err != nil {
			return err
		}
		attrs = append(attrs, nodeattr.CamliContentImage, coverRef.String())
	}

	if err = bookmarkNode.SetAttrs(attrs...); err != nil {
		return err
	}
	if err = bookmarkNode.SetAttrValues("tag", bookmark.Tags); err != nil {
		return err
	}

	return nil
}
