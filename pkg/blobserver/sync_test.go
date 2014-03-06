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

package blobserver

import (
	"strings"
	"testing"

	"camlistore.org/pkg/blob"
)

type lmdbTest struct {
	src, dst          string // comma-separated list of blobrefs for two sides
	missing, mismatch string // comma-separated list of wanted results
	diffSize          bool   // whether dest should report different sized blobs
}

func (lt *lmdbTest) run(t *testing.T) {
	srcBlobs := make(chan blob.SizedRef, 100)
	destBlobs := make(chan blob.SizedRef, 100)
	sendTestBlobs(srcBlobs, lt.src, 123)
	destSize := uint32(123)
	if lt.diffSize {
		destSize = 567
	}
	sendTestBlobs(destBlobs, lt.dst, destSize)

	missingc := make(chan blob.SizedRef)
	var missing, mismatch []string
	onMismatch := func(br blob.Ref) {
		mismatch = append(mismatch, br.String())
	}
	go ListMissingDestinationBlobs(missingc, onMismatch, srcBlobs, destBlobs)
	for sb := range missingc {
		missing = append(missing, sb.Ref.String())
	}
	if got := strings.Join(missing, ","); got != lt.missing {
		t.Errorf("For src %q and dest %q got missing %q; want %q",
			lt.src, lt.dst, got, lt.missing)
	}
	if got := strings.Join(mismatch, ","); got != lt.mismatch {
		t.Errorf("For src %q and dest %q got mismatched %q; want %q",
			lt.src, lt.dst, got, lt.mismatch)
	}
}

func sendTestBlobs(ch chan blob.SizedRef, list string, size uint32) {
	defer close(ch)
	if list == "" {
		return
	}
	for _, br := range strings.Split(list, ",") {
		ch <- blob.SizedRef{blob.MustParse(br), size}
	}
}

func TestListMissingDestinationBlobs(t *testing.T) {
	tests := []lmdbTest{
		{src: "foo-aa,foo-bb,foo-cc", missing: "foo-aa,foo-bb,foo-cc"},
		{src: "foo-aa,foo-bb,foo-cc", dst: "foo-aa", missing: "foo-bb,foo-cc"},
		{src: "foo-aa,foo-bb,foo-cc", dst: "foo-bb", missing: "foo-aa,foo-cc"},
		{src: "foo-aa,foo-bb,foo-cc", dst: "foo-cc", missing: "foo-aa,foo-bb"},
		{src: "foo-aa,foo-bb,foo-cc", dst: "foo-aa,foo-bb", missing: "foo-cc"},
		{src: "foo-aa,foo-bb,foo-cc", dst: "foo-bb,foo-cc", missing: "foo-aa"},
		{src: "foo-aa,foo-bb,foo-cc", dst: "foo-aa,foo-bb,foo-cc", missing: ""},
		{src: "", dst: "foo-aa,foo-bb,foo-cc", missing: ""},
		{src: "foo-ff", dst: "foo-aa,foo-bb,foo-cc", missing: "foo-ff"},

		{
			src:      "foo-aa,foo-bb",
			dst:      "foo-aa,foo-cc",
			mismatch: "foo-aa",
			missing:  "foo-bb",
			diffSize: true,
		},
	}

	for _, test := range tests {
		test.run(t)
	}
}
