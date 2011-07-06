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

package main

import (
	"http"
	"http/httptest"
	"testing"

	"camli/blobref"
	"camli/search"
	"camli/test"
)

func TestPublishURLs(t *testing.T) {
	fakeIndex := test.NewFakeIndex()
	owner := blobref.MustParse("owner-123")
	sh := search.NewHandler(fakeIndex, owner)
	ph := &PublishHandler{
		RootName: "foo",
		Search:   sh,
	}
	rw := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "http://foo.com/pics/singlepic", nil)
	req.Header.Set("X-PrefixHandler-PathBase", "/pics/")
	req.Header.Set("X-PrefixHandler-PathSuffix", "singlepic")
	pr := ph.NewRequest(rw, req)
	t.Logf("Got request: %#v", *pr)
}
