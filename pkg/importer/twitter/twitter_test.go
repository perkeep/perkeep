/*
Copyright 2014 The Perkeep Authors

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

package twitter

import (
	"context"
	"fmt"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"

	"github.com/garyburd/go-oauth/oauth"
	"go4.org/ctxutil"
	"perkeep.org/internal/httputil"
	"perkeep.org/pkg/importer"
	imptest "perkeep.org/pkg/importer/test"
	"perkeep.org/pkg/schema"
	"perkeep.org/pkg/schema/nodeattr"
	"perkeep.org/pkg/test"
)

var ctxbg = context.Background()

func TestGetUserID(t *testing.T) {
	ctx, cancel := context.WithCancel(context.WithValue(context.TODO(), ctxutil.HTTPClient, &http.Client{
		Transport: httputil.NewFakeTransport(map[string]func() *http.Response{
			apiURL + userInfoAPIPath: httputil.FileResponder(filepath.FromSlash("testdata/verify_credentials-res.json")),
		}),
	}))
	defer cancel()
	inf, err := getUserInfo(importer.OAuthContext{Ctx: ctx, Client: &oauth.Client{}, Creds: &oauth.Credentials{}})
	if err != nil {
		t.Fatal(err)
	}
	want := userInfo{
		ID:         "2325935334",
		ScreenName: "lejatorn",
		Name:       "Mathieu Lonjaret",
	}
	if inf != want {
		t.Errorf("user info = %+v; want %+v", inf, want)
	}
}

func checkTweets(t *testing.T, rc *importer.RunContext, expectedPostGroups ...map[string]string) {
	postsNode, err := imptest.GetRequiredChildPathObj(rc.RootNode(), "tweets")
	if err != nil {
		t.Fatal(err)
	}

	childRefs, err := imptest.FindChildRefs(postsNode)
	if err != nil {
		t.Fatal(err)
	}

	// Merges groups, last wins
	expectedPosts := map[string]string{}
	for _, posts := range expectedPostGroups {
		maps.Copy(expectedPosts, posts)
	}

	if len(childRefs) != len(expectedPosts) {
		t.Fatalf("After import, found %d child refs, want %d: %v", len(childRefs), len(expectedPosts), childRefs)
	}

	for _, ref := range childRefs {
		childNode, err := rc.Host.ObjectFromRef(ref)
		if err != nil {
			t.Fatal(err)
		}
		foundID := childNode.Attr("twitterId")
		expectedContent, ok := expectedPosts[foundID]
		if !ok {
			t.Fatalf("Found unexpected child node %v with id %q", childNode, foundID)
		}
		foundContent := childNode.Attr(nodeattr.Content)
		if foundContent != expectedContent {
			t.Fatalf("Found unexpected child node %v with content %q when we want %q", childNode, foundContent, expectedContent)
		}
		delete(expectedPosts, foundID)
	}
	if len(expectedPosts) != 0 {
		t.Fatalf("The following entries were expected but not found: %#v", expectedPosts)
	}
}

// TestIntegrationRun tests both the twitter API and zip file import paths.
func TestIntegrationRun(t *testing.T) {
	test.TLog(t)
	const accessToken = "foo"
	const accessSecret = "bar"
	const userID = "camlistore_test"

	responder := httputil.FileResponder("testdata/user_tweets.json")
	transport, err := httputil.NewRegexpFakeTransport([]*httputil.Matcher{
		{URLRegex: `^https://api\.x\.com/2/users/camlistore_test/tweets\?`, Fn: responder},
	})
	if err != nil {
		t.Fatal(err)
	}

	imptest.ImporterTest(t, "twitter", transport, func(rc *importer.RunContext) {

		err = rc.AccountNode().SetAttrs(importer.AcctAttrAccessToken, accessToken, importer.AcctAttrAccessTokenSecret, accessSecret, importer.AcctAttrUserID, userID)
		if err != nil {
			t.Fatal(err)
		}

		// First, run without the zip.
		testee := imp{}
		if err := testee.Run(rc); err != nil {
			t.Fatal(err)
		}

		// Tests that special characters are decoded properly, #476.
		jsonTweets := map[string]string{
			"727366997390946304": "I am a test account. Boop beep.",
			"727613700438265858": "foo and bar",
			"727613616149565440": `More beeping and booping & <> . $ % ^ * && /\/\()!`,
		}

		checkTweets(t, rc, jsonTweets)

		zipFile, err := os.Open("testdata/camlistore_test.zip")
		if err != nil {
			t.Fatal(err)
		}
		defer zipFile.Close()

		zipRef, err := schema.WriteFileFromReader(ctxbg, rc.Host.Target(), "camlistore_test.zip", zipFile)
		if err != nil {
			t.Fatal(err)
		}
		err = rc.AccountNode().SetAttrs(acctAttrTweetZip, zipRef.String())
		if err != nil {
			t.Fatal(err)
		}

		// Now run with the zip.
		if err := testee.Run(rc); err != nil {
			t.Fatal(err)
		}

		zipTweets := map[string]string{
			// Different text from JSON version for this item. Tests that importer prefers JSON.
			// Included here just to explain the test.
			"727366997390946304": "I am a test account. Beep boop.",
			"727367542772133888": `& <> . $ % ^ * && /\/\()! @Camlistore camlistore. https://t.co/Ld5gT3wjyq`,
		}
		checkTweets(t, rc, zipTweets, jsonTweets)
	})
}

// TestAPIPagesAndSinceID tests a run through the API v2's pages of
// tweets, with their media, and that an incremental run then asks for
// only the tweets since the newest imported.
func TestAPIPagesAndSinceID(t *testing.T) {
	im := &imp{}
	api := im.MakeTestData().(*fakeAPI)
	var (
		mu      sync.Mutex
		sinceID []string // of the API requests
	)
	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Host == "api.x.com" {
			mu.Lock()
			sinceID = append(sinceID, req.URL.Query().Get("since_id"))
			mu.Unlock()
		}
		return api.RoundTrip(req)
	})
	h := imptest.NewMemoryHost(t, importer.HostConfig{
		HTTPClient:   &http.Client{Transport: transport},
		ClientId:     map[string]string{"twitter": "fakeClientID"},
		ClientSecret: map[string]string{"twitter": "fakeClientSecret"},
	})
	t.Cleanup(func() { h.Close() })
	rc, err := importer.CreateAccount(h, "twitter")
	if err != nil {
		t.Fatal(err)
	}
	if err := im.SetTestAccount(rc.AccountNode()); err != nil {
		t.Fatal(err)
	}

	if err := im.Run(rc); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(sinceID) != "[  ]" {
		t.Errorf("first run's requests' since_id = %q; want 3 pages, without it", sinceID)
	}
	tweets, err := imptest.GetRequiredChildPathObj(rc.RootNode(), "tweets")
	if err != nil {
		t.Fatal(err)
	}
	refs, err := imptest.FindChildRefs(tweets)
	if err != nil {
		t.Fatal(err)
	}
	if want := int(api.maxID - api.minID); len(refs) != want {
		t.Errorf("imported %d tweets; want %d", len(refs), want)
	}
	// A tweet of fakeEntities' first kind with media, of fakeMedia.
	withMedia := api.maxID - 1
	if withMedia%7 != 1 {
		t.Fatalf("tweet %d isn't of fakeEntities' kind with media", withMedia)
	}
	tweet, err := imptest.GetRequiredChildPathObj(tweets, strconv.FormatInt(withMedia, 10))
	if err != nil {
		t.Fatal(err)
	}
	if tweet.Attr("camliPath:foo.jpg") == "" || tweet.Attr("camliPath:bar.png") == "" || tweet.Attr(nodeattr.CamliContentImage) == "" {
		t.Errorf("tweet %d's media weren't imported", withMedia)
	}
	newest := strconv.FormatInt(api.maxID, 10)
	if got := rc.AccountNode().Attr(acctAttrNewestTweetID); got != newest {
		t.Errorf("%s = %q; want %q", acctAttrNewestTweetID, got, newest)
	}

	sinceID = nil
	if err := im.Run(rc); err != nil {
		t.Fatal(err)
	}
	if len(sinceID) != 1 || sinceID[0] != newest {
		t.Errorf("incremental run's requests' since_id = %q; want one, of %q", sinceID, newest)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }
