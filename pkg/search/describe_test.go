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

package search_test

import (
	"testing"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/index"
	"camlistore.org/pkg/search"
	"camlistore.org/pkg/test"
)

func addPermanode(fi *test.FakeIndex, pnStr string, attrs ...string) {
	pn := blob.MustParse(pnStr)
	fi.AddMeta(pn, "permanode", 123)
	for len(attrs) > 0 {
		k, v := attrs[0], attrs[1]
		attrs = attrs[2:]
		fi.AddClaim(owner, pn, "add-attribute", k, v)
	}
}

func searchDescribeSetup(fi *test.FakeIndex) index.Interface {
	addPermanode(fi, "abc-123",
		"camliContent", "abc-123c",
		"camliImageContent", "abc-888",
	)
	addPermanode(fi, "abc-123c",
		"camliContent", "abc-123cc",
		"camliImageContent", "abc-123c1",
	)
	addPermanode(fi, "abc-123c1",
		"some", "image",
	)
	addPermanode(fi, "abc-123cc",
		"name", "leaf",
	)
	addPermanode(fi, "abc-888",
		"camliContent", "abc-8881",
	)
	addPermanode(fi, "abc-8881",
		"name", "leaf8881",
	)
	return fi
}

var searchDescribeTests = []handlerTest{
	{
		name:     "null",
		postBody: marshalJSON(&search.DescribeRequest{}),
		want: jmap(&search.DescribeResponse{
			Meta: search.MetaMap{},
		}),
	},

	{
		name: "single",
		postBody: marshalJSON(&search.DescribeRequest{
			BlobRef: blob.MustParse("abc-123"),
		}),
		wantDescribed: []string{"abc-123"},
	},

	{
		name: "follow all camliContent",
		postBody: marshalJSON(&search.DescribeRequest{
			BlobRef: blob.MustParse("abc-123"),
			Rules: []*search.DescribeRule{
				{
					Attrs: []string{"camliContent"},
				},
			},
		}),
		wantDescribed: []string{"abc-123", "abc-123c", "abc-123cc"},
	},

	{
		name: "follow only root camliContent",
		postBody: marshalJSON(&search.DescribeRequest{
			BlobRef: blob.MustParse("abc-123"),
			Rules: []*search.DescribeRule{
				{
					IfResultRoot: true,
					Attrs:        []string{"camliContent"},
				},
			},
		}),
		wantDescribed: []string{"abc-123", "abc-123c"},
	},

	{
		name: "follow all root, substring",
		postBody: marshalJSON(&search.DescribeRequest{
			BlobRef: blob.MustParse("abc-123"),
			Rules: []*search.DescribeRule{
				{
					IfResultRoot: true,
					Attrs:        []string{"camli*"},
				},
			},
		}),
		wantDescribed: []string{"abc-123", "abc-123c", "abc-888"},
	},

	{
		name: "two rules, two attrs",
		postBody: marshalJSON(&search.DescribeRequest{
			BlobRef: blob.MustParse("abc-123"),
			Rules: []*search.DescribeRule{
				{
					IfResultRoot: true,
					Attrs:        []string{"camliContent", "camliImageContent"},
				},
				{
					Attrs: []string{"camliContent"},
				},
			},
		}),
		wantDescribed: []string{"abc-123", "abc-123c", "abc-123cc", "abc-888", "abc-8881"},
	},
}

func init() {
	checkNoDups("searchDescribeTests", searchDescribeTests)
}

func TestSearchDescribe(t *testing.T) {
	for _, ht := range searchDescribeTests {
		if ht.setup == nil {
			ht.setup = searchDescribeSetup
		}
		if ht.query == "" {
			ht.query = "describe"
		}
		ht.test(t)
	}
}
