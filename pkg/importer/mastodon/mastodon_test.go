/*
Copyright 2018 The Perkeep Authors

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

package mastodon

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver"
	"perkeep.org/pkg/importer"
	imptest "perkeep.org/pkg/importer/test"
	"perkeep.org/pkg/schema"
	"perkeep.org/pkg/schema/nodeattr"
	"perkeep.org/pkg/test"
)

const (
	testInstance = "http://mastodon.example"
	testToken    = "token"
	testUserID   = "1"
)

// fakeServer is a Mastodon server of one account's statuses.
type fakeServer struct {
	t        *testing.T
	pageSize int

	mu       sync.Mutex
	statuses []*status         // newest first
	sources  map[string]string // status ID => text
	files    map[string]string // path => contents
	pages    int               // of statuses served
	sourced  []string          // IDs of statuses whose sources were served
	fetched  []string          // paths of files served
}

func (s *fakeServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if strings.HasPrefix(r.URL.Path, "/media/") {
		body, ok := s.files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		s.fetched = append(s.fetched, r.URL.Path)
		w.Write([]byte(body))
		return
	}
	if got := r.Header.Get("Authorization"); got != "Bearer "+testToken {
		http.Error(w, `{"error":"The access token is invalid"}`, http.StatusUnauthorized)
		return
	}
	writeJSON := func(v any) {
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(v); err != nil {
			s.t.Error(err)
		}
	}
	if r.URL.Path == "/api/v1/accounts/"+testUserID+"/statuses" {
		if r.FormValue("exclude_reblogs") != "true" {
			s.t.Errorf("statuses requested with reblogs: %v", r.URL)
		}
		s.pages++
		i := 0
		if max := r.FormValue("max_id"); max != "" {
			i = slices.IndexFunc(s.statuses, func(st *status) bool { return st.ID == max }) + 1
		}
		page := s.statuses[i:min(i+s.pageSize, len(s.statuses))]
		if i+len(page) < len(s.statuses) {
			w.Header().Set("Link", fmt.Sprintf(`<%s%s?limit=40&exclude_reblogs=true&max_id=%s>; rel="next", <%s%s?min_id=%s>; rel="prev"`,
				testInstance, r.URL.Path, page[len(page)-1].ID, testInstance, r.URL.Path, page[0].ID))
		}
		writeJSON(page)
		return
	}
	if id, ok := strings.CutPrefix(r.URL.Path, "/api/v1/statuses/"); ok {
		if id, ok := strings.CutSuffix(id, "/source"); ok {
			text, ok := s.sources[id]
			if !ok {
				http.Error(w, `{"error":"Record not found"}`, http.StatusNotFound)
				return
			}
			s.sourced = append(s.sourced, id)
			writeJSON(statusSource{ID: id, Text: text})
			return
		}
	}
	http.NotFound(w, r)
}

// requests returns, and forgets, the counts of the server's requests.
func (s *fakeServer) requests() (pages int, sourced, fetched []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	pages, sourced, fetched = s.pages, s.sourced, s.fetched
	s.pages, s.sourced, s.fetched = 0, nil, nil
	return
}

func testTime(day int) time.Time { return time.Date(2025, 1, day, 12, 0, 0, 0, time.UTC) }

// countClaims returns the number of claims in src.
func countClaims(t *testing.T, src blob.Fetcher) int {
	t.Helper()
	ctx := context.Background()
	n := 0
	err := blobserver.EnumerateAll(ctx, src.(blobserver.BlobEnumerator), func(sb blob.SizedRef) error {
		rc, _, err := src.Fetch(ctx, sb.Ref)
		if err != nil {
			return err
		}
		defer rc.Close()
		b, err := schema.BlobFromReader(sb.Ref, rc)
		if err == nil && b.Type() == schema.TypeClaim {
			n++
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestImport(t *testing.T) {
	test.TLog(t)
	srv := &fakeServer{
		t:        t,
		pageSize: 2,
		statuses: []*status{
			{
				ID: "5", URI: testInstance + "/users/u/statuses/5", URL: testInstance + "/@u/5", CreatedAt: testTime(5),
				Visibility: "public", Content: "<p>hello</p><p>world&#39;s<br />&quot;end&quot;</p>",
			},
			{
				ID: "4", URI: testInstance + "/users/u/statuses/4", URL: testInstance + "/@u/4", CreatedAt: testTime(4),
				Visibility: "unlisted", Language: "en", InReplyToID: "3", InReplyToAccountID: testUserID,
				Content: `<p>see <a href="https://example.com/a/long/path" rel="nofollow noopener" target="_blank"><span class="invisible">https://</span><span class="ellipsis">example.com/a/lo</span><span class="invisible">ng/path</span></a></p>`,
				Card:    &card{URL: "https://example.com/a/long/path", Title: "A page"},
			},
			{
				ID: "3", URI: testInstance + "/users/u/statuses/3", URL: testInstance + "/@u/3", CreatedAt: testTime(3),
				Visibility: "public", Sensitive: true, SpoilerText: "cw", Content: "<p>pics</p>",
				MediaAttachments: []attachment{
					{ID: "31", Type: "image", URL: testInstance + "/media/31.png", Description: "a dog"},
					{ID: "32", Type: "video", URL: testInstance + "/media/32.mp4", PreviewURL: testInstance + "/media/32.jpg"},
				},
			},
			{
				ID: "2", URI: testInstance + "/users/u/statuses/2", URL: testInstance + "/@u/2", CreatedAt: testTime(2),
				Visibility: "direct", Content: "<p>secret</p>",
			},
			{
				ID: "1", URI: testInstance + "/users/u/statuses/1", URL: testInstance + "/@u/1", CreatedAt: testTime(1),
				Visibility: "public", Content: "<p>old</p>",
				MediaAttachments: []attachment{
					{ID: "11", Type: "image", URL: testInstance + "/media/abc.png"},
				},
			},
		},
		sources: map[string]string{
			"5": "hello\n\nworld's\n\"end\"",
			"4": "see https://example.com/a/long/path",
			"3": "pics",
			"2": "secret",
			"1": "old",
		},
		files: map[string]string{
			"/media/31.png":  "dog png",
			"/media/32.mp4":  "video",
			"/media/32.jpg":  "video preview",
			"/media/abc.png": "old png",
		},
	}
	ts := httptest.NewTestServer(t, srv)
	h := imptest.NewMemoryHost(t, importer.HostConfig{HTTPClient: ts.Client()})
	t.Cleanup(func() { h.Close() })
	rc, err := importer.CreateAccount(h, "mastodon")
	if err != nil {
		t.Fatal(err)
	}
	ctx := rc.Context()
	if err := rc.AccountNode().SetAttrs(
		importer.KV(importer.AcctAttrAccessToken, testToken),
		importer.KV(acctAttrClientID, "clientid"),
		importer.KV(acctAttrClientSecret, "supersecret"),
		importer.KV(acctAttrInstanceURL, testInstance),
		importer.KV(importer.AcctAttrUserName, "u"),
		importer.KV(importer.AcctAttrUserID, testUserID),
	); err != nil {
		t.Fatal(err)
	}
	statuses, err := rc.RootNode().ChildPathObject(nodeStatuses)
	if err != nil {
		t.Fatal(err)
	}

	// Status 1 was imported by the importer's version 0, with its HTML
	// as its content, and its image's file on its node.
	oldPNG, err := schema.WriteFileFromReader(ctx, h.Target(), "", strings.NewReader("old png"))
	if err != nil {
		t.Fatal(err)
	}
	old, err := statuses.ChildPathObject(srv.statuses[4].URI)
	if err != nil {
		t.Fatal(err)
	}
	if err := old.SetAttrs(
		importer.KV(nodeattr.Type, nodeTypeStatus),
		importer.KV(attrURI, srv.statuses[4].URI),
		importer.KV(nodeattr.Content, "<p>old</p>"),
		importer.KV("camliPath:abc.png", oldPNG.String()),
		importer.KV(nodeattr.CamliContentImage, oldPNG.String()),
	); err != nil {
		t.Fatal(err)
	}
	if err := rc.AccountNode().SetAttr(importer.AcctAttrCompletedVersion, "0"); err != nil {
		t.Fatal(err)
	}

	child := func(parent *importer.Object, path string) *importer.Object {
		t.Helper()
		n, err := imptest.GetRequiredChildPathObj(parent, path)
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	statusNode := func(i int) *importer.Object {
		t.Helper()
		return child(statuses, srv.statuses[i].URI)
	}
	attrsOf := func(n *importer.Object) map[string]string {
		m := map[string]string{}
		n.ForeachAttr(func(k, v string) { m[k] = v })
		return m
	}
	fileOf := func(path string) string {
		t.Helper()
		ref, err := schema.WriteFileFromReader(ctx, h.Target(), path, strings.NewReader(srv.files["/media/"+path]))
		if err != nil {
			t.Fatal(err)
		}
		return ref.String()
	}
	im := &imp{}
	runImport := func() (claims int) {
		t.Helper()
		before := countClaims(t, h.BlobSource())
		if err := im.Run(rc); err != nil {
			t.Fatal(err)
		}
		statuses, err = h.ObjectFromRef(statuses.PermanodeRef())
		if err != nil {
			t.Fatal(err)
		}
		return countClaims(t, h.BlobSource()) - before
	}

	// The first run, of the importer's new version, is complete.
	claims := runImport()
	pages, sourced, fetched := srv.requests()
	if pages != 3 {
		t.Errorf("first run fetched %d pages; want 3", pages)
	}
	if want := []string{"1", "2", "3", "4", "5"}; !slicesEqualSorted(sourced, want) {
		t.Errorf("first run fetched the sources of %q; want %q", sourced, want)
	}
	if want := []string{"/media/31.png", "/media/32.jpg", "/media/32.mp4"}; !slicesEqualSorted(fetched, want) {
		t.Errorf("first run fetched %q; want %q, not the image status 1 had", fetched, want)
	}
	// Per page: a claim of its new statuses' camliPaths, if any, and one
	// of all else. And the titles of the root and statuses nodes, and the
	// account's completed version.
	if want := 2 + 2 + 1 + 3; claims != want {
		t.Errorf("first run made %d claims; want %d", claims, want)
	}

	got := attrsOf(statusNode(0))
	want := map[string]string{
		nodeattr.Type:      nodeTypeStatus,
		attrID:             "5",
		attrURI:            srv.statuses[0].URI,
		nodeattr.URL:       srv.statuses[0].URL,
		nodeattr.StartDate: "2025-01-05T12:00:00Z",
		nodeattr.Title:     "hello\n\nworld's\n\"end\"",
		nodeattr.Content:   "hello\n\nworld's\n\"end\"",
		attrVisibility:     "public",
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("plain status's attributes:\n got %v\nwant %v", got, want)
	}

	got = attrsOf(statusNode(1))
	if got[attrContentHTML] != srv.statuses[1].Content || got[nodeattr.Content] != srv.sources["4"] {
		t.Errorf("linking status's content %q, HTML %q", got[nodeattr.Content], got[attrContentHTML])
	}
	if got[attrInReplyToID] != "3" || got[attrInReplyToAccountID] != testUserID || got[attrLanguage] != "en" || got[attrVisibility] != "unlisted" {
		t.Errorf("reply's attributes: %v", got)
	}
	if got[attrLinkURL] != "https://example.com/a/long/path" || got[attrLinkTitle] != "A page" {
		t.Errorf("linking status's card: %q, %q", got[attrLinkURL], got[attrLinkTitle])
	}

	pics := statusNode(2)
	if got := attrsOf(pics); got[attrSpoilerText] != "cw" || got[attrSensitive] != "true" || got[attrContentHTML] != "" {
		t.Errorf("status with media: %v", got)
	}
	img, video := child(pics, "image1"), child(pics, "video2")
	if got, want := attrsOf(img), (map[string]string{
		nodeattr.Type:         "mastodon:image",
		attrID:                "31",
		nodeattr.CamliContent: fileOf("31.png"),
		nodeattr.Description:  "a dog",
	}); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("image's attributes:\n got %v\nwant %v", got, want)
	}
	if got, want := attrsOf(video), (map[string]string{
		nodeattr.Type:              "mastodon:video",
		attrID:                     "32",
		nodeattr.CamliContent:      fileOf("32.mp4"),
		nodeattr.CamliContentImage: fileOf("32.jpg"),
	}); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("video's attributes:\n got %v\nwant %v", got, want)
	}
	if got := pics.Attr(nodeattr.CamliContentImage); got != fileOf("31.png") {
		t.Errorf("status's image = %v; want its first image's", got)
	}

	if got := statusNode(3).Attr(attrVisibility); got != "direct" {
		t.Errorf("direct status's visibility = %q", got)
	}

	// Status 1 has the source's text, and its image's node has the file it had.
	old = statusNode(4)
	if got := old.Attr(nodeattr.Content); got != "old" {
		t.Errorf("version 0's status's content = %q; want its source's", got)
	}
	if got := old.Attr("camliPath:abc.png"); got != "" {
		t.Errorf("version 0's status still has its image's file at camliPath:abc.png")
	}
	if got := child(old, "image1").Attr(nodeattr.CamliContent); got != oldPNG.String() {
		t.Errorf("version 0's status's image = %v; want %v, its file", got, oldPNG)
	}
	if got := old.Attr(nodeattr.CamliContentImage); got != oldPNG.String() {
		t.Errorf("version 0's status's camliContentImage = %v; want %v", got, oldPNG)
	}

	// An incremental run with nothing new stops at the first page, and
	// fetches and claims nothing. (Its progress bars are the first run's,
	// continued, of the same RunContext.)
	claims = runImport()
	pages, sourced, fetched = srv.requests()
	if pages != 1 || len(sourced) != 0 || len(fetched) != 0 || claims != 0 {
		t.Errorf("unchanged incremental run fetched %d pages, sources %q, files %q, and made %d claims; want 1 page and nothing else", pages, sourced, fetched, claims)
	}
	if bars := rc.Progress().Snapshot(); len(bars) == 0 || bars[0].Name != "statuses" || bars[0].Skipped != 2 {
		t.Errorf("unchanged incremental run's progress: %+v", bars)
	}

	// A new status, and an edit of status 3 dropping its content warning,
	// are imported, with the sources of only those.
	srv.mu.Lock()
	edited := testTime(9)
	srv.statuses[2].EditedAt = &edited
	srv.statuses[2].SpoilerText = ""
	srv.statuses[2].Sensitive = false
	srv.statuses[2].Content = "<p>pics, edited</p>"
	srv.sources["3"] = "pics, edited"
	srv.statuses = slices.Insert(srv.statuses, 0, &status{
		ID: "6", URI: testInstance + "/users/u/statuses/6", URL: testInstance + "/@u/6", CreatedAt: testTime(6),
		Visibility: "public", Content: "<p>new</p>",
	})
	srv.sources["6"] = "new"
	srv.mu.Unlock()
	runImport()
	pages, sourced, fetched = srv.requests()
	if pages != 3 || !slicesEqualSorted(sourced, []string{"3", "6"}) || len(fetched) != 0 {
		t.Errorf("incremental run fetched %d pages, sources %q, files %q; want 3 pages, and the sources of 3 and 6", pages, sourced, fetched)
	}
	if got := statusNode(0).Attr(nodeattr.Content); got != "new" {
		t.Errorf("new status's content = %q", got)
	}
	got = attrsOf(statusNode(3))
	if got[nodeattr.Content] != "pics, edited" || got[nodeattr.DateModified] != "2025-01-09T12:00:00Z" || got[attrSpoilerText] != "" || got[attrSensitive] != "" {
		t.Errorf("edited status's attributes: %v", got)
	}
	if got["camliPath:image1"] == "" || got["camliPath:video2"] == "" {
		t.Errorf("edited status lost its media: %v", got)
	}
}

func slicesEqualSorted(got, want []string) bool {
	got = slices.Clone(got)
	slices.Sort(got)
	return slices.Equal(got, want)
}

func TestPlainHTML(t *testing.T) {
	tests := []struct{ text, want string }{
		{"", ""},
		{"hi", "<p>hi</p>"},
		{"a\nb\n\n\nc", "<p>a<br />b</p><p>c</p>"},
		{`<b> & "it's"`, "<p>&lt;b&gt; &amp; &quot;it&#39;s&quot;</p>"},
	}
	for _, tt := range tests {
		if got := plainHTML(tt.text); got != tt.want {
			t.Errorf("plainHTML(%q) = %q; want %q", tt.text, got, tt.want)
		}
	}
}

func TestNextLink(t *testing.T) {
	tests := []struct{ link, want string }{
		{"", ""},
		{`<https://m.example/api/v1/x?max_id=7>; rel="next", <https://m.example/api/v1/x?min_id=9>; rel="prev"`, "https://m.example/api/v1/x?max_id=7"},
		{`<https://m.example/api/v1/x?min_id=9>; rel="prev"`, ""},
	}
	for _, tt := range tests {
		h := http.Header{}
		if tt.link != "" {
			h.Set("Link", tt.link)
		}
		if got := nextLink(h); got != tt.want {
			t.Errorf("nextLink(%q) = %q; want %q", tt.link, got, tt.want)
		}
	}
}

// rateLimiter is a server's rate limit of calls per window.
type rateLimiter struct {
	limit  int
	window time.Duration

	mu      sync.Mutex
	start   time.Time // of the current window
	used    int       // calls of the current window
	refused int       // calls refused for the limit
}

func (rl *rateLimiter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	now := time.Now()
	if start := now.Truncate(rl.window); start != rl.start {
		rl.start, rl.used = start, 0
	}
	rl.used++
	w.Header().Set("X-RateLimit-Limit", fmt.Sprint(rl.limit))
	w.Header().Set("X-RateLimit-Remaining", fmt.Sprint(max(rl.limit-rl.used, 0)))
	w.Header().Set("X-RateLimit-Reset", rl.start.Add(rl.window).Format("2006-01-02T15:04:05.000Z07:00"))
	if rl.used > rl.limit {
		rl.refused++
		http.Error(w, `{"error":"Too many requests"}`, http.StatusTooManyRequests)
		return
	}
	w.Write([]byte("{}"))
}

func TestRateLimit(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rl := &rateLimiter{limit: 20, window: 5 * time.Minute}
		ts := httptest.NewTestServer(t, rl)
		c, err := newAPIClient(ts.Client(), testInstance, testToken)
		if err != nil {
			t.Fatal(err)
		}
		var waits int
		c.logf = func(string, ...any) { waits++ }
		ctx := context.Background()
		get := func() {
			t.Helper()
			var v struct{}
			if _, err := c.get(ctx, c.endpoint("/api/v1/x", nil), &v); err != nil {
				t.Fatal(err)
			}
		}

		// Of each window's 20 calls, 10 are left for other apps.
		start := time.Now()
		for range 25 {
			get()
		}
		if rl.refused != 0 {
			t.Errorf("%d calls refused", rl.refused)
		}
		if got, want := time.Since(start), 10*time.Minute; got != want || waits != 2 {
			t.Errorf("25 calls took %v and %d waits; want %v and 2", got, waits, want)
		}

		// Calls refused, as other apps used the rest, are retried once
		// the window ends.
		rl.mu.Lock()
		rl.used = rl.limit
		rl.mu.Unlock()
		start = time.Now()
		get()
		if rl.refused != 1 {
			t.Errorf("%d calls refused; want 1", rl.refused)
		}
		if got, want := time.Since(start), 5*time.Minute; got != want {
			t.Errorf("refused call waited %v; want %v, until the window's end", got, want)
		}
	})
}

func TestNextPageElsewhere(t *testing.T) {
	ts := httptest.NewTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Link", `<https://evil.example/api/v1/x?max_id=1>; rel="next"`)
		w.Write([]byte("[]"))
	}))
	c, err := newAPIClient(ts.Client(), testInstance, testToken)
	if err != nil {
		t.Fatal(err)
	}
	var v []any
	if _, err := c.get(context.Background(), c.endpoint("/api/v1/x", url.Values{}), &v); err == nil || !strings.Contains(err.Error(), "elsewhere") {
		t.Errorf("get of a page whose next page is elsewhere = %v; want an error", err)
	}
}
