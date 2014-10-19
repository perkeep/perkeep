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

	addPermanode(fi, "fourcheckin-0",
		"camliNodeType", "foursquare.com:checkin",
		"foursquareVenuePermanode", "fourvenue-123",
	)
	addPermanode(fi, "fourvenue-123",
		"camliNodeType", "foursquare.com:venue",
		"camliPath:photos", "venuepicset-123",
	)
	addPermanode(fi, "venuepicset-123",
		"camliPath:1.jpg", "venuepic-1",
	)
	addPermanode(fi, "venuepic-1",
		"camliContent", "somevenuepic-0",
	)
	addPermanode(fi, "somevenuepic-0",
		"foo", "bar",
	)
	addPermanode(fi, "venuepic-2",
		"camliContent", "somevenuepic-2",
	)
	addPermanode(fi, "somevenuepic-2",
		"foo", "baz",
	)

	addPermanode(fi, "homedir-0",
		"camliPath:subdir.1", "homedir-1",
	)
	addPermanode(fi, "homedir-1",
		"camliPath:subdir.2", "homedir-2",
	)
	addPermanode(fi, "homedir-2",
		"foo", "bar",
	)

	addPermanode(fi, "set-0",
		"camliMember", "venuepic-1",
		"camliMember", "venuepic-2",
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

	{
		name: "foursquare venue photos, but not recursive camliPath explosion",
		postBody: marshalJSON(&search.DescribeRequest{
			BlobRefs: []blob.Ref{
				blob.MustParse("homedir-0"),
				blob.MustParse("fourcheckin-0"),
			},
			Rules: []*search.DescribeRule{
				{
					Attrs: []string{"camliContent", "camliContentImage"},
				},
				{
					IfCamliNodeType: "foursquare.com:checkin",
					Attrs:           []string{"foursquareVenuePermanode"},
				},
				{
					IfCamliNodeType: "foursquare.com:venue",
					Attrs:           []string{"camliPath:photos"},
					Rules: []*search.DescribeRule{
						{
							Attrs: []string{"camliPath:*"},
						},
					},
				},
			},
		}),
		wantDescribed: []string{"homedir-0", "fourcheckin-0", "fourvenue-123", "venuepicset-123", "venuepic-1", "somevenuepic-0"},
	},

	{
		name: "home dirs forever",
		postBody: marshalJSON(&search.DescribeRequest{
			BlobRefs: []blob.Ref{
				blob.MustParse("homedir-0"),
			},
			Rules: []*search.DescribeRule{
				{
					Attrs: []string{"camliPath:*"},
				},
			},
		}),
		wantDescribed: []string{"homedir-0", "homedir-1", "homedir-2"},
	},

	{
		name: "find members",
		postBody: marshalJSON(&search.DescribeRequest{
			BlobRef: blob.MustParse("set-0"),
			Rules: []*search.DescribeRule{
				{
					IfResultRoot: true,
					Attrs:        []string{"camliMember"},
					Rules: []*search.DescribeRule{
						{Attrs: []string{"camliContent"}},
					},
				},
			},
		}),
		wantDescribed: []string{"set-0", "venuepic-1", "venuepic-2", "somevenuepic-0", "somevenuepic-2"},
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
