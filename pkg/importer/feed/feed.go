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

// Package feed implements an importer for RSS, Atom, and RDF feeds.
package feed

import (
	"bytes"
	"fmt"
	"html/template"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/httputil"
	"camlistore.org/pkg/importer"
	"camlistore.org/pkg/schema"

	"camlistore.org/third_party/code.google.com/p/go.net/html"
	"camlistore.org/third_party/code.google.com/p/go.net/html/atom"

	"go4.org/ctxutil"
	"golang.org/x/net/context"
)

const (
	// Permanode attributes on account node:
	acctAttrFeedURL = "feedURL"
)

func init() {
	importer.Register("feed", &imp{
		urlFileRef: make(map[string]blob.Ref),
	})
}

type imp struct {
	mu         sync.Mutex          // guards following
	urlFileRef map[string]blob.Ref // url to file schema blob

	importer.OAuth1 // for CallbackRequestAccount and CallbackURLParameters
}

func (im *imp) NeedsAPIKey() bool { return false }

func (im *imp) SupportsIncremental() bool { return true }

func (im *imp) IsAccountReady(acctNode *importer.Object) (ok bool, err error) {
	if acctNode.Attr(acctAttrFeedURL) != "" {
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
	return fmt.Sprintf("feed %s", acct.Attr(acctAttrFeedURL))
}

// A run is our state for a given run of the importer.
type run struct {
	*importer.RunContext
	im *imp
}

func (im *imp) Run(ctx *importer.RunContext) error {
	r := &run{
		RunContext: ctx,
		im:         im,
	}

	if err := r.importFeed(); err != nil {
		return err
	}
	return nil
}

func (r *run) importFeed() error {
	accountNode := r.RunContext.AccountNode()
	feedURL, err := url.Parse(accountNode.Attr(acctAttrFeedURL))
	if err != nil {
		return err
	}
	body, err := doGet(r.Context, feedURL.String())
	if err != nil {
		return err
	}
	if auto, err := autoDiscover(body); err == nil {
		if autoURL, err := url.Parse(auto); err == nil {
			if autoURL.Scheme == "" {
				autoURL.Scheme = feedURL.Scheme
			}
			if autoURL.Host == "" {
				autoURL.Host = feedURL.Host
			}
			body, err = doGet(r.Context, autoURL.String())
			if err != nil {
				return err
			}
		}
	}
	feed, err := parseFeed(body, feedURL.String())
	if err != nil {
		return err
	}
	itemsNode := r.RootNode()
	if accountNode.Attr("title") == "" {
		accountNode.SetAttr("title", fmt.Sprintf("%s Feed", feed.Title))
	}
	if itemsNode.Attr("title") == "" {
		itemsNode.SetAttr("title", fmt.Sprintf("%s Items", feed.Title))
	}
	for _, item := range feed.Items {
		if err := r.importItem(itemsNode, item); err != nil {
			log.Printf("Feed importer: error importing item %s %v", item.ID, err)
			continue
		}
	}
	return nil
}

func (r *run) importItem(parent *importer.Object, item *item) error {
	itemNode, err := parent.ChildPathObject(item.ID)
	if err != nil {
		return err
	}
	fileRef, err := schema.WriteFileFromReader(r.Host.Target(), "", bytes.NewBufferString(item.Content))
	if err != nil {
		return err
	}
	if err := itemNode.SetAttrs(
		"feedItemId", item.ID,
		"camliNodeType", "feed:item",
		"title", item.Title,
		"link", item.Link,
		"author", item.Author,
		"camliContent", fileRef.String(),
		"feedMediaContentURL", item.MediaContent,
	); err != nil {
		return err
	}
	return nil
}

// autodiscover takes an HTML document and returns the autodiscovered feed
// URL. Returns an error if there is no such URL.
func autoDiscover(body []byte) (feedURL string, err error) {
	r := bytes.NewReader(body)
	z := html.NewTokenizer(r)
	for {
		if z.Next() == html.ErrorToken {
			break
		}
		t := z.Token()
		switch t.DataAtom {
		case atom.Link:
			if t.Type == html.StartTagToken || t.Type == html.SelfClosingTagToken {
				attrs := make(map[string]string)
				for _, a := range t.Attr {
					attrs[a.Key] = a.Val
				}
				if attrs["rel"] == "alternate" && attrs["href"] != "" &&
					(attrs["type"] == "application/rss+xml" || attrs["type"] == "application/atom+xml") {
					return attrs["href"], nil
				}
			}
		}
	}
	return "", fmt.Errorf("No feed link found")
}

func doGet(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	res, err := ctxutil.Client(ctx).Do(req)
	if err != nil {
		log.Printf("Error fetching %s: %v", url, err)
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Get request on %s failed with: %s", url, res.Status)
	}
	return ioutil.ReadAll(io.LimitReader(res.Body, 8<<20))
}

// urlFileRef slurps urlstr from the net, writes to a file and returns its
// fileref or "" on error
func (r *run) urlFileRef(urlstr string) string {
	if urlstr == "" {
		return ""
	}
	im := r.im
	im.mu.Lock()
	if br, ok := im.urlFileRef[urlstr]; ok {
		im.mu.Unlock()
		return br.String()
	}
	im.mu.Unlock()

	res, err := ctxutil.Client(r).Get(urlstr)
	if err != nil {
		log.Printf("couldn't get file: %v", err)
		return ""
	}
	defer res.Body.Close()

	filename := urlstr[strings.LastIndex(urlstr, "/")+1:]
	fileRef, err := schema.WriteFileFromReader(r.Host.Target(), filename, res.Body)
	if err != nil {
		log.Printf("couldn't write file: %v", err)
		return ""
	}

	im.mu.Lock()
	defer im.mu.Unlock()
	im.urlFileRef[urlstr] = fileRef
	return fileRef.String()
}

func (im *imp) ServeSetup(w http.ResponseWriter, r *http.Request, ctx *importer.SetupContext) error {
	return tmpl.ExecuteTemplate(w, "serveSetup", ctx)
}

var tmpl = template.Must(template.New("root").Parse(`
{{define "serveSetup"}}
<h1>Configuring Feed</h1>
<form method="get" action="{{.CallbackURL}}">
  <input type="hidden" name="acct" value="{{.AccountNode.PermanodeRef}}">
  <table border=0 cellpadding=3>
  <tr><td align=right>Feed URL</td><td><input name="feedURL" size=50></td></tr>
  <tr><td align=right></td><td><input type="submit" value="Add"></td></tr>
  </table>
</form>
{{end}}
`))

func (im *imp) ServeCallback(w http.ResponseWriter, r *http.Request, ctx *importer.SetupContext) {
	u := r.FormValue("feedURL")
	if u == "" {
		http.Error(w, "Expected a feed URL", 400)
		return
	}
	feed, err := url.Parse(u)
	if err != nil {
		httputil.ServeError(w, r, err)
		return
	}
	if feed.Scheme == "" {
		feed.Scheme = "http"
	}
	if err := ctx.AccountNode.SetAttrs(
		acctAttrFeedURL, feed.String(),
	); err != nil {
		httputil.ServeError(w, r, fmt.Errorf("Error setting attribute: %v", err))
		return
	}
	http.Redirect(w, r, ctx.AccountURL(), http.StatusFound)
}
