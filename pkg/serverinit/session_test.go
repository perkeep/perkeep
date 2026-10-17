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
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver/protocol"
	"perkeep.org/pkg/types/camtypes"
)

// TestUploadSessions checks that the default config serves upload
// sessions at its blob root, refusing to open them while read-only, and
// that blobs uploaded in one are visible once flushed.
func TestUploadSessions(t *testing.T) {
	mux, mon := newTestServer(t)
	do := func(method, path, contentType string, body io.Reader) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, body)
		req.SetBasicAuth("foo", "bar")
		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}
	decode := func(rec *httptest.ResponseRecorder, v any) {
		t.Helper()
		if rec.Code != http.StatusOK {
			t.Fatalf("status %d: %s", rec.Code, rec.Body)
		}
		if err := json.Unmarshal(rec.Body.Bytes(), v); err != nil {
			t.Fatalf("decoding %q: %v", rec.Body, err)
		}
	}

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Accept", "text/x-camli-configuration")
	req.SetBasicAuth("foo", "bar")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	var disco camtypes.Discovery
	decode(rec, &disco)
	if !disco.UploadSessions || disco.BlobRoot == "" {
		t.Fatalf("discovery has uploadSessions %v, blobRoot %q", disco.UploadSessions, disco.BlobRoot)
	}
	root := disco.BlobRoot

	check := mon.Register("test.fatal[test]", "test check")
	check.Fatal(errors.New("something is very wrong"))
	if rec := do("POST", root+"camli/session", "", nil); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("open while read-only = %d: %s; want %d", rec.Code, rec.Body, http.StatusServiceUnavailable)
	}
	check.OK()

	var opened protocol.SessionOpenResponse
	decode(do("POST", root+"camli/session", "", nil), &opened)

	const contents = "some blob"
	br := blob.RefFromString(contents)
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	w, err := mw.CreateFormFile(br.String(), br.String())
	if err != nil {
		t.Fatal(err)
	}
	io.WriteString(w, contents)
	mw.Close()
	var up protocol.UploadResponse
	decode(do("POST", root+"camli/upload?session="+opened.Session, mw.FormDataContentType(), &body), &up)
	if len(up.Accepted) != 1 || up.Accepted[0].Ref != br || up.ErrorText != "" {
		t.Fatalf("upload response = %+v", up)
	}

	// The session can be flushed while read-only, to learn what's durable.
	check.Fatal(errors.New("something is very wrong"))
	var st protocol.SessionStatusResponse
	decode(do("POST", root+"camli/session/"+opened.Session+"/flush", "", nil), &st)
	check.OK()
	if len(st.Durable) != 1 || st.Durable[0].Ref != br {
		t.Errorf("flush response = %+v", st)
	}
	if rec := do("GET", root+"camli/"+br.String(), "", nil); rec.Code != http.StatusOK || rec.Body.String() != contents {
		t.Errorf("GET after flush = %d %q; want 200 %q", rec.Code, rec.Body, contents)
	}
	if rec := do("DELETE", root+"camli/session/"+opened.Session, "", nil); rec.Code != http.StatusNoContent {
		t.Errorf("close = %d: %s", rec.Code, rec.Body)
	}
	if rec := do("POST", root+"camli/session/"+opened.Session+"/status", "", strings.NewReader("")); rec.Code != http.StatusNotFound {
		t.Errorf("status after close = %d; want %d", rec.Code, http.StatusNotFound)
	}
}
