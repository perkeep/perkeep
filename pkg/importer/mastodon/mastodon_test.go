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
	"net/http"
	"testing"

	"perkeep.org/pkg/schema/nodeattr"

	"perkeep.org/internal/httputil"

	"perkeep.org/pkg/importer"
	imptest "perkeep.org/pkg/importer/test"
)

type testPost struct {
	content     string
	spoilerText string
}

var expectedPosts = map[string]testPost{
	"https://example.com/d0264031-3c1b-42dd-87a5-e0c3b75eec70": testPost{
		content: "MULTIPLE<br /><br />LINES<br /><br />OF<br /><br />TEXT",
	},
	"https://example.com/objects/bf8712b8-6268-4a8c-acaf-99966a5cd9eb": testPost{
		content:     "A status with a spoiler text.",
		spoilerText: "I'm the spoiler text.",
	},
}

func TestIntegration(t *testing.T) {
	responder := httputil.FileResponder("testdata/user_statuses.json")
	transport := httputil.NewFakeTransport(map[string]func() *http.Response{
		"https://example.com/api/v1/accounts/1/statuses": responder,
	})

	imptest.ImporterTest(t, "mastodon", transport, func(rc *importer.RunContext) {
		if err := rc.AccountNode().SetAttrs(
			importer.AcctAttrAccessToken, "aaabbb",
			acctAttrClientID, "clientid",
			acctAttrClientSecret, "supersecret",
			acctAttrInstanceURL, "https://example.com",
			importer.AcctAttrUserName, "testuser",
			importer.AcctAttrUserID, "1",
		); err != nil {
			t.Fatal(err)
		}

		imp := imp{}
		if err := imp.Run(rc); err != nil {
			t.Fatal(err)
		}

		statuses, err := imptest.GetRequiredChildPathObj(rc.RootNode(), "statuses")
		if err != nil {
			t.Fatal(err)
		}

		gotTitle := statuses.Attr(nodeattr.Title)
		if gotTitle != "Mastodon statuses for @testuser@example.com" {
			t.Errorf("Statuses node title is \"%s\" (expected \"Mastodon statuses for @testuser@example.com\")", gotTitle)
		}

		childRefs, err := imptest.FindChildRefs(statuses)
		if err != nil {
			t.Fatal(err)
		}

		for _, ref := range childRefs {
			currNode, err := rc.Host.ObjectFromRef(ref)
			if err != nil {
				t.Fatal(err)
			}

			uri := currNode.Attr(attrURI)
			expectedPost, ok := expectedPosts[uri]
			if !ok {
				t.Fatalf("Did not expect status with URI %s", uri)
			}

			spoiler := currNode.Attrs(attrSpoilerText)
			if expectedPost.spoilerText != "" {
				if len(spoiler) == 0 {
					t.Errorf("Expected spoiler text, but none was stored.")
				} else {
					if spoiler[0] != expectedPost.spoilerText {
						t.Errorf("Did not find the expected spoiler text.")
					}

					if len(spoiler) > 1 {
						t.Errorf("Multiple spoiler text entries where only one expected.")
					}
				}
			} else if len(spoiler) != 0 {
				t.Errorf("Expected no spoiler text, but found some anyway")
			}

			content := currNode.Attr(nodeattr.Content)
			if expectedPost.content != content {
				t.Errorf("Did not store the right content")
			}
		}

	})

}
