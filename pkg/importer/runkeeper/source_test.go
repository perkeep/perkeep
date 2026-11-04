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

package runkeeper

import (
	"archive/zip"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"perkeep.org/internal/httputil"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/importer"
	imptest "perkeep.org/pkg/importer/test"
	"perkeep.org/pkg/types/camtypes"
)

// TestArchiveFromFileSource checks that a Runkeeper export's ZIP can be
// picked from a file source, which copies it into the server, for the
// importer's page to inspect and import, as an uploaded one.
func TestArchiveFromFileSource(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "Exports"), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(filepath.Join(dir, "Exports", "runkeeper-export.zip"))
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for name, data := range synthetic() {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		io.WriteString(w, data)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	f.Close()

	h := imptest.NewMemoryHost(t, importer.HostConfig{})
	t.Cleanup(func() { h.Close() })
	do := func(method, suffix string, form url.Values) *httptest.ResponseRecorder {
		t.Helper()
		var body io.Reader = http.NoBody
		if form != nil {
			body = strings.NewReader(form.Encode())
		}
		r := httptest.NewRequest(method, "/importer/"+suffix, body)
		if form != nil {
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		p, _, _ := strings.Cut(suffix, "?")
		r.Header.Set(httputil.PathSuffixHeader, p)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}

	w := do("POST", "sources/", url.Values{"type": {"local"}, "dir": {dir}, "title": {"Exports dir"}})
	if w.Code != http.StatusSeeOther {
		t.Fatalf("adding a source: %d %s", w.Code, w.Body)
	}
	src := strings.TrimPrefix(w.Header().Get("Location"), "/importer/sources/")

	if body := do("GET", "runkeeper", nil).Body.String(); !strings.Contains(body, "/importer/runkeeper/archive-source") {
		t.Errorf("importer page doesn't link to importing from a file source:\n%s", body)
	}
	if body := do("GET", "runkeeper/archive-source", nil).Body.String(); !strings.Contains(body, "Exports dir") {
		t.Errorf("file source page doesn't list the source:\n%s", body)
	}
	if body := do("GET", "runkeeper/archive-source?source="+src, nil).Body.String(); !strings.Contains(body, `value="Exports"`) {
		t.Errorf("source's top doesn't have its folder:\n%s", body)
	}
	body := do("GET", "runkeeper/archive-source?source="+src+"&goto=/Exports/", nil).Body.String()
	if !strings.Contains(body, "runkeeper-export.zip") || !strings.Contains(body, `value="Exports/runkeeper-export.zip"`) {
		t.Fatalf("Exports folder doesn't have the ZIP:\n%s", body)
	}

	w = do("POST", "runkeeper/archive-source", url.Values{"source": {src}, "file": {"Exports/runkeeper-export.zip"}})
	loc, err := url.Parse(w.Header().Get("Location"))
	if w.Code != http.StatusSeeOther || err != nil || loc.Path != "/importer/runkeeper" {
		t.Fatalf("importing the ZIP: %d, to %q: %s", w.Code, w.Header().Get("Location"), w.Body)
	}
	ref, ok := blob.Parse(loc.Query().Get("fileRef"))
	if !ok {
		t.Fatalf("redirected to %v, without a file ref", loc)
	}
	if body := do("GET", "runkeeper?fileRef="+ref.String(), nil).Body.String(); !strings.Contains(body, `data-file-ref="`+ref.String()+`"`) {
		t.Errorf("importer page doesn't have the copied ZIP to inspect:\n%s", body)
	}
	res, err := h.ImportArchive(context.Background(), "runkeeper", camtypes.ArchiveImportRequest{Action: "inspect", FileRef: ref})
	if err != nil {
		t.Fatalf("inspecting the copied ZIP: %v", err)
	}
	if res.Identity.Name != "Test Person" {
		t.Errorf("identity = %+v; want Test Person's", res.Identity)
	}

	// Neither folders nor files outside the source can be imported.
	for _, file := range []string{"Exports", "../" + filepath.Base(dir), "/etc/passwd"} {
		if w := do("POST", "runkeeper/archive-source", url.Values{"source": {src}, "file": {file}}); w.Code != http.StatusBadRequest {
			t.Errorf("importing %q: %d; want 400", file, w.Code)
		}
	}
}
