/*
Copyright 2013 The Perkeep Authors

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
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRedirect(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"/foo", ""},
		{"/gw/502aff1fd522c454e39a3723b596aca43d206d4e", "https://camlistore.googlesource.com/camlistore/+/502aff1fd522c454e39a3723b596aca43d206d4e"},
		{"/gw/502AFF", "https://camlistore.googlesource.com/camlistore/+/502AFF"},
		{"/gw/server", "https://camlistore.googlesource.com/camlistore/+/master/server"},
		{"/code/?p=camlistore.git;a=commit;h=b0d2a8f0e5f27bbfc025a96ec3c7896b42d198ed", "https://camlistore.googlesource.com/camlistore/+/b0d2a8f0e5f27bbfc025a96ec3c7896b42d198ed"},
		{"/docs/schema/", "/doc/schema/"},

		// strip directory index files
		{"/doc/README.md", "/doc/"},
		{"/doc/index.html", "/doc/"},

		// strip common file extensions
		{"/doc/overview.md", "/doc/overview"},
		{"/doc/overview.html", "/doc/overview"},
		{"/doc/overview.txt", ""},
	}
	for _, tt := range tests {
		u, err := url.ParseRequestURI(tt.in)
		if err != nil {
			t.Fatal(err)
		}
		got := redirectPath(u)
		if got != tt.want {
			t.Errorf("redirectPath(%q) = %q; want %q", tt.in, got, tt.want)
		}
	}

}

func TestIsIssueRequest(t *testing.T) {
	wantNum := "https://github.com/perkeep/perkeep/issues/34"
	wantList := "https://github.com/perkeep/perkeep/issues"
	tests := []struct {
		urlPath   string
		redirects bool
		dest      string
	}{
		{"/issue", true, wantList},
		{"/issue/", true, wantList},
		{"/issue/34", true, wantNum},
		{"/issue34", false, ""},
		{"/issues", true, wantList},
		{"/issues/", true, wantList},
		{"/issues/34", true, wantNum},
		{"/issues34", false, ""},
		{"/bugs", true, wantList},
		{"/bugs/", true, wantList},
		{"/bugs/34", true, wantNum},
		{"/bugs34", false, ""},
	}
	for _, tt := range tests {
		dest, ok := issueRedirect(tt.urlPath)
		if ok != tt.redirects || dest != tt.dest {
			t.Errorf("issueRedirect(%q) = %q, %v; want %q, %v", tt.urlPath, dest, ok, tt.dest, tt.redirects)
		}
	}
}

func TestDocHandler(t *testing.T) {
	// Set up environment
	var err error
	*root, err = os.Getwd()
	if err != nil {
		t.Fatalf("Failed to getwd: %v", err)
	}
	*root = filepath.Join(*root, "..")
	readTemplates()

	tests := []struct {
		path       string
		status     int
		wantSubstr string
	}{
		// Test that the title tag is constructed from the h1 element
		{"/website/testdata/sample", http.StatusOK,
			"<title>Lorem Ipsum - Perkeep</title>"},
		// Test that an html extension redirects to the base path
		{"/website/testdata/sample.html", 302, "Found"},
	}

	for _, tt := range tests {
		// Construct a request that maps to the given path
		req, err := http.NewRequest("GET", tt.path, nil)
		if err != nil {
			t.Fatal(err)
		}

		rr := httptest.NewRecorder()
		handler := http.HandlerFunc(docHandler)
		handler.ServeHTTP(rr, req)

		// Check the status code is what we expect.
		if status := rr.Code; status != tt.status {
			t.Errorf("for path %s, code=%v want %v", tt.path, status, tt.status)
		}

		// Check that the output contains the specified substring
		if !strings.Contains(rr.Body.String(), tt.wantSubstr) {
			t.Errorf("for path %s, got %q should contain %q",
				tt.path, rr.Body.String(), tt.wantSubstr)
		}
	}
}
