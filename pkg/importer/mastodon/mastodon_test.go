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
	"maps"
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

var (
	testUser  = account{ID: testUserID, Username: "u", Acct: "u", URI: testInstance + "/users/u", URL: testInstance + "/@u"}
	otherUser = account{ID: "9", Username: "other", Acct: "other@remote.example", DisplayName: "Other",
		URI: "https://remote.example/users/other", URL: "https://remote.example/@other",
		Note: "<p>other&#39;s bio</p>", Avatar: testInstance + "/media/other-avatar.png"}
	thirdUser = account{ID: "10", Username: "third", Acct: "third@far.example",
		URI: "https://far.example/users/third", URL: "https://far.example/@third",
		Avatar: testInstance + "/media/missing.png"}
)

// fakeServer is a Mastodon server of one account's statuses, favourites,
// bookmarks, follows and profile.
type fakeServer struct {
	t        *testing.T
	pageSize int

	mu         sync.Mutex
	statuses   []*status         // the account's own and boosts, newest first
	others     []*status         // others' statuses that aren't in lists
	favourites []*status         // most recent first
	bookmarks  []*status         // most recent first
	following  []*account        // most recent first
	me         *account          // with its source
	sources    map[string]string // status ID => text
	files      map[string]string // path => contents
	pages      map[string]int    // of lists served, by path
	sourced    []string          // IDs of statuses whose sources were served
	byID       [][]string        // IDs of statuses looked up, by request
	fetched    []string          // paths of files served
}

func (s *fakeServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if strings.HasPrefix(r.URL.Path, "/media_proxy/") || r.Host == "gone.example" {
		// The server's copies of others' media, which it lacks, and
		// a server that's failing.
		http.Error(w, "oops", http.StatusInternalServerError)
		return
	}
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
	// page serves the page of a list of n items at the request's offset.
	page := func(n int) (i, j int) {
		s.pages[r.URL.Path]++
		fmt.Sscan(r.FormValue("offset"), &i)
		j = min(i+s.pageSize, n)
		if j < n {
			w.Header().Set("Link", fmt.Sprintf(`<%s%s?offset=%d>; rel="next", <%s%s?min_id=x>; rel="prev"`, testInstance, r.URL.Path, j, testInstance, r.URL.Path))
		}
		return i, j
	}
	switch r.URL.Path {
	case "/api/v1/accounts/" + testUserID + "/statuses":
		i, j := page(len(s.statuses))
		writeJSON(s.statuses[i:j])
		return
	case "/api/v1/favourites":
		i, j := page(len(s.favourites))
		writeJSON(s.favourites[i:j])
		return
	case "/api/v1/bookmarks":
		i, j := page(len(s.bookmarks))
		writeJSON(s.bookmarks[i:j])
		return
	case "/api/v1/accounts/" + testUserID + "/following":
		i, j := page(len(s.following))
		writeJSON(s.following[i:j])
		return
	case "/api/v1/accounts/verify_credentials":
		writeJSON(s.me)
		return
	case "/api/v1/statuses":
		ids := r.URL.Query()["id[]"]
		s.byID = append(s.byID, ids)
		var sts []*status
		for _, id := range ids {
			for _, st := range slices.Concat(s.statuses, s.others, s.favourites, s.bookmarks) {
				if st.ID == id {
					sts = append(sts, st)
					break
				}
			}
		}
		writeJSON(sts)
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

// requests is what a fakeServer served.
type requests struct {
	pages   map[string]int
	sourced []string
	byID    [][]string
	fetched []string
}

// requests returns, and forgets, what the server served.
func (s *fakeServer) requests() requests {
	s.mu.Lock()
	defer s.mu.Unlock()
	rs := requests{s.pages, s.sourced, s.byID, s.fetched}
	s.pages, s.sourced, s.byID, s.fetched = map[string]int{}, nil, nil, nil
	return rs
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

// ownStatus returns a status of the test user, of ID id.
func ownStatus(id string, day int, visibility, content string) *status {
	return &status{
		ID: id, URI: testInstance + "/users/u/statuses/" + id, URL: testInstance + "/@u/" + id, CreatedAt: testTime(day),
		Visibility: visibility, Content: content, Account: testUser,
	}
}

// otherStatus returns a status of another user, of ID id.
func otherStatus(id string, day int, content string) *status {
	return &status{
		ID: id, URI: "https://remote.example/users/other/statuses/" + id, URL: "https://remote.example/@other/" + id, CreatedAt: testTime(day),
		Visibility: "public", Content: content, Account: otherUser,
	}
}

// boost returns a status of the test user's, of ID id, boosting st.
func boost(id string, day int, st *status) *status {
	return &status{
		ID: id, URI: testInstance + "/users/u/statuses/" + id + "/activity", CreatedAt: testTime(day),
		Visibility: "public", Account: testUser, Reblog: st,
	}
}

func TestImport(t *testing.T) {
	test.TLog(t)
	s1 := ownStatus("1", 1, "public", "<p>old</p>")
	s1.MediaAttachments = []attachment{{ID: "11", Type: "image", URL: testInstance + "/media/abc.png"}}
	s2 := ownStatus("2", 2, "direct", "<p>secret</p>")
	s3 := ownStatus("3", 3, "public", "<p>pics</p>")
	s3.Sensitive = true
	s3.SpoilerText = "cw"
	s3.MediaAttachments = []attachment{
		{ID: "31", Type: "image", URL: testInstance + "/media/31.png", Description: "a dog"},
		{ID: "32", Type: "video", URL: testInstance + "/media/32.mp4", PreviewURL: testInstance + "/media/32.jpg"},
	}
	s2.InReplyToID, s2.InReplyToAccountID = "3", testUserID
	s4 := ownStatus("4", 4, "unlisted", `<p>see <a href="https://example.com/a/long/path" rel="nofollow noopener" target="_blank"><span class="invisible">https://</span><span class="ellipsis">example.com/a/lo</span><span class="invisible">ng/path</span></a></p>`)
	s4.Language = "en"
	s4.Card = &card{URL: "https://example.com/a/long/path", Title: "A page"}
	s4.InReplyToID, s4.InReplyToAccountID = "o2", otherUser.ID
	s5 := ownStatus("5", 5, "public", "<p>hello</p><p>world&#39;s<br />&quot;end&quot;</p>")
	s5.SpoilerText = "cw5"

	o1 := otherStatus("o1", 1, `<p><span class="h-card"><a href="http://mastodon.example/@u" class="u-url mention">@<span>u</span></a></span> look</p><p>a cat</p>`)
	o1.MediaAttachments = []attachment{{ID: "o11", Type: "image", URL: testInstance + "/media_proxy/o11/original", RemoteURL: "https://remote.example/media/o1.png", Description: "a cat"}}
	o2 := otherStatus("o2", 2, "<p>a question</p>")
	o3 := otherStatus("o3", 3, "<p>likeable</p>")
	o3.MediaAttachments = []attachment{{ID: "o31", Type: "image", URL: testInstance + "/media_proxy/o31/original", RemoteURL: "https://gone.example/media/o31.png"}}

	srv := &fakeServer{
		t:          t,
		pageSize:   2,
		statuses:   []*status{s5, boost("b7", 7, o1), s4, boost("b8", 8, s3), s3, s2, s1},
		others:     []*status{o2},
		favourites: []*status{o3, s5},
		bookmarks:  []*status{o1},
		following:  []*account{&otherUser, &thirdUser},
		me: &account{
			ID: testUserID, Username: "u", Acct: "u", DisplayName: "U", URI: testUser.URI, URL: testUser.URL,
			Note:   "<p>bio &amp; more</p>",
			Avatar: testInstance + "/media/avatar.png", Header: testInstance + "/media/header.png",
			Source: &accountSource{Note: "bio & more", Fields: []field{{Name: "Website", Value: "https://u.example"}}},
		},
		sources: map[string]string{
			"5": "hello\n\nworld's\n\"end\"",
			"4": "see https://example.com/a/long/path",
			"3": "pics",
			"2": "secret",
			"1": "old",
		},
		files: map[string]string{
			"/media/31.png":           "dog png",
			"/media/32.mp4":           "video",
			"/media/32.jpg":           "video preview",
			"/media/abc.png":          "old png",
			"/media/o1.png":           "cat png",
			"/media/avatar.png":       "avatar",
			"/media/header.png":       "header",
			"/media/other-avatar.png": "other avatar",
		},
		pages: map[string]int{},
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
	old, err := statuses.ChildPathObject(s1.URI)
	if err != nil {
		t.Fatal(err)
	}
	if err := old.SetAttrs(
		importer.KV(nodeattr.Type, nodeTypeStatus),
		importer.KV(attrURI, s1.URI),
		importer.KV(nodeattr.Content, "<p>old</p>"),
		importer.KV("camliPath:abc.png", oldPNG.String()),
		importer.KV(nodeattr.CamliContentImage, oldPNG.String()),
	); err != nil {
		t.Fatal(err)
	}
	if err := rc.AccountNode().SetAttr(importer.AcctAttrCompletedVersion, "0"); err != nil {
		t.Fatal(err)
	}

	// node returns the node at the path from the root.
	node := func(path ...string) *importer.Object {
		t.Helper()
		n, err := h.ObjectFromRef(rc.RootNode().PermanodeRef())
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range path {
			if n, err = imptest.GetRequiredChildPathObj(n, p); err != nil {
				t.Fatal(err)
			}
		}
		return n
	}
	attrsOf := func(n *importer.Object) map[string]string {
		m := map[string]string{}
		n.ForeachAttr(func(k, v string) { m[k] = v })
		return m
	}
	fileOf := func(path, contents string) string {
		t.Helper()
		ref, err := schema.WriteFileFromReader(ctx, h.Target(), path, strings.NewReader(contents))
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
		return countClaims(t, h.BlobSource()) - before
	}
	onePageEach := map[string]int{
		"/api/v1/accounts/1/statuses":  1,
		"/api/v1/favourites":           1,
		"/api/v1/bookmarks":            1,
		"/api/v1/accounts/1/following": 1,
	}

	// The first run, of the importer's new version, is complete.
	claims := runImport()
	rs := srv.requests()
	if want := map[string]int{
		"/api/v1/accounts/1/statuses":  4,
		"/api/v1/favourites":           1,
		"/api/v1/bookmarks":            1,
		"/api/v1/accounts/1/following": 1,
	}; !maps.Equal(rs.pages, want) {
		t.Errorf("first run fetched pages %v; want %v", rs.pages, want)
	}
	if want := []string{"1", "2", "3", "4", "5"}; !slicesEqualSorted(rs.sourced, want) {
		t.Errorf("first run fetched the sources of %q; want %q, each once", rs.sourced, want)
	}
	if want := [][]string{{"o2"}, {"3"}}; fmt.Sprint(rs.byID) != fmt.Sprint(want) {
		t.Errorf("first run looked up statuses %q; want %q, those replied to", rs.byID, want)
	}
	if want := []string{"/media/31.png", "/media/32.jpg", "/media/32.mp4", "/media/avatar.png", "/media/header.png", "/media/o1.png", "/media/other-avatar.png"}; !slicesEqualSorted(rs.fetched, want) {
		t.Errorf("first run fetched %q; want %q, not the image status 1 had", rs.fetched, want)
	}
	// A claim of each page, of all its items' changes, children and media
	// included: 4 of statuses, and one of each other list. And one of the
	// root's children, one of the profile, and the account's completed
	// version.
	if want := 4 + 3 + 3; claims != want {
		t.Errorf("first run made %d claims; want %d", claims, want)
	}

	got := attrsOf(node(nodeStatuses, s5.URI))
	want := map[string]string{
		nodeattr.Type:      nodeTypeStatus,
		attrID:             "5",
		attrURI:            s5.URI,
		nodeattr.URL:       s5.URL,
		nodeattr.StartDate: "2025-01-05T12:00:00Z",
		nodeattr.Title:     "hello\n\nworld's\n\"end\"",
		nodeattr.Content:   "hello\n\nworld's\n\"end\"",
		attrSpoilerText:    "cw5",
		attrVisibility:     "public",
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("plain status's attributes:\n got %v\nwant %v", got, want)
	}

	got = attrsOf(node(nodeStatuses, s4.URI))
	if got[attrContentHTML] != s4.Content || got[nodeattr.Content] != srv.sources["4"] {
		t.Errorf("linking status's content %q, HTML %q", got[nodeattr.Content], got[attrContentHTML])
	}
	if got[attrInReplyTo] != o2.URI || got[attrInReplyToID] != "o2" || got[attrLanguage] != "en" || got[attrVisibility] != "unlisted" {
		t.Errorf("reply's attributes: %v", got)
	}
	if got[attrLinkURL] != "https://example.com/a/long/path" || got[attrLinkTitle] != "A page" {
		t.Errorf("linking status's card: %q, %q", got[attrLinkURL], got[attrLinkTitle])
	}
	if got := attrsOf(node(nodeLinked, o2.URI)); got[nodeattr.Type] != nodeTypeLinkedStatus || got[nodeattr.Content] != "a question" ||
		got[attrAuthor] != "other@remote.example" || got[attrAuthorName] != "Other" || got[attrAuthorURI] != otherUser.URI || got[attrContentHTML] != "" {
		t.Errorf("status replied to: %v", got)
	}

	pics := node(nodeStatuses, s3.URI)
	if got := attrsOf(pics); got[attrSpoilerText] != "cw" || got[attrSensitive] != "true" || got[attrContentHTML] != "" {
		t.Errorf("status with media: %v", got)
	}
	if got, want := attrsOf(node(nodeStatuses, s3.URI, "image1")), (map[string]string{
		nodeattr.Type:         "mastodon:image",
		attrID:                "31",
		nodeattr.CamliContent: fileOf("31.png", "dog png"),
		nodeattr.Description:  "a dog",
	}); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("image's attributes:\n got %v\nwant %v", got, want)
	}
	if got, want := attrsOf(node(nodeStatuses, s3.URI, "video2")), (map[string]string{
		nodeattr.Type:              "mastodon:video",
		attrID:                     "32",
		nodeattr.CamliContent:      fileOf("32.mp4", "video"),
		nodeattr.CamliContentImage: fileOf("32.jpg", "video preview"),
	}); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("video's attributes:\n got %v\nwant %v", got, want)
	}
	if got := pics.Attr(nodeattr.CamliContentImage); got != fileOf("31.png", "dog png") {
		t.Errorf("status's image = %v; want its first image's", got)
	}

	if got := attrsOf(node(nodeStatuses, s2.URI)); got[attrVisibility] != "direct" || got[attrInReplyTo] != s3.URI {
		t.Errorf("direct reply's attributes: %v", got)
	}

	// Status 1 has the source's text, and its image's node has the file it had.
	old = node(nodeStatuses, s1.URI)
	if got := old.Attr(nodeattr.Content); got != "old" {
		t.Errorf("version 0's status's content = %q; want its source's", got)
	}
	if got := old.Attr("camliPath:abc.png"); got != "" {
		t.Errorf("version 0's status still has its image's file at camliPath:abc.png")
	}
	if got := node(nodeStatuses, s1.URI, "image1").Attr(nodeattr.CamliContent); got != oldPNG.String() {
		t.Errorf("version 0's status's image = %v; want %v, its file", got, oldPNG)
	}

	// Boosts refer to the statuses boosted: another's, imported with its
	// media, from its own server, which the server no longer has; and
	// the account's own.
	linkedCat := node(nodeLinked, o1.URI)
	if got := attrsOf(node(nodeReblogs, srv.statuses[1].URI)); got[nodeattr.Type] != nodeTypeReblog || got[nodeattr.CamliContent] != linkedCat.PermanodeRef().String() ||
		got[attrSubject] != o1.URI || got[nodeattr.StartDate] != "2025-01-07T12:00:00Z" || got[nodeattr.Title] != "Boost: @u look\n\na cat" {
		t.Errorf("boost's attributes: %v", got)
	}
	if got := attrsOf(linkedCat); got[nodeattr.Content] != "@u look\n\na cat" || got[attrContentHTML] != o1.Content || got[nodeattr.CamliContentImage] != fileOf("o1.png", "cat png") {
		t.Errorf("boosted status's attributes: %v", got)
	}
	if got := node(nodeLinked, o1.URI, "image1").Attr(nodeattr.Description); got != "a cat" {
		t.Errorf("boosted status's image's description = %q", got)
	}
	if got := node(nodeReblogs, srv.statuses[3].URI).Attr(nodeattr.CamliContent); got != pics.PermanodeRef().String() {
		t.Errorf("boost of own status refers to %v; want %v", got, pics.PermanodeRef())
	}

	// Favourites and bookmarks refer to their statuses too.
	if got := attrsOf(node(nodeFavourites, o3.URI)); got[nodeattr.Type] != nodeTypeFavourite || got[nodeattr.CamliContent] != node(nodeLinked, o3.URI).PermanodeRef().String() || got[nodeattr.Title] != "Favourite: likeable" {
		t.Errorf("favourite's attributes: %v", got)
	}
	if got := node(nodeLinked, o3.URI).Attr("camliPath:image1"); got != "" {
		t.Errorf("favourited status has the image that couldn't be fetched, %v", got)
	}
	if got := node(nodeFavourites, s5.URI).Attr(nodeattr.CamliContent); got != node(nodeStatuses, s5.URI).PermanodeRef().String() {
		t.Errorf("favourite of own status refers to %v", got)
	}
	if got := attrsOf(node(nodeBookmarks, o1.URI)); got[nodeattr.Type] != nodeTypeBookmark || got[nodeattr.CamliContent] != linkedCat.PermanodeRef().String() {
		t.Errorf("bookmark's attributes: %v", got)
	}

	// The profile, and follows.
	if got, want := attrsOf(node(nodeProfile)), (map[string]string{
		nodeattr.Type:               nodeTypeProfile,
		attrID:                      testUserID,
		attrURI:                     testUser.URI,
		nodeattr.URL:                testUser.URL,
		nodeattr.Title:              "U (@u@mastodon.example)",
		attrAccount:                 "u@mastodon.example",
		nodeattr.Description:        "bio & more",
		nodeattr.CamliContentImage:  fileOf("avatar.png", "avatar"),
		attrAvatarURL:               srv.me.Avatar,
		attrHeader:                  fileOf("header.png", "header"),
		attrHeaderURL:               srv.me.Header,
		attrFieldPrefix + "Website": "https://u.example",
	}); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("profile's attributes:\n got %v\nwant %v", got, want)
	}
	otherProfile := node(nodeAccounts, otherUser.URI)
	if got := attrsOf(otherProfile); got[nodeattr.Type] != nodeTypeProfile || got[nodeattr.Title] != "Other (@other@remote.example)" ||
		got[nodeattr.Description] != "other's bio" || got[attrContentHTML] != "" || got[nodeattr.CamliContentImage] != fileOf("other-avatar.png", "other avatar") {
		t.Errorf("followed profile's attributes: %v", got)
	}
	if got := attrsOf(node(nodeFollowing, otherUser.URI)); got[nodeattr.Type] != nodeTypeFollow || got[nodeattr.CamliContent] != otherProfile.PermanodeRef().String() ||
		got[attrSubject] != otherUser.URI || got[nodeattr.Title] != "Follow: Other (@other@remote.example)" {
		t.Errorf("follow's attributes: %v", got)
	}
	if got := node(nodeAccounts, thirdUser.URI).Attr(nodeattr.CamliContentImage); got != "" {
		t.Errorf("profile of no avatar has image %v", got)
	}

	// An incremental run with nothing new stops at each list's first
	// page, and fetches and claims nothing else. (Its progress bars are
	// the first run's, continued, of the same RunContext.)
	claims = runImport()
	rs = srv.requests()
	if !maps.Equal(rs.pages, onePageEach) || len(rs.sourced) != 0 || len(rs.byID) != 0 || len(rs.fetched) != 0 || claims != 0 {
		t.Errorf("unchanged incremental run fetched pages %v, sources %q, statuses %q, files %q, and made %d claims; want a page of each list and nothing else",
			rs.pages, rs.sourced, rs.byID, rs.fetched, claims)
	}
	// Of the first run's 7 statuses and boosts, status 3 was skipped, as
	// imported already with its boost, a page before; of this run's, the
	// page's 2.
	if bars := rc.Progress().Snapshot(); len(bars) == 0 || bars[0].Name != "statuses" || bars[0].Done != 9 || bars[0].Skipped != 3 {
		t.Errorf("unchanged incremental run's progress: %+v", bars)
	}

	// A new status, and an edit of status 5 dropping its content warning,
	// are imported, with the sources of only those.
	srv.mu.Lock()
	edited := testTime(9)
	s5.EditedAt = &edited
	s5.SpoilerText = ""
	s5.Content = "<p>hello, edited</p>"
	srv.sources["5"] = "hello, edited"
	srv.statuses = slices.Insert(srv.statuses, 0, ownStatus("6", 6, "public", "<p>new</p>"))
	srv.sources["6"] = "new"
	srv.mu.Unlock()
	runImport()
	rs = srv.requests()
	if rs.pages["/api/v1/accounts/1/statuses"] != 2 || !slicesEqualSorted(rs.sourced, []string{"5", "6"}) || len(rs.fetched) != 0 {
		t.Errorf("incremental run fetched pages %v, sources %q, files %q; want 2 pages of statuses, and the sources of 5 and 6", rs.pages, rs.sourced, rs.fetched)
	}
	if got := node(nodeStatuses, srv.statuses[0].URI).Attr(nodeattr.Content); got != "new" {
		t.Errorf("new status's content = %q", got)
	}
	got = attrsOf(node(nodeStatuses, s5.URI))
	if got[nodeattr.Content] != "hello, edited" || got[nodeattr.DateModified] != "2025-01-09T12:00:00Z" || got[attrSpoilerText] != "" {
		t.Errorf("edited status's attributes: %v", got)
	}
	if got := node(nodeFavourites, s5.URI).Attr(nodeattr.Title); got != "Favourite: hello, edited" {
		t.Errorf("favourite of edited status's title = %q", got)
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

func TestHTMLText(t *testing.T) {
	tests := []struct{ html, want string }{
		{"", ""},
		{"<p>hi</p>", "hi"},
		{"<p>a<br />b</p><p>c &amp; d&#39;s</p>", "a\nb\n\nc & d's"},
		{`<p>see <a href="https://example.com/a/long/path"><span class="invisible">https://</span><span class="ellipsis">example.com/a/lo</span><span class="invisible">ng/path</span></a></p>`, "see https://example.com/a/long/path"},
		{`<p><span class="h-card"><a href="https://m.example/@u" class="u-url mention">@<span>u</span></a></span> <a href="https://m.example/tags/go" class="mention hashtag" rel="tag">#<span>go</span></a></p>`, "@u #go"},
	}
	for _, tt := range tests {
		if got := htmlText(tt.html); got != tt.want {
			t.Errorf("htmlText(%q) = %q; want %q", tt.html, got, tt.want)
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
