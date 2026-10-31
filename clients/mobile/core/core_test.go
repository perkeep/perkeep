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

package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"runtime"
	"testing"
)

// do makes a request to s and decodes the JSON response into out, if
// non-nil. It fails the test if the status isn't wantStatus.
func do(t *testing.T, s *server, method, path string, body any, wantStatus int, out any) {
	t.Helper()
	var reqBody []byte
	if body != nil {
		var err error
		if reqBody, err = json.Marshal(body); err != nil {
			t.Fatal(err)
		}
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(reqBody))
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != wantStatus {
		t.Fatalf("%s %s: status %d, want %d; body: %s", method, path, rec.Code, wantStatus, rec.Body)
	}
	if out != nil {
		if err := json.Unmarshal(rec.Body.Bytes(), out); err != nil {
			t.Fatalf("%s %s: decoding %s: %v", method, path, rec.Body, err)
		}
	}
}

func newTestServer(t *testing.T, dir string) *server {
	s := newServer()
	do(t, s, "POST", "/init", map[string]string{"dataDir": dir}, http.StatusNoContent, nil)
	return s
}

func TestHello(t *testing.T) {
	var got map[string]string
	do(t, newServer(), "GET", "/hello", nil, http.StatusOK, &got)
	if got["goos"] != runtime.GOOS {
		t.Errorf("goos = %q; want %q", got["goos"], runtime.GOOS)
	}
}

func TestCall(t *testing.T) {
	if status, _ := call("GET", "/hello", nil); status != http.StatusOK {
		t.Errorf("hello status = %d", status)
	}
	if status, _ := call("GET", "/nope", nil); status != http.StatusNotFound {
		t.Errorf("status = %d; want 404", status)
	}
}

func TestNotInitialized(t *testing.T) {
	do(t, newServer(), "GET", "/accounts", nil, http.StatusServiceUnavailable, nil)
}

func TestAccounts(t *testing.T) {
	dir := t.TempDir()
	s := newTestServer(t, dir)

	var l accountList
	do(t, s, "GET", "/accounts", nil, http.StatusOK, &l)
	if l.Current != "" || len(l.Accounts) != 0 {
		t.Fatalf("initial accounts = %+v", l)
	}

	var home, work Account
	do(t, s, "POST", "/accounts", accountFields{Server: "pk.example.com/", Username: "me"}, http.StatusCreated, &home)
	if home.Server != "https://pk.example.com" || home.Name != "pk.example.com" || home.Username != "me" {
		t.Errorf("home = %+v", home)
	}
	wantInclude, wantExclude := newAccountAlbums()
	if !reflect.DeepEqual(home.Include, wantInclude) || !reflect.DeepEqual(home.Exclude, wantExclude) {
		t.Errorf("home albums = %+v, %+v; want defaults", home.Include, home.Exclude)
	}
	do(t, s, "POST", "/accounts", accountFields{Name: "Work", Server: "http://10.0.0.5:3179"}, http.StatusCreated, &work)

	do(t, s, "GET", "/accounts", nil, http.StatusOK, &l)
	if l.Current != home.ID || len(l.Accounts) != 2 {
		t.Fatalf("after adds: %+v; want 2 accounts with first current", l)
	}

	do(t, s, "POST", "/accounts/"+work.ID+"/select", nil, http.StatusNoContent, nil)
	var updated Account
	do(t, s, "PUT", "/accounts/"+home.ID, accountFields{Name: "Home", Server: "https://pk.example.com:8443"}, http.StatusOK, &updated)
	if updated.Name != "Home" || updated.Server != "https://pk.example.com:8443" || updated.Username != "" {
		t.Errorf("updated = %+v", updated)
	}
	do(t, s, "PUT", "/accounts/"+home.ID, accountFields{Server: "ftp://x"}, http.StatusBadRequest, nil)
	do(t, s, "PUT", "/accounts/nope", accountFields{Server: "x"}, http.StatusNotFound, nil)

	// State survives a restart.
	s = newTestServer(t, dir)
	do(t, s, "GET", "/accounts", nil, http.StatusOK, &l)
	if l.Current != work.ID || len(l.Accounts) != 2 || l.Accounts[0].Name != "Home" {
		t.Fatalf("after reload: %+v", l)
	}

	// Deleting the current account makes another one current.
	do(t, s, "DELETE", "/accounts/"+work.ID, nil, http.StatusNoContent, nil)
	do(t, s, "GET", "/accounts", nil, http.StatusOK, &l)
	if l.Current != home.ID || len(l.Accounts) != 1 {
		t.Fatalf("after delete: %+v", l)
	}
	do(t, s, "DELETE", "/accounts/"+home.ID, nil, http.StatusNoContent, nil)
	do(t, s, "GET", "/accounts", nil, http.StatusOK, &l)
	if l.Current != "" || len(l.Accounts) != 0 {
		t.Fatalf("after deleting all: %+v", l)
	}
}

func TestNormalizeServer(t *testing.T) {
	for in, want := range map[string]string{
		"pk.example.com":            "https://pk.example.com",
		" https://pk.example.com/ ": "https://pk.example.com",
		"http://localhost:3179":     "http://localhost:3179",
		"https://example.com/pk/":   "https://example.com/pk",
		"http://[fd7a::1]:3179/":    "http://[fd7a::1]:3179",
		"":                          "",
		"ftp://example.com":         "",
		"https://":                  "",
		"https://u:p@example.com":   "",
		"https://example.com/?a=b":  "",
		"https://example.com/#frag": "",
	} {
		got, err := normalizeServer(in)
		if want == "" {
			if err == nil {
				t.Errorf("normalizeServer(%q) = %q; want error", in, got)
			}
			continue
		}
		if err != nil || got != want {
			t.Errorf("normalizeServer(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
}

func TestAlbums(t *testing.T) {
	s := newTestServer(t, t.TempDir())
	var a Account
	do(t, s, "POST", "/accounts", accountFields{Server: "pk.example.com"}, http.StatusCreated, &a)
	path := "/accounts/" + a.ID

	cameraRoll := Album{ID: "CR/L0/001", Title: "Recents", CameraRoll: true}
	whatsApp := Album{ID: "A/1", Title: "WhatsApp"}
	trip := Album{ID: "A/2", Title: "Trip"}

	type states struct {
		Albums []struct {
			Album Album      `json:"album"`
			State AlbumState `json:"state"`
		} `json:"albums"`
		Unmatched struct {
			Include []Album `json:"include"`
			Exclude []Album `json:"exclude"`
		} `json:"unmatched"`
	}
	check := func(device []Album, want []AlbumState, wantUnmatchedInclude, wantUnmatchedExclude int) {
		t.Helper()
		var got states
		do(t, s, "POST", path+"/albumstates", map[string]any{"albums": device}, http.StatusOK, &got)
		var gotStates []AlbumState
		for _, e := range got.Albums {
			gotStates = append(gotStates, e.State)
		}
		if !reflect.DeepEqual(gotStates, want) {
			t.Errorf("states = %v; want %v", gotStates, want)
		}
		if len(got.Unmatched.Include) != wantUnmatchedInclude || len(got.Unmatched.Exclude) != wantUnmatchedExclude {
			t.Errorf("unmatched = %+v; want %d include, %d exclude", got.Unmatched, wantUnmatchedInclude, wantUnmatchedExclude)
		}
	}

	// Defaults: Camera Roll included; WhatsApp excluded if present.
	check([]Album{cameraRoll, trip}, []AlbumState{AlbumInclude, AlbumNone}, 0, 1)
	check([]Album{cameraRoll, whatsApp, trip}, []AlbumState{AlbumInclude, AlbumExclude, AlbumNone}, 0, 0)
	check([]Album{cameraRoll, {ID: "A/9", Title: "whatsapp"}}, []AlbumState{AlbumInclude, AlbumExclude}, 0, 0)

	set := func(al Album, st AlbumState) {
		t.Helper()
		do(t, s, "PUT", path+"/albums", map[string]any{"album": al, "state": st}, http.StatusOK, nil)
	}
	set(trip, AlbumInclude)
	set(whatsApp, AlbumInclude) // replaces the title-only exclusion
	set(cameraRoll, AlbumNone)
	check([]Album{cameraRoll, whatsApp, trip}, []AlbumState{AlbumNone, AlbumInclude, AlbumInclude}, 0, 0)

	// An album selected by ID stays selected when renamed; another album
	// with its old title isn't affected.
	check([]Album{{ID: "A/2", Title: "Trip 2026"}, {ID: "A/3", Title: "Trip"}}, []AlbumState{AlbumInclude, AlbumNone}, 1, 0)

	do(t, s, "PUT", path+"/albums", map[string]any{"album": trip, "state": "bogus"}, http.StatusBadRequest, nil)

	var l accountList
	do(t, s, "GET", "/accounts", nil, http.StatusOK, &l)
	got := l.Accounts[0]
	if len(got.Exclude) != 0 || len(got.Include) != 2 {
		t.Errorf("stored albums = include %+v, exclude %+v", got.Include, got.Exclude)
	}
}
