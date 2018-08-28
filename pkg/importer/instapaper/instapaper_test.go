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

package instapaper

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"perkeep.org/internal/httputil"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/importer"
	imptest "perkeep.org/pkg/importer/test"
	"perkeep.org/pkg/schema/nodeattr"
)

func verify(t *testing.T, root *importer.Object, path string, expected map[string]string) []blob.Ref {
	parent, err := imptest.GetRequiredChildPathObj(root, path)
	if err != nil {
		t.Fatal(err)
	}
	refs, err := imptest.FindChildRefs(parent)
	if err != nil {
		t.Fatal(err)
	}

	if len(refs) != len(expected) {
		t.Fatalf("After import, found %d child refs on %v, want %d: %v", len(refs), path, len(expected), refs)
	}

	return refs
}

func TestParseFilename(t *testing.T) {
	filename := parseFilename("Title should have slashes / replaced with a dash", "123")
	want := "Title should have slashes - replaced with a dash_123.html"
	if filename != want {
		t.Fatalf("Got %v but expected %v", filename, want)
	}
}

func TestIntegrationRun(t *testing.T) {
	const authToken = "token"
	const authSecret = "secret"
	const userID = "999"

	bmListResponder := httputil.FileResponder("testdata/bookmarks_list_response.json")
	foldersListResponder := httputil.FileResponder("testdata/folders_list_response.json")
	bm1TxtResponder := httputil.FileResponder("testdata/bookmark_text_response.txt")
	hl1Responder := httputil.FileResponder("testdata/highlights_list_response.json")

	transport := httputil.NewFakeTransport(map[string]func() *http.Response{
		foldersListRequestURL:                   foldersListResponder,
		bookmarkListRequestURL:                  bmListResponder,
		bookmarkTextRequestURL:                  bm1TxtResponder,
		fmt.Sprintf(highlightListRequestURL, 1): hl1Responder,
	})

	imptest.ImporterTest(t, "instapaper", transport, func(rc *importer.RunContext) {
		err := rc.AccountNode().SetAttrs(
			importer.AcctAttrAccessToken, authToken,
			importer.AcctAttrAccessTokenSecret, authSecret,
			importer.AcctAttrUserID, userID,
		)
		if err != nil {
			t.Fatal(err)
		}

		parent, err := rc.RootNode().ChildPathObject("bookmarks")
		if err != nil {
			t.Fatal(err)
		}
		existing, err := parent.ChildPathObject("Existing Title_1.html")
		if err != nil {
			t.Fatal(err)
		}
		if err = existing.SetAttrs(attrBookmarkId, "1", nodeattr.Title, "Existing Title"); err != nil {
			t.Fatal(err)
		}

		testee := imp{}
		if err := testee.Run(rc); err != nil {
			t.Fatal(err)
		}

		expectedBookmarks := map[string]string{
			`https://www.example.org/title-1`: "Title 1",
		}

		expectedHighlights := map[string]string{
			"Highlighted - 1": "1",
			"Highlighted - 2": "1",
		}

		bmRefs := verify(t, rc.RootNode(), "bookmarks", expectedBookmarks)
		hlRefs := verify(t, rc.RootNode(), "highlights", expectedHighlights)

		// Verify imported Bookmark attributes
		for _, ref := range bmRefs {
			childNode, err := rc.Host.ObjectFromRef(ref)
			if err != nil {
				t.Fatal(err)
			}
			foundURL := childNode.Attr(nodeattr.URL)
			expectedTitle, ok := expectedBookmarks[foundURL]
			if !ok {
				t.Fatalf("Found unexpected child node %v with url %q", childNode, foundURL)
			}
			foundTitle := childNode.Attr(nodeattr.Title)
			if foundTitle != expectedTitle {
				t.Fatalf("Found unexpected child node %v with title %q when we want %q", childNode, foundTitle, expectedTitle)
			}
			camliContent := childNode.Attr("camliContent")
			if !strings.HasPrefix(camliContent, "sha") {
				t.Fatalf("Expected child node %v to have camliContent ref", childNode)
			}
			delete(expectedBookmarks, foundURL)
		}
		if len(expectedBookmarks) != 0 {
			t.Fatalf("The following entries were expected but not found: %#v", expectedBookmarks)
		}

		// Verify imported Highlight attributes
		for _, ref := range hlRefs {
			childNode, err := rc.Host.ObjectFromRef(ref)
			if err != nil {
				t.Fatal(err)
			}
			foundContent := childNode.Attr(nodeattr.Content)
			expectedBmId, ok := expectedHighlights[foundContent]
			if !ok {
				t.Fatalf("Found unexpected child node %v with content %q", childNode, foundContent)
			}
			foundBmId := childNode.Attr(attrBookmarkId)
			if foundBmId != expectedBmId {
				t.Fatalf("Found unexpected child node %v with bookmark Id %q when we want %q", childNode, foundBmId, expectedBmId)
			}
			delete(expectedHighlights, foundContent)
		}
		if len(expectedHighlights) != 0 {
			t.Fatalf("The following entries were expected but not found: %#v", expectedHighlights)
		}
	})
}
