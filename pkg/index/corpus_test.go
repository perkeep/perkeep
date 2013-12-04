/*
Copyright 2013 The Camlistore Authors

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

package index

import (
	"reflect"
	"testing"
	"time"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/types"
	"camlistore.org/pkg/types/camtypes"
)

func TestCorpusAppendPermanodeAttrValues(t *testing.T) {
	c := newCorpus()
	pn := blob.MustParse("abc-123")
	tm := time.Unix(99, 0)
	claim := func(verb, attr, val string) camtypes.Claim {
		tm = tm.Add(time.Second)
		return camtypes.Claim{
			Type:  verb + "-attribute",
			Attr:  attr,
			Value: val,
			Date:  tm,
		}
	}
	s := func(s ...string) []string { return s }

	c.permanodes[pn] = &PermanodeMeta{
		Claims: []camtypes.Claim{
			claim("set", "foo", "foov"), // time 100

			claim("add", "tag", "a"), // time 101
			claim("add", "tag", "b"), // time 102
			claim("del", "tag", ""),
			claim("add", "tag", "c"),
			claim("add", "tag", "d"),
			claim("add", "tag", "e"),
			claim("del", "tag", "d"),

			claim("add", "DelAll", "a"),
			claim("add", "DelAll", "b"),
			claim("add", "DelAll", "c"),
			claim("del", "DelAll", ""),

			claim("add", "DelOne", "a"),
			claim("add", "DelOne", "b"),
			claim("add", "DelOne", "c"),
			claim("add", "DelOne", "d"),
			claim("del", "DelOne", "d"),
			claim("del", "DelOne", "a"),

			claim("add", "SetAfterAdd", "a"),
			claim("add", "SetAfterAdd", "b"),
			claim("set", "SetAfterAdd", "setv"),
		},
	}

	tests := []struct {
		attr string
		want []string
		t    time.Time
	}{
		{attr: "not-exist", want: s()},
		{attr: "DelAll", want: s()},
		{attr: "DelOne", want: s("b", "c")},
		{attr: "foo", want: s("foov")},
		{attr: "tag", want: s("c", "e")},
		{attr: "tag", want: s("a", "b"), t: time.Unix(102, 0)},
		{attr: "SetAfterAdd", want: s("setv")},
	}
	for i, tt := range tests {
		got := c.AppendPermanodeAttrValues(nil, pn, tt.attr, tt.t, blob.Ref{})
		if len(got) == 0 && len(tt.want) == 0 {
			continue
		}
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("%d. attr %q = %q; want %q",
				i, tt.attr, got, tt.want)
		}
	}

}

func TestKVClaimAllocs(t *testing.T) {
	n := testing.AllocsPerRun(20, func() {
		kvClaim("claim|sha1-b380b3080f9c71faa5c1d82bbd4d583a473bc77d|2931A67C26F5ABDA|2011-11-28T01:32:37.000123456Z|sha1-b3d93daee62e40d36237ff444022f42d7d0e43f2",
			"set-attribute|tag|foo1|sha1-ad87ca5c78bd0ce1195c46f7c98e6025abbaf007",
			blob.Parse)
	})
	t.Logf("%v allocations", n)
}

func TestKVClaim(t *testing.T) {
	tests := []struct {
		k, v string
		ok   bool
		want camtypes.Claim
	}{
		{
			k:  "claim|sha1-b380b3080f9c71faa5c1d82bbd4d583a473bc77d|2931A67C26F5ABDA|2011-11-28T01:32:37.000123456Z|sha1-b3d93daee62e40d36237ff444022f42d7d0e43f2",
			v:  "set-attribute|tag|foo1|sha1-ad87ca5c78bd0ce1195c46f7c98e6025abbaf007",
			ok: true,
			want: camtypes.Claim{
				BlobRef:   blob.MustParse("sha1-b3d93daee62e40d36237ff444022f42d7d0e43f2"),
				Signer:    blob.MustParse("sha1-ad87ca5c78bd0ce1195c46f7c98e6025abbaf007"),
				Permanode: blob.MustParse("sha1-b380b3080f9c71faa5c1d82bbd4d583a473bc77d"),
				Type:      "set-attribute",
				Attr:      "tag",
				Value:     "foo1",
				Date:      time.Time(types.ParseTime3339OrZero("2011-11-28T01:32:37.000123456Z")),
			},
		},
	}
	for _, tt := range tests {
		got, ok := kvClaim(tt.k, tt.v, blob.Parse)
		if ok != tt.ok {
			t.Errorf("kvClaim(%q, %q) = ok %v; want %v", tt.k, tt.v, ok, tt.ok)
			continue
		}
		if got != tt.want {
			t.Errorf("kvClaim(%q, %q) = %+v; want %+v", tt.k, tt.v, got, tt.want)
			continue
		}
	}
}
