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
