/*
Copyright 2011 Google Inc.

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

package search_test

import (
	. "camlistore.org/pkg/search"

	"bytes"
	"encoding/json"
	"testing"

	"camlistore.org/pkg/blobref"
	"camlistore.org/pkg/test"
)

type describeTest struct {
	setup func(fi *test.FakeIndex)

	blob  string // blobref to describe
	depth int

	expect map[string]interface{}
}

var owner = blobref.MustParse("abcown-123")

var describeTests = []describeTest{
	{
		func(fi *test.FakeIndex) {},
		"abc-555",
		1,
		map[string]interface{}{},
	},

	{
		func(fi *test.FakeIndex) {
			fi.AddMeta(blobref.MustParse("abc-555"), "image/jpeg", 999)
		},
		"abc-555",
		1,
		map[string]interface{}{
			"abc-555": map[string]interface{}{
				"blobRef":  "abc-555",
				"mimeType": "image/jpeg",
				"size":     999,
			},
		},
	},

	{
		func(fi *test.FakeIndex) {
			pn := blobref.MustParse("perma-123")
			fi.AddMeta(pn, "application/json; camliType=permanode", 123)
			fi.AddClaim(owner, pn, "set-attribute", "camliContent", "foo-232")
			fi.AddMeta(blobref.MustParse("foo-232"), "foo/bar", 878)

			// Test deleting all attributes
			fi.AddClaim(owner, pn, "add-attribute", "wont-be-present", "x")
			fi.AddClaim(owner, pn, "add-attribute", "wont-be-present", "y")
			fi.AddClaim(owner, pn, "del-attribute", "wont-be-present", "")

			// Test deleting a specific attribute.
			fi.AddClaim(owner, pn, "add-attribute", "only-delete-b", "a")
			fi.AddClaim(owner, pn, "add-attribute", "only-delete-b", "b")
			fi.AddClaim(owner, pn, "add-attribute", "only-delete-b", "c")
			fi.AddClaim(owner, pn, "del-attribute", "only-delete-b", "b")
		},
		"perma-123",
		2,
		map[string]interface{}{
			"foo-232": map[string]interface{}{
				"blobRef":  "foo-232",
				"mimeType": "foo/bar",
				"size":     878,
			},
			"perma-123": map[string]interface{}{
				"blobRef":   "perma-123",
				"mimeType":  "application/json; camliType=permanode",
				"camliType": "permanode",
				"size":      123,
				"permanode": map[string]interface{}{
					"attr": map[string]interface{}{
						"camliContent":  []string{"foo-232"},
						"only-delete-b": []string{"a", "c"},
					},
				},
			},
		},
	},
}

func TestDescribe(t *testing.T) {
	for testn, tt := range describeTests {
		idx := test.NewFakeIndex()
		tt.setup(idx)

		h := NewHandler(idx, owner)
		js := make(map[string]interface{})
		dr := h.NewDescribeRequest()
		dr.Describe(blobref.MustParse(tt.blob), tt.depth)
		dr.PopulateJSON(js)
		got, _ := json.MarshalIndent(js, "", "  ")
		want, _ := json.MarshalIndent(tt.expect, "", "  ")
		if !bytes.Equal(got, want) {
			t.Errorf("test %d:\nwant: %s\n got: %s", testn, string(want), string(got))
		}
	}
}
