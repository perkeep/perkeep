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

/*
Package pinboard imports pinboard.in posts.

This package uses the v1 api documented here:  https://pinboard.in/api.

Note that the api document seems to use 'post' and 'bookmark'
interchangeably.  We use 'post' everywhere in this code.

Posts in pinboard are mutable; they can be edited or deleted.

We handle edited posts by always reimporting everything and rewriting
any nodes.  Perhaps this would become more efficient if we would first
compare the meta tag from pinboard to the meta tag we have stored to
only write the node if there are changes.

We don't handle deleted posts.  One possible approach for this would
be to import everything under a new permanode, then once it is
successful, swap the new permanode and the posts node (note: I don't
think I really understand the data model here, so this is sort of
gibberish).

I have exchanged email with Maciej Ceglowski of pinboard, who may in
the future provide an api that lets us query what has changed.  We
might want to switch to that when available to make the import process
more light-weight.
*/
package pinboard

import (
	"encoding/json"
	"fmt"
	"html/template"
	"io/ioutil"
	"log"
	"net/http"
	"strings"
	"time"

	"camlistore.org/pkg/httputil"
	"camlistore.org/pkg/importer"
	"camlistore.org/pkg/schema"
	"camlistore.org/pkg/schema/nodeattr"

	"go4.org/ctxutil"
	"go4.org/syncutil"
)

func init() {
	importer.Register("pinboard", imp{})
}

const (
	fetchUrl = "https://api.pinboard.in/v1/posts/all?auth_token=%s&format=json&results=%d&todt=%s"

	timeFormat = "2006-01-02T15:04:05Z"

	// pauseInterval is the time we wait between fetching batches (for
	// a particualar user).  This time is pretty long, but is what the
	// api documentation suggests.
	pauseInterval = 5 * time.Minute

	// batchLimit is the maximum number of posts we will fetch in one batch.
	batchLimit = 10000

	attrAuthToken = "authToken"

	// StatusTooManyRequests is the http status code returned by
	// pinboard servers if we have made too many requests for a
	// particular user.  If we receive this status code, we should
	// double the amount of time we wait before trying agian.
	StatusTooManyRequests = 429
)

// We expect <username>:<some id>.  Sometimes pinboard calls this an
// auth token and sometimes they call it an api token.
func extractUsername(authToken string) string {
	split := strings.SplitN(authToken, ":", 2)
	if len(split) == 2 {
		return split[0]
	} else {
		return ""
	}
}

type imp struct {
	importer.OAuth1 // for CallbackRequestAccount and CallbackURLParameters
}

func (imp) SupportsIncremental() bool { return false }

func (imp) NeedsAPIKey() bool { return false }

func (imp) IsAccountReady(acct *importer.Object) (ready bool, err error) {
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
	return fmt.Sprintf("Pinboard account for %s", extractUsername(acct.Attr(attrAuthToken)))
}

func (imp) ServeSetup(w http.ResponseWriter, r *http.Request, ctx *importer.SetupContext) error {
	return tmpl.ExecuteTemplate(w, "serveSetup", ctx)
}

var tmpl = template.Must(template.New("root").Parse(`
{{define "serveSetup"}}
<h1>Configuring Pinboad Account</h1>
<form method="get" action="{{.CallbackURL}}">
  <input type="hidden" name="acct" value="{{.AccountNode.PermanodeRef}}">
  <table border=0 cellpadding=3>
  <tr><td align=right>API token</td><td><input name="apiToken" size=50> (You can find it <a href="https://pinboard.in/settings/password">here</a>)</td></tr>
  <tr><td align=right></td><td><input type="submit" value="Add"></td></tr>
  </table>
</form>
{{end}}
`))

func (im imp) ServeCallback(w http.ResponseWriter, r *http.Request, ctx *importer.SetupContext) {
	t := r.FormValue("apiToken")
	if t == "" {
		http.Error(w, "Expected an API Token", 400)
		return
	}
	if extractUsername(t) == "" {
		errText := fmt.Sprintf("Unable to parse %q as an api token.  We expect <username>:<somevalue>", t)
		http.Error(w, errText, 400)
	}
	if err := ctx.AccountNode.SetAttrs(
		attrAuthToken, t,
	); err != nil {
		httputil.ServeError(w, r, fmt.Errorf("Error setting attribute: %v", err))
		return
	}
	http.Redirect(w, r, ctx.AccountURL(), http.StatusFound)
}

func (im imp) Run(ctx *importer.RunContext) (err error) {
	log.Printf("pinboard: Running importer.")
	r := &run{
		RunContext: ctx,
		im:         im,
		postGate:   syncutil.NewGate(3),
		nextCursor: time.Now().Format(timeFormat),
		nextAfter:  time.Now(),
		lastPause:  pauseInterval,
	}
	_, err = r.importPosts()
	log.Printf("pinboard: Importer returned %v.", err)
	return
}

func (im imp) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	httputil.BadRequestError(w, "Unexpected path: %s", r.URL.Path)
}

type run struct {
	*importer.RunContext
	im       imp
	postGate *syncutil.Gate

	// Return only bookmarks created before this time (exclusive bound)
	nextCursor string

	// We should not fetch the next batch until this time (exclusive bound)
	nextAfter time.Time

	// This gets set to pauseInterval at the beginning of each run and
	// after each successful fetch.  Every time we get a 429 back from
	// pinboard, it gets doubled.  It will be used to calculate the
	// next time we fetch from pinboard.
	lastPause time.Duration
}

func (r *run) getPostsNode() (*importer.Object, error) {
	username := extractUsername(r.AccountNode().Attr(attrAuthToken))
	root := r.RootNode()
	rootTitle := fmt.Sprintf("%s's Pinboard Account", username)
	log.Printf("pinboard: root title = %q; want %q.", root.Attr(nodeattr.Title), rootTitle)
	if err := root.SetAttr(nodeattr.Title, rootTitle); err != nil {
		return nil, err
	}
	obj, err := root.ChildPathObject("posts")
	if err != nil {
		return nil, err
	}
	title := fmt.Sprintf("%s's Posts", username)
	return obj, obj.SetAttr(nodeattr.Title, title)
}

func (r *run) importPosts() (*importer.Object, error) {
	authToken := r.AccountNode().Attr(attrAuthToken)
	parent, err := r.getPostsNode()
	if err != nil {
		return nil, err
	}

	keepTrying := true
	for keepTrying {
		keepTrying, err = r.importBatch(authToken, parent)
		if err != nil {
			return nil, err
		}
	}

	return parent, nil
}

// Used to parse json
type apiPost struct {
	Href        string
	Description string
	Extended    string
	Meta        string
	Hash        string
	Time        string
	Shared      string
	ToRead      string
	Tags        string
}

func (r *run) importBatch(authToken string, parent *importer.Object) (keepTrying bool, err error) {
	sleepDuration := r.nextAfter.Sub(time.Now())
	// block until we either get canceled or until it is time to run
	select {
	case <-r.Done():
		log.Printf("pinboard: Importer interrupted.")
		return false, r.Err()
	case <-time.After(sleepDuration):
		// just proceed
	}
	start := time.Now()

	u := fmt.Sprintf(fetchUrl, authToken, batchLimit, r.nextCursor)
	resp, err := ctxutil.Client(r).Get(u)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == StatusTooManyRequests:
		r.lastPause = r.lastPause * 2
		r.nextAfter = time.Now().Add(r.lastPause)
		return true, nil
	case resp.StatusCode != http.StatusOK:
		return false, fmt.Errorf("Unexpected status code %v fetching %v", resp.StatusCode, u)
	}

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return false, err
	}

	var postBatch []apiPost
	if err = json.Unmarshal(body, &postBatch); err != nil {
		return false, err
	}

	if err != nil {
		return false, err
	}

	postCount := len(postBatch)
	if postCount == 0 {
		// we are done!
		return false, nil
	}

	log.Printf("pinboard: Importing %d posts...", postCount)
	var grp syncutil.Group
	for _, post := range postBatch {
		select {
		case <-r.Done():
			log.Printf("pinboard: Importer interrupted")
			return false, r.Err()
		default:
		}

		post := post
		r.postGate.Start()
		grp.Go(func() error {
			defer r.postGate.Done()
			return r.importPost(&post, parent)
		})
	}

	log.Printf("pinboard: Imported batch of %d posts in %s.", postCount, time.Now().Sub(start))

	r.nextCursor = postBatch[postCount-1].Time
	r.lastPause = pauseInterval
	r.nextAfter = time.Now().Add(pauseInterval)
	tryAgain := postCount == batchLimit
	return tryAgain, grp.Err()
}

func (r *run) importPost(post *apiPost, parent *importer.Object) error {
	postNode, err := parent.ChildPathObject(post.Hash)
	if err != nil {
		return err
	}

	t, err := time.Parse(timeFormat, post.Time)
	if err != nil {
		return err
	}

	attrs := []string{
		"pinboard.in:hash", post.Hash,
		nodeattr.Type, "pinboard.in:post",
		nodeattr.DateCreated, schema.RFC3339FromTime(t),
		nodeattr.Title, post.Description,
		nodeattr.URL, post.Href,
		"pinboard.in:extended", post.Extended,
		"pinboard.in:meta", post.Meta,
		"pinboard.in:shared", post.Shared,
		"pinboard.in:toread", post.ToRead,
	}
	if err = postNode.SetAttrs(attrs...); err != nil {
		return err
	}
	if err = postNode.SetAttrValues("tag", strings.Split(post.Tags, " ")); err != nil {
		return err
	}

	return nil
}
