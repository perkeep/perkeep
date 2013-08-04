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

package client

import (
	"camlistore.org/pkg/blob"
	"strings"
	"testing"
)

type lmdbTest struct {
	source, dest, expectedMissing string // comma-separated list of blobref strings
}

func (lt *lmdbTest) run(t *testing.T) {
	srcBlobs := make(chan blob.SizedRef, 100)
	destBlobs := make(chan blob.SizedRef, 100)
	sendTestBlobs(srcBlobs, lt.source)
	sendTestBlobs(destBlobs, lt.dest)

	missing := make(chan blob.SizedRef)
	got := make([]string, 0)
	go ListMissingDestinationBlobs(missing, nil, srcBlobs, destBlobs)
	for sb := range missing {
		got = append(got, sb.Ref.String())
	}
	gotJoined := strings.Join(got, ",")
	if gotJoined != lt.expectedMissing {
		t.Errorf("For %q and %q expected %q, got %q",
			lt.source, lt.dest, lt.expectedMissing, gotJoined)
	}
}

func sendTestBlobs(ch chan blob.SizedRef, list string) {
	defer close(ch)
	if list == "" {
		return
	}
	for _, b := range strings.Split(list, ",") {
		br := blob.MustParse(b)
		ch <- blob.SizedRef{Ref: br, Size: 123}
	}
}

func TestListMissingDestinationBlobs(t *testing.T) {
	tests := []lmdbTest{
		{"foo-aa,foo-bb,foo-cc", "", "foo-aa,foo-bb,foo-cc"},
		{"foo-aa,foo-bb,foo-cc", "foo-aa", "foo-bb,foo-cc"},
		{"foo-aa,foo-bb,foo-cc", "foo-bb", "foo-aa,foo-cc"},
		{"foo-aa,foo-bb,foo-cc", "foo-cc", "foo-aa,foo-bb"},
		{"foo-aa,foo-bb,foo-cc", "foo-aa,foo-bb", "foo-cc"},
		{"foo-aa,foo-bb,foo-cc", "foo-bb,foo-cc", "foo-aa"},
		{"foo-aa,foo-bb,foo-cc", "foo-aa,foo-bb,foo-cc", ""},
		{"", "foo-aa,foo-bb,foo-cc", ""},
		{"foo-ff", "foo-aa,foo-bb,foo-cc", "foo-ff"},
	}

	for _, test := range tests {
		test.run(t)
	}
}
