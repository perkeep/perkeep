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

package serverinit_test

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"perkeep.org/pkg/blob"
)

// TestUploadIsIndexed checks that with a generated config, a schema blob
// uploaded to the blob root is indexed by the time the upload returns,
// both for multipart and PUT uploads.
func TestUploadIsIndexed(t *testing.T) {
	mux, _ := newTestServer(t)
	do := func(req *http.Request) *httptest.ResponseRecorder {
		t.Helper()
		req.SetBasicAuth("foo", "bar")
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code >= 300 {
			t.Fatalf("%s %s = %d: %s", req.Method, req.URL, rec.Code, rec.Body)
		}
		return rec
	}
	checkIndexed := func(br blob.Ref) {
		t.Helper()
		rec := do(httptest.NewRequest("GET", "/my-search/camli/search/describe?blobref="+br.String(), nil))
		var res struct {
			Meta map[string]struct {
				CamliType string `json:"camliType"`
			} `json:"meta"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
			t.Fatal(err)
		}
		if got := res.Meta[br.String()].CamliType; got != "static-set" {
			t.Errorf("described camliType of %v = %q; want static-set, as it should be indexed: %s", br, got, rec.Body)
		}
	}

	multi := `{"camliVersion": 1, "camliType": "static-set", "members": [], "via": "multipart"}`
	br := blob.RefFromString(multi)
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	w, err := mw.CreateFormFile(br.String(), br.String())
	if err != nil {
		t.Fatal(err)
	}
	w.Write([]byte(multi))
	mw.Close()
	req := httptest.NewRequest("POST", "/bs/camli/upload", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	do(req)
	checkIndexed(br)

	put := `{"camliVersion": 1, "camliType": "static-set", "members": [], "via": "PUT"}`
	br = blob.RefFromString(put)
	do(httptest.NewRequest("PUT", "/bs/camli/"+br.String(), strings.NewReader(put)))
	checkIndexed(br)
}
