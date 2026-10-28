/*
Copyright 2026 The Perkeep Authors

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
	"os"
	"testing"
	"time"

	"perkeep.org/pkg/blob"
	. "perkeep.org/pkg/search"
)

func TestQueryMedia(t *testing.T) {
	audio, err := os.ReadFile("../index/indextest/testdata/0s.mp3")
	if err != nil {
		t.Fatal(err)
	}
	testQueryTypes(t, memIndexTypes, func(qt *queryTest) {
		id := qt.id
		track, _ := id.UploadFile("track.mp3", string(audio), time.Time{})
		// A second schema for the same contents must share the media tags.
		copy, _ := id.UploadFile("copy.mp3", string(audio), time.Time{})
		plain, _ := id.UploadFile("Test Artist.txt", "Test Album", time.Time{})
		nodes := make(map[blob.Ref]blob.Ref)
		for _, file := range []blob.Ref{track, copy, plain} {
			pn := id.NewDeterministicPermanodeForTest(file.String())
			id.SetAttribute(pn, "camliContent", file.String())
			nodes[file] = pn
		}
		for _, tt := range []struct {
			expression string
			files      []blob.Ref
		}{
			{`media:"Zero Seconds"`, []blob.Ref{track, copy}},
			{`media:ARTIST`, []blob.Ref{track, copy}},
			{`media:album`, []blob.Ref{track, copy}},
			{`media:1992`, []blob.Ref{track, copy}},
			{`media:""`, []blob.Ref{track, copy}},
			{`media:unknown`, nil},
			{`media:title`, nil}, // Only values, not tag names.
			{`media:track.mp3`, nil},
			{`media:"Seconds Test"`, nil}, // Do not concatenate tag values.
			{`media:artist media:album`, []blob.Ref{track, copy}},
			{`media:artist -media:unknown`, []blob.Ref{track, copy}},
			{`media:unknown or media:album`, []blob.Ref{track, copy}},
			{`-media:artist`, []blob.Ref{plain}},
		} {
			t.Logf("query: %s", tt.expression)
			var want []blob.Ref
			for _, file := range tt.files {
				want = append(want, nodes[file])
			}
			qt.wantRes(&SearchQuery{Expression: tt.expression}, want...)
		}
		// The JSON API retains named-tag behavior, while empty Tag searches
		// any value using the normal StringConstraint semantics.
		for _, tt := range []struct {
			mt    *MediaTagConstraint
			files []blob.Ref
		}{
			{&MediaTagConstraint{String: &StringConstraint{Contains: "Artist"}}, []blob.Ref{track, copy}},
			{&MediaTagConstraint{String: &StringConstraint{Contains: "artist"}}, nil},
			{&MediaTagConstraint{String: &StringConstraint{Regexp: "^Test (Artist|Album)$"}}, []blob.Ref{track, copy}},
			{&MediaTagConstraint{String: &StringConstraint{HasPrefix: "Zero", HasSuffix: "Album"}}, nil},
			{&MediaTagConstraint{Tag: "title", String: &StringConstraint{Equals: "Zero Seconds"}}, []blob.Ref{track, copy}},
			{&MediaTagConstraint{Tag: "artist", String: &StringConstraint{Contains: "Album"}}, nil},
			{&MediaTagConstraint{Tag: "missing", String: &StringConstraint{Contains: "Artist"}}, nil},
			{&MediaTagConstraint{Tag: "missing", String: &StringConstraint{Empty: true}}, []blob.Ref{track, copy, plain}},
			{&MediaTagConstraint{Tag: "year", Int: &IntConstraint{Min: 1992, Max: 1992}}, []blob.Ref{track, copy}},
			{&MediaTagConstraint{Tag: "year", Int: &IntConstraint{Min: 2000}}, nil},
			{&MediaTagConstraint{Tag: "year", Int: &IntConstraint{Min: 1992}, String: &StringConstraint{Equals: "1992"}}, []blob.Ref{track, copy}},
			{&MediaTagConstraint{Tag: "year", Int: &IntConstraint{Min: 1992}, String: &StringConstraint{Equals: "1993"}}, nil},
			{&MediaTagConstraint{Int: &IntConstraint{Min: 1992}}, nil},
		} {
			t.Logf("media tag constraint: %+v", tt.mt)
			qt.wantRes(&SearchQuery{Constraint: &Constraint{File: &FileConstraint{MediaTag: tt.mt}}}, tt.files...)
		}
	})
}
