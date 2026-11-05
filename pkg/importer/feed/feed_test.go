/*
Copyright 2026 The Perkeep Authors

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

package feed

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"perkeep.org/internal/httputil"
	"perkeep.org/pkg/importer"
	importertest "perkeep.org/pkg/importer/test"
	"perkeep.org/pkg/search"
	"perkeep.org/pkg/secretstore"
)

type feedRoundTripper func(*http.Request) (*http.Response, error)

func (f feedRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestFeedURLRemainsAccountMetadata(t *testing.T) {
	const feedURL = "https://feed.example/feeds/news.xml?format=atom"
	const body = `<feed xmlns="http://www.w3.org/2005/Atom">
		<title>Example</title>
		<entry><id>news-1</id><title>First</title><content>News</content></entry>
	</feed>`
	store, err := secretstore.NewMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	requests := 0
	transport := feedRoundTripper(func(r *http.Request) (*http.Response, error) {
		requests++
		if r.URL.String() != feedURL {
			t.Errorf("request URL = %q; want %q", r.URL, feedURL)
		}
		if r.Header.Get("Authorization") != "" {
			t.Error("ordinary feed request unexpectedly used authentication")
		}
		return &http.Response{
			StatusCode: http.StatusOK, Header: make(http.Header),
			Body: io.NopCloser(strings.NewReader(body)),
		}, nil
	})
	h := importertest.NewMemoryHost(t, importer.HostConfig{
		Secrets: store, HTTPClient: &http.Client{Transport: transport},
	})
	t.Cleanup(func() { h.Close() })
	rc, err := importer.CreateAccount(h, "feed")
	if err != nil {
		t.Fatal(err)
	}
	acct := rc.AccountNode()
	form := url.Values{"acct": {acct.PermanodeRef().String()}, "feedURL": {feedURL}}
	req := httptest.NewRequest("POST", h.ImporterBaseURL()+"feed/callback", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set(httputil.PathSuffixHeader, "feed/callback")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusFound {
		t.Fatalf("feed setup = %d: %s", w.Code, w.Body)
	}

	ctx := context.Background()
	res, err := h.Searcher().Describe(ctx, &search.DescribeRequest{BlobRef: acct.PermanodeRef(), Depth: 1})
	if err != nil {
		t.Fatal(err)
	}
	if got := res.Meta[acct.PermanodeRef().String()].Permanode.Attr.Get(acctAttrFeedURL); got != feedURL {
		t.Fatalf("permanode feed URL = %q; want %q", got, feedURL)
	}
	reloaded, err := h.ObjectFromRef(acct.PermanodeRef())
	if err != nil {
		t.Fatal(err)
	}
	if got := (new(imp)).SummarizeAccount(reloaded); got != "feed "+feedURL {
		t.Fatalf("feed summary = %q", got)
	}
	for i := range 2 {
		if err := (new(imp)).Run(rc); err != nil {
			t.Fatal(err)
		}
		// The run's progress bar, which the account's page shows. The
		// runs share rc's bars, so the second counts its item, unchanged,
		// as skipped, on the first's.
		bars := rc.Progress().Snapshot()
		if len(bars) != 1 || bars[0].Name != "items" || !bars[0].Finished || bars[0].Total != 1 || bars[0].Skipped != int64(i) {
			t.Errorf("run %d's progress bars: %+v", i, bars)
		}
	}
	if requests != 2 {
		t.Errorf("feed requests = %d; want 2", requests)
	}
	if got := rc.RootNode().Attr("title"); got != "Example Items" {
		t.Errorf("import root title = %q; want Example Items", got)
	}
	if rc.RootNode().Attr("camliPath:news-1") == "" {
		t.Error("feed item was not imported")
	}
	entries, err := store.List(ctx)
	if err != nil || len(entries) != 0 {
		t.Fatalf("public feed created secret-store entries: %v, %v", entries, err)
	}
}
