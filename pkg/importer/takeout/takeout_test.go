/*
Copyright 2020 The Perkeep Authors

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

package takeout

import (
	"context"
	"os"
	"testing"
	"time"

	"perkeep.org/internal/httputil"
	"perkeep.org/internal/testhooks"
	"perkeep.org/pkg/importer"
	imptest "perkeep.org/pkg/importer/test"
	"perkeep.org/pkg/schema"
	"perkeep.org/pkg/schema/nodeattr"
)

var ctxbg = context.Background()

func init() {
	testhooks.SetUseSHA1(true)
}

func checkItems(t *testing.T, rc *importer.RunContext, expectedItems map[string]item) {
	itemsNode, err := imptest.GetRequiredChildPathObj(rc.RootNode(), "Google Notes")
	if err != nil {
		t.Fatal(err)
	}

	childRefs, err := imptest.FindChildRefs(itemsNode)
	if err != nil {
		t.Fatal(err)
	}

	if len(childRefs) != len(expectedItems) {
		t.Fatalf("After import, found %d child refs, want %d: %v", len(childRefs), len(expectedItems), childRefs)
	}

	for _, ref := range childRefs {
		childNode, err := rc.Host.ObjectFromRef(ref)
		if err != nil {
			t.Fatal(err)
		}
		title := childNode.Attr("title")
		expectedItem := expectedItems[title]
		expectedContent := expectedItem.Content()
		foundContent := childNode.Attr(nodeattr.Content)
		if foundContent != expectedContent {
			t.Fatalf("Found unexpected child node %v with content %q when we want %q", childNode, foundContent, expectedContent)
		}
		expectedDate := schema.RFC3339FromTime(time.Unix(expectedItem.Timestamp(), 0))
		foundDate := childNode.Attr(nodeattr.StartDate)
		if expectedDate != foundDate {
			t.Fatalf("Found unexpected child node %v with date %q when we want %q", childNode, foundDate, expectedDate)
		}
		delete(expectedItems, title)
	}
	if len(expectedItems) != 0 {
		t.Fatalf("The following entries were expected but not found: %#v", expectedItems)
	}
}

// TestIntegrationRun tests both the twitter API and zip file import paths.
func TestIntegrationRun(t *testing.T) {
	const userID = "perkeep_test"
	const attrKey = "key"
	const attrValue = "value"

	responder := httputil.FileResponder("dummy")
	transport, err := httputil.NewRegexpFakeTransport([]*httputil.Matcher{
		{`^https\://takeout\.google\.com`, responder},
	})
	if err != nil {
		t.Fatal(err)
	}

	imptest.ImporterTest(t, "takeout", transport, func(rc *importer.RunContext) {

		err := rc.AccountNode().SetAttrs(importer.AcctAttrUserID, userID)
		if err != nil {
			t.Fatal(err)
		}

		zipFile, err := os.Open("testdata/perkeep_test.zip")
		if err != nil {
			t.Fatal(err)
		}
		defer zipFile.Close()

		zipRef, err := schema.WriteFileFromReader(ctxbg, rc.Host.Target(), "camlistore_test.zip", zipFile)
		if err != nil {
			t.Fatal(err)
		}
		err = rc.AccountNode().SetAttrs(acctAttrTakeoutZip, zipRef.String())
		if err != nil {
			t.Fatal(err)
		}

		// Now run with the zip.
		testee := imp{}
		if err := testee.Run(rc); err != nil {
			t.Fatal(err)
		}

		li1 := &listItem{Text: "Halt and Catch Fire ", Checked: false}
		li2 := &listItem{Text: "Sillicon Valley", Checked: true}
		li3 := &listItem{Text: "Mr. Robot", Checked: true}

		i1 := &noteItem{NoteTitle: "Test 1", TextContent: "Test 1", EditTimestamp: 1525639240544000}
		i2 := &noteItem{NoteTitle: "", TextContent: "No title ", EditTimestamp: 1525639240544000}
		i3 := &noteItem{NoteTitle: "Series", EditTimestamp: 1563310284270000, ListContent: []*listItem{li1, li2, li3}}

		testItems := map[string]item{
			"Test 1": i1,
			"2":      i2,
			"Series": i3,
		}
		checkItems(t, rc, testItems)
	})
}
