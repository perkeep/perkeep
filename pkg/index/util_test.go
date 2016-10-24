/*
Copyright 2016 The Camlistore AUTHORS

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

package index_test

import (
	"testing"
	"time"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/index"
	"camlistore.org/pkg/types/camtypes"
)

func TestClaimsAttrValue(t *testing.T) {
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

	claims := []camtypes.Claim{
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
	}

	tests := []struct {
		attr string
		want string
		t    time.Time
	}{
		{attr: "foo", want: "foov"},
		{attr: "tag", want: "c"},
		{attr: "tag", want: "a", t: time.Unix(102, 0)},
		{attr: "DelAll", want: ""},
		{attr: "DelOne", want: "b"},
		{attr: "SetAfterAdd", want: "setv"},
	}

	for i, tt := range tests {
		got := index.ClaimsAttrValue(claims, tt.attr, tt.t, blob.Ref{})
		if got != tt.want {
			t.Errorf("%d. attr %q = %v; want %v",
				i, tt.attr, got, tt.want)
		}
	}
}
