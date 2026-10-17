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

package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"sort"
	"strings"
	"testing"
	"testing/synctest"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver/protocol"
	"perkeep.org/pkg/blobserver/uploadsession"
	"perkeep.org/pkg/test"
)

// serveSession serves a request to the session handler, returning the
// recorded response.
func serveSession(sessions *uploadsession.Manager, method, path string, form url.Values) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	CreateSessionHandler(sessions).ServeHTTP(rec, req)
	return rec
}

func decode(t *testing.T, rec *httptest.ResponseRecorder, v any) {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), v); err != nil {
		t.Fatalf("decoding %q: %v", rec.Body, err)
	}
}

func sizedRefsOf(sbs []blob.SizedRef) []blob.Ref {
	var refs []blob.Ref
	for _, sb := range sbs {
		refs = append(refs, sb.Ref)
	}
	return refs
}

// TestSessionHTTP checks opening a session, uploading into it, flushing
// it, acknowledging its durable blobs, and closing it.
func TestSessionHTTP(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sto := &uploadStorage{Fetcher: new(test.Fetcher)}
		sessions := uploadsession.NewManager(sto, uploadsession.Limits{})

		var opened protocol.SessionOpenResponse
		decode(t, serveSession(sessions, "POST", "/bs/camli/session", url.Values{"idleTimeout": {"60"}}), &opened)
		if opened.Session == "" || opened.IdleTimeoutSeconds != 60 || opened.MaxBufferedBytes != sessions.Limits().SessionBytes {
			t.Fatalf("open response = %+v", opened)
		}
		sessionPath := "/bs/camli/session/" + opened.Session

		a, b := blobPart("a"), blobPart("b")
		bad := blob.RefFromString("bad")
		res := uploadURL(t, sto, sessions, "/bs/camli/upload?session="+opened.Session,
			[]uploadPart{a, {bad.String(), "BAD"}, b})
		want := []blob.Ref{blob.MustParse(a.name), blob.MustParse(b.name)}
		if got := sizedRefsOf(res.Accepted); !slices.Equal(got, want) || len(res.Received) != 0 {
			t.Errorf("upload accepted %v, received %v; want accepted %v", got, sizedRefsOf(res.Received), want)
		}
		if !strings.Contains(res.ErrorText, bad.String()) {
			t.Errorf("ErrorText = %q; want it to report %v", res.ErrorText, bad)
		}
		var st protocol.SessionStatusResponse
		decode(t, serveSession(sessions, "POST", sessionPath+"/flush", nil), &st)
		sort.Sort(blob.ByRef(want))
		if got := sizedRefsOf(st.Durable); !slices.Equal(got, want) {
			t.Errorf("flush durable = %v; want %v", got, want)
		}
		if sto.NumBlobs() != 2 {
			t.Errorf("storage has %d blobs after flush; want 2", sto.NumBlobs())
		}
		ack := url.Values{"ack": {want[0].String()}}
		st = protocol.SessionStatusResponse{}
		decode(t, serveSession(sessions, "POST", sessionPath+"/status", ack), &st)
		if got := sizedRefsOf(st.Durable); !slices.Equal(got, want[1:]) {
			t.Errorf("status after ack = %v; want %v", got, want[1:])
		}
		if rec := serveSession(sessions, "POST", sessionPath+"/status", url.Values{"ack": {"junk"}}); rec.Code != http.StatusBadRequest {
			t.Errorf("status with a bad ack: status %d; want %d", rec.Code, http.StatusBadRequest)
		}

		if rec := serveSession(sessions, "DELETE", sessionPath, nil); rec.Code != http.StatusNoContent {
			t.Errorf("close: status %d: %s", rec.Code, rec.Body)
		}
		for _, path := range []string{sessionPath + "/flush", sessionPath + "/status", "/bs/camli/session/nope/flush"} {
			if rec := serveSession(sessions, "POST", path, nil); rec.Code != http.StatusNotFound {
				t.Errorf("POST %s after close: status %d; want %d", path, rec.Code, http.StatusNotFound)
			}
		}
		rec := httptest.NewRecorder()
		CreateBatchUploadHandler(sto, sessions).ServeHTTP(rec,
			uploadRequest(t, "/bs/camli/upload?session="+opened.Session, []uploadPart{a}))
		if rec.Code != http.StatusNotFound {
			t.Errorf("upload to a closed session: status %d; want %d", rec.Code, http.StatusNotFound)
		}
	})
}

// TestSessionUploadVivify checks that vivifying isn't done in a session.
func TestSessionUploadVivify(t *testing.T) {
	sto := &uploadStorage{Fetcher: new(test.Fetcher)}
	sessions := uploadsession.NewManager(sto, uploadsession.Limits{})
	s, err := sessions.Open(0)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	req := uploadRequest(t, "/bs/camli/upload?session="+s.ID(), []uploadPart{blobPart("a")})
	req.Header.Set("X-Camlistore-Vivify", "1")
	rec := httptest.NewRecorder()
	CreateBatchUploadHandler(sto, sessions).ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("vivify in a session: status %d; want %d", rec.Code, http.StatusBadRequest)
	}
}
