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
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"perkeep.org/pkg/auth"
)

func TestStartupLog(t *testing.T) {
	l := new(startupLog)
	fmt.Fprintf(l, "one\ntw")
	fmt.Fprintf(l, "o\nthree\n")
	if got, want := l.Lines(), []string{"one", "two", "three"}; !slices.Equal(got, want) {
		t.Errorf("Lines = %q; want %q", got, want)
	}

	// Once full, it keeps the most recent lines.
	l = new(startupLog)
	for i := range startupLogLines + 10 {
		fmt.Fprintf(l, "line %d\n", i)
	}
	got := l.Lines()
	if len(got) != startupLogLines {
		t.Fatalf("got %d lines; want %d", len(got), startupLogLines)
	}
	if got[0] != "line 10" || got[len(got)-1] != fmt.Sprintf("line %d", startupLogLines+9) {
		t.Errorf("lines run from %q to %q; want line 10 to line %d", got[0], got[len(got)-1], startupLogLines+9)
	}
}

func TestStartupHandler(t *testing.T) {
	l := new(startupLog)
	fmt.Fprintf(l, "loading <corpus>\n")
	h := &startupHandler{log: l, start: time.Now()}
	get := func(path string, header ...string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest("GET", path, nil)
		for i := 0; i+1 < len(header); i += 2 {
			req.Header.Set(header[i], header[i+1])
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	auth.SetMode(&auth.UserPass{Username: "owner", Password: "secret"})
	t.Cleanup(func() { auth.SetMode(auth.None{}) })

	// API requests, including discovery, are told to come back later.
	for _, rec := range []*httptest.ResponseRecorder{
		get("/bs/camli/sha224-abc"),
		get("/?camli.mode=config"),
		get("/ui/", "Accept", "text/x-camli-configuration"),
	} {
		if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Retry-After") == "" {
			t.Errorf("API request: got %d, Retry-After %q; want 503 with Retry-After", rec.Code, rec.Header().Get("Retry-After"))
		}
	}

	// The page needs the owner's credentials.
	if rec := get("/ui/"); rec.Code != http.StatusUnauthorized {
		t.Errorf("unauthenticated /ui/: got %d; want 401", rec.Code)
	}

	auth.SetMode(auth.None{})
	for _, path := range []string{"/", "/ui/"} {
		rec := get(path)
		if rec.Code != http.StatusServiceUnavailable {
			t.Errorf("%s: got %d; want 503", path, rec.Code)
		}
		if body := rec.Body.String(); !strings.Contains(body, "loading &lt;corpus&gt;") {
			t.Errorf("%s: body doesn't show the escaped log line:\n%s", path, body)
		}
	}
}
