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
	"io/ioutil"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

// Note: a few of the fields are here are before the protocol change
// (camlistore.org/issue/123) but preserved to make sure we don't
// choke on them.
var response = `{
   "stat": [
      {"blobRef": "foo-abcd",
       "size": 123},
      {"blobRef": "foo-cdef",
       "size": 999}
   ],
   "maxUploadSize": 1048576,
   "uploadUrl": "http://upload-server.example.com/some/server-chosen/url",
   "uploadUrlExpirationSeconds": 7200,
   "canLongPoll": true
}
`

func TestParseStatResponse(t *testing.T) {
	res, err := parseStatResponse(&http.Response{
		Body: ioutil.NopCloser(strings.NewReader(response)),
	})
	if err != nil {
		t.Fatal(err)
	}
	hm := res.HaveMap
	res.HaveMap = nil
	want := &statResponse{
		HaveMap:     nil,
		canLongPoll: true,
	}
	if !reflect.DeepEqual(want, res) {
		t.Errorf(" Got: %#v\nWant: %#v", res, want)
	}

	if sb, ok := hm["foo-abcd"]; !ok || sb.Size != 123 {
		t.Errorf("Got unexpected map: %#v", hm)
	}

	if sb, ok := hm["foo-cdef"]; !ok || sb.Size != 999 {
		t.Errorf("Got unexpected map: %#v", hm)
	}
}
