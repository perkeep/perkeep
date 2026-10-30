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

package allimporters

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"perkeep.org/internal/httputil"
	"perkeep.org/pkg/importer"
	imptest "perkeep.org/pkg/importer/test"
)

type blobCounter interface{ NumBlobs() int }

// pagesHost is a host for tests of the importer pages, with a real
// index, to see what visiting them stores.
type pagesHost struct {
	t *testing.T
	h *importer.Host
}

func newPagesHost(t *testing.T) *pagesHost {
	h := imptest.NewMemoryHost(t, importer.HostConfig{
		HTTPClient: &http.Client{Transport: navigationNoNetwork{t}},
	})
	return &pagesHost{t, h}
}

func (p *pagesHost) blobs() int { return p.h.Target().(blobCounter).NumBlobs() }

// do makes a request of the host's page at suffix (after the importer
// prefix), with form as a POST body if non-nil.
func (p *pagesHost) do(method, suffix string, form url.Values) *httptest.ResponseRecorder {
	p.t.Helper()
	var body *strings.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	} else {
		body = strings.NewReader("")
	}
	r := httptest.NewRequest(method, "/importer/"+suffix, body)
	if form != nil && method == "POST" {
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	path, _, _ := strings.Cut(suffix, "?")
	r.Header.Set(httputil.PathSuffixHeader, path)
	w := httptest.NewRecorder()
	p.h.ServeHTTP(w, r)
	return w
}

var acctField = regexp.MustCompile(`name="acct" value="([^"]+)"`)

// TestPagesStoreNothingUntilSaved checks that looking at the importer
// pages, and opening a new account's setup form, store nothing, and
// that saving the setup stores the account, which the pages then list.
func TestPagesStoreNothingUntilSaved(t *testing.T) {
	p := newPagesHost(t)
	start := p.blobs()
	for _, page := range []string{"", "pinboard", "raindrop", "instapaper"} {
		if w := p.do("GET", page, nil); w.Code != http.StatusOK {
			t.Fatalf("GET %q = %d: %s", page, w.Code, w.Body)
		}
	}
	if n := p.blobs(); n != start {
		t.Fatalf("viewing pages stored %d blobs", n-start)
	}

	// Starting to add an account, from the list of all importers, shows
	// its setup form but stores nothing.
	w := p.do("POST", "", url.Values{"mode": {"newacct"}, "importer": {"pinboard"}})
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "apiToken") {
		t.Fatalf("new account = %d: %s", w.Code, w.Body)
	}
	m := acctField.FindStringSubmatch(w.Body.String())
	if m == nil {
		t.Fatalf("setup form has no account: %s", w.Body)
	}
	acct := m[1]
	if n := p.blobs(); n != start {
		t.Fatalf("opening a setup form stored %d blobs", n-start)
	}
	if body := p.do("GET", "", nil).Body.String(); !strings.Contains(body, "No accounts yet") {
		t.Errorf("unsaved account is listed:\n%s", body)
	}

	// An unsaved account can be deleted, which stores nothing.
	if w := p.do("POST", "pinboard/"+acct, url.Values{"mode": {"delete"}}); w.Code != http.StatusFound {
		t.Errorf("deleting unsaved account = %d: %s", w.Code, w.Body)
	}
	if n := p.blobs(); n != start {
		t.Fatalf("deleting an unsaved account stored %d blobs", n-start)
	}

	// Saving another's setup stores it, and the pages list it.
	w = p.do("POST", "pinboard", url.Values{"mode": {"newacct"}})
	acct = acctField.FindStringSubmatch(w.Body.String())[1]
	w = p.do("GET", "pinboard/callback?"+url.Values{"acct": {acct}, "apiToken": {"someone:secret"}}.Encode(), nil)
	if w.Code != http.StatusFound {
		t.Fatalf("saving setup = %d: %s", w.Code, w.Body)
	}
	if p.blobs() == start {
		t.Fatal("saving setup stored nothing")
	}
	for _, page := range []string{"", "pinboard"} {
		body := p.do("GET", page, nil).Body.String()
		if !strings.Contains(body, "Pinboard account for someone") || !strings.Contains(body, "/importer/pinboard/"+acct) {
			t.Errorf("page %q doesn't list the saved account:\n%s", page, body)
		}
	}
	if body := p.do("GET", "pinboard/"+acct, nil).Body.String(); !strings.Contains(body, "confirm(") || strings.Contains(body, "isn't saved yet") {
		t.Errorf("saved account's page:\n%s", body)
	}
}

// TestNewAccountNeedsAPIKeyFirst checks that adding an account of an
// importer that needs an API key it doesn't have goes to its page, to
// set one, rather than to an account's setup.
func TestNewAccountNeedsAPIKeyFirst(t *testing.T) {
	p := newPagesHost(t)
	start := p.blobs()
	w := p.do("POST", "", url.Values{"mode": {"newacct"}, "importer": {"instapaper"}})
	if w.Code != http.StatusSeeOther || !strings.HasSuffix(w.Header().Get("Location"), "/instapaper") {
		t.Fatalf("new account without an API key = %d, Location %q", w.Code, w.Header().Get("Location"))
	}
	body := p.do("GET", "instapaper", nil).Body.String()
	if !strings.Contains(body, `value="saveclientidsecret"`) || strings.Contains(body, `value="newacct"`) {
		t.Errorf("importer page without an API key:\n%s", body)
	}
	if n := p.blobs(); n != start {
		t.Errorf("stored %d blobs", n-start)
	}
}
