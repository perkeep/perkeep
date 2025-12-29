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
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/garyburd/go-oauth/oauth"
	"go4.org/ctxutil"
	"perkeep.org/internal/httputil"
	"perkeep.org/pkg/importer"
	imptest "perkeep.org/pkg/importer/test"
	"perkeep.org/pkg/schema"
	"perkeep.org/pkg/schema/nodeattr"
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
	const accessToken = "foo"
	const accessSecret = "bar"
	const userID = "camlistore_test"

	responder := httputil.FileResponder("testdata/user_timeline.json")
	transport, err := httputil.NewRegexpFakeTransport([]*httputil.Matcher{
		{URLRegex: `^https\://api\.twitter\.com/1.1/statuses/user_timeline.json\?`, Fn: responder},
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
