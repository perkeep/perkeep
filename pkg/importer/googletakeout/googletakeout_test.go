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

package googletakeout

import (
	"archive/zip"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"testing/synctest"

	"perkeep.org/internal/attrclaims"
	"perkeep.org/internal/httputil"
	"perkeep.org/internal/takeoutimport"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/importer"
	imptest "perkeep.org/pkg/importer/test"
	"perkeep.org/pkg/search"
)

const exportName = "takeout-20240101T120000Z"

// writeExport writes the zip file of a small export in dir/Takeout.
func writeExport(t *testing.T, dir string) {
	t.Helper()
	if err := os.Mkdir(filepath.Join(dir, "Takeout"), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(filepath.Join(dir, "Takeout", exportName+"-001.zip"))
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for _, file := range []struct{ name, data string }{
		{"Takeout/Google Photos/Photos from 2023/IMG_0001.JPG", "photo one"},
		{"Takeout/Google Photos/Photos from 2023/IMG_0001.JPG.supplemental-metadata.json", `{"title": "IMG_0001.JPG", "description": "the first", "photoTakenTime": {"timestamp": "1700000000"}}`},
		{"Takeout/Google Photos/Photos from 2023/IMG_0001-edited.JPG", "photo one, edited"},
		{"Takeout/Google Photos/Photos from 2023/IMG_0002.JPG", "photo two"},
		{"Takeout/Google Photos/Trip/IMG_0002.JPG", "photo two"},
		{"Takeout/Google Photos/Trip/metadata.json", `{"title": "Trip"}`},
		{"Takeout/Chrome/History.json", "{}"},
	} {
		w, err := zw.Create(file.name)
		if err != nil {
			t.Fatal(err)
		}
		io.WriteString(w, file.data)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

// testHost is a Host for tests of the importer's pages and runs.
type testHost struct {
	t *testing.T
	h *importer.Host
}

// do makes a request of the page at suffix, after the importer prefix,
// with form as a POST body if non-nil.
func (th testHost) do(method, suffix string, form url.Values) *httptest.ResponseRecorder {
	th.t.Helper()
	var body io.Reader = http.NoBody
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	r := httptest.NewRequest(method, "/importer/"+suffix, body)
	if form != nil {
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	path, _, _ := strings.Cut(suffix, "?")
	r.Header.Set(httputil.PathSuffixHeader, path)
	w := httptest.NewRecorder()
	th.h.ServeHTTP(w, r)
	return w
}

// post posts form to suffix, failing the test unless it's answered with
// code.
func (th testHost) post(suffix string, form url.Values, code int) *httptest.ResponseRecorder {
	th.t.Helper()
	w := th.do("POST", suffix, form)
	if w.Code != code {
		th.t.Fatalf("POST %s %v: %d; want %d\n%s", suffix, form, w.Code, code, w.Body)
	}
	return w
}

// attrs returns the attributes of the account acct.
func (th testHost) attr(acct blob.Ref, key string) string {
	th.t.Helper()
	o, err := th.h.ObjectFromRef(acct)
	if err != nil {
		th.t.Fatal(err)
	}
	return o.Attr(key)
}

var callbackRx = regexp.MustCompile(`action="/importer/googletakeout/callback\?acct=([^"]+)"`)

func TestImport(t *testing.T) {
	dir := t.TempDir()
	writeExport(t, dir)
	// Made outside the bubble, as its search handler's goroutines run
	// as long as the process.
	h := imptest.NewMemoryHost(t, importer.HostConfig{})
	synctest.Test(t, func(t *testing.T) {
		// Closed in the bubble, as it cancels the runs' contexts, made
		// in the bubble.
		defer h.Close()
		th := testHost{t, h}

		w := th.post("sources/", url.Values{"type": {"local"}, "dir": {dir}}, http.StatusSeeOther)
		src := strings.TrimPrefix(w.Header().Get("Location"), "/importer/sources/")

		// Set up an account.
		w = th.post("googletakeout", url.Values{"mode": {"newacct"}}, http.StatusOK)
		m := callbackRx.FindStringSubmatch(w.Body.String())
		if m == nil {
			t.Fatalf("setup page has no form:\n%s", w.Body)
		}
		acct, ok := blob.Parse(m[1])
		if !ok {
			t.Fatalf("bad account %q", m[1])
		}
		callback := "googletakeout/callback?acct=" + acct.String()
		w = th.post(callback, url.Values{"step": {"browse"}, "source": {src}}, http.StatusOK)
		if body := w.Body.String(); !strings.Contains(body, exportName) || !strings.Contains(body, ">Takeout/<") {
			t.Fatalf("browse page doesn't have the export and the folders:\n%s", body)
		}
		th.post(callback, url.Values{"step": {"confirm"}}, http.StatusBadRequest) // not scanned yet

		// A folder can be gone to by its path, as typed.
		w = th.post(callback, url.Values{"step": {"browse"}, "source": {src}, "goto": {"/Takeout/"}}, http.StatusOK)
		if body := w.Body.String(); !strings.Contains(body, "Looked in Takeout/") || !strings.Contains(body, exportName) {
			t.Fatalf("browse page of Takeout/ doesn't have the export:\n%s", body)
		}
		th.post(callback, url.Values{"step": {"browse"}, "source": {src}, "goto": {"missing"}}, http.StatusBadRequest)

		// Picking the export scans it.
		th.post(callback, url.Values{"step": {"pick"}, "source": {src}, "export": {exportName}}, http.StatusSeeOther)
		synctest.Wait()
		if got := th.attr(acct, attrState); got != stateReview {
			t.Fatalf("state after scanning = %q; want %q", got, stateReview)
		}
		var ov overview
		if err := json.Unmarshal([]byte(th.attr(acct, attrOverview)), &ov); err != nil {
			t.Fatal(err)
		}
		if ov.Zips != 1 || ov.Photos == nil {
			t.Fatalf("overview = %+v", ov)
		}
		// Two distinct photos, of three files, without the edited copy.
		if p := ov.Photos; p.Files != 3 || p.Distinct != 2 || p.Edited != 1 || p.Albums != 1 || p.Missing != 2 || p.Have != 0 {
			t.Errorf("Google Photos overview = %+v", p)
		}
		if len(ov.Products) != 2 || ov.Products[0].Name != "Google Photos" || ov.Products[1].Name != "Chrome" {
			t.Errorf("products = %+v", ov.Products)
		}
		page := th.do("GET", "googletakeout/"+acct.String(), nil).Body.String()
		if !strings.Contains(page, "To upload") || !strings.Contains(page, `value="confirm"`) {
			t.Errorf("account page has no overview or confirmation:\n%s", page)
		}
		// Its script reloads it once a run that's running finishes.
		if !regexp.MustCompile(`var wasRunning = *false *;`).MatchString(page) {
			t.Errorf("account page's script doesn't know the account isn't running:\n%s", page)
		}

		// Confirming imports it.
		th.post(callback, url.Values{"step": {"confirm"}}, http.StatusSeeOther)
		synctest.Wait()
		if got := th.attr(acct, attrState); got != stateDone {
			t.Fatalf("state after importing = %q; want %q", got, stateDone)
		}
		var res result
		if err := json.Unmarshal([]byte(th.attr(acct, attrResult)), &res); err != nil {
			t.Fatal(err)
		}
		if res.Photos == nil || res.Photos.Uploaded != 2 {
			t.Errorf("result = %+v; want 2 Google Photos files uploaded", res)
		}
		ctx := context.Background()
		_, pn, err := attrclaims.PlannedPermanode(ctx, h.Signer(), takeoutimport.ExportPermanodeKeyPrefix+exportName)
		if err != nil {
			t.Fatal(err)
		}
		if got := th.attr(acct, attrExportPermanode); got != pn.String() {
			t.Errorf("export permanode = %q; want %v", got, pn)
		}
		res2, err := h.Searcher().Query(ctx, &search.SearchQuery{Constraint: &search.Constraint{Permanode: &search.PermanodeConstraint{Attr: "description", Value: "the first"}}})
		if err != nil || len(res2.Blobs) != 1 {
			t.Errorf("finding the first photo by its description: %v, %v", res2, err)
		}
		if ResumeRunOf(h, acct) {
			t.Error("a finished import would be resumed")
		}
	})
}

// ResumeRunOf reports whether the run of the account acct would be
// resumed when the server starts.
func ResumeRunOf(h *importer.Host, acct blob.Ref) bool {
	o, err := h.ObjectFromRef(acct)
	return err == nil && imp{}.ResumeRun(o)
}

func TestResumeRun(t *testing.T) {
	h := imptest.NewMemoryHost(t, importer.HostConfig{})
	t.Cleanup(func() { h.Close() })
	for _, tt := range []struct {
		state, paused string
		want          bool
	}{
		{"", "", false},
		{stateScanning, "", true},
		{stateScanning, "true", false},
		{stateReview, "", false},
		{stateImporting, "", true},
		{stateImporting, "true", false},
		{stateDone, "", false},
	} {
		o, err := h.NewObject()
		if err != nil {
			t.Fatal(err)
		}
		if err := o.SetAttrs(attrState, tt.state, attrPaused, tt.paused); err != nil {
			t.Fatal(err)
		}
		if got := (imp{}).ResumeRun(o); got != tt.want {
			t.Errorf("ResumeRun(state %q, paused %q) = %v; want %v", tt.state, tt.paused, got, tt.want)
		}
	}
}
