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

package pinboard

import (
	"testing"

	"camlistore.org/pkg/httputil"
	"camlistore.org/pkg/importer"
	imptest "camlistore.org/pkg/importer/test"
	"camlistore.org/pkg/schema/nodeattr"
)

func verifyUsername(t *testing.T, apiToken string, expected string) {
	extracted := extractUsername(apiToken)
	if extracted != expected {
		t.Errorf("Testing %q: user name is %q when we want %q", apiToken, extracted, expected)
	}
}

func TestExtractUsername(t *testing.T) {
	verifyUsername(t, "gina:foo", "gina")
	verifyUsername(t, "", "")
}

// Verify that a batch import of 3 posts works
func TestIntegrationRun(t *testing.T) {
	const authToken = "gina:foo"
	const attrKey = "key"
	const attrValue = "value"

	responder := httputil.FileResponder("testdata/batchresponse.json")
	transport, err := httputil.NewRegexpFakeTransport([]*httputil.Matcher{
		&httputil.Matcher{`^https\://api\.pinboard\.in/v1/posts/all\?auth_token=gina:foo&format=json&results=10000&todt=\d\d\d\d.*`, responder},
	})
	if err != nil {
		t.Fatal(err)
	}

	imptest.ImporterTest(t, "pinboard", transport, func(rc *importer.RunContext) {
		err = rc.AccountNode().SetAttrs(attrAuthToken, authToken)
		if err != nil {
			t.Fatal(err)
		}

		testee := imp{}
		if err := testee.Run(rc); err != nil {
			t.Fatal(err)
		}

		postsNode, err := imptest.GetRequiredChildPathObj(rc.RootNode(), "posts")
		if err != nil {
			t.Fatal(err)
		}

		childRefs, err := imptest.FindChildRefs(postsNode)
		if err != nil {
			t.Fatal(err)
		}

		expectedPosts := map[string]string{
			`https://wiki.archlinux.org/index.php/xorg#Display_size_and_DPI`:                   "Xorg - ArchWiki",
			`http://www.harihareswara.net/sumana/2014/08/17/0`:                                 "One Way Confidence Will Look",
			`http://www.wikiart.org/en/marcus-larson/fishing-near-the-fjord-by-moonlight-1862`: "Fishing Near The Fjord By Moonlight - Marcus Larson - WikiArt.org",
		}

		if len(childRefs) != len(expectedPosts) {
			t.Fatalf("After import, found %d child refs, want %d: %v", len(childRefs), len(expectedPosts), childRefs)
		}

		for _, ref := range childRefs {
			childNode, err := rc.Host.ObjectFromRef(ref)
			if err != nil {
				t.Fatal(err)
			}
			foundURL := childNode.Attr(nodeattr.URL)
			expectedTitle, ok := expectedPosts[foundURL]
			if !ok {
				t.Fatalf("Found unexpected child node %v with url %q", childNode, foundURL)
			}
			foundTitle := childNode.Attr(nodeattr.Title)
			if foundTitle != expectedTitle {
				t.Fatalf("Found unexpected child node %v with title %q when we want %q", childNode, foundTitle, expectedTitle)
			}
			delete(expectedPosts, foundURL)
		}
		if len(expectedPosts) != 0 {
			t.Fatalf("The following entries were expected but not found: %#v", expectedPosts)
		}
	})
}
