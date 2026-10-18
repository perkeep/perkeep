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

package importer

import (
	"bytes"
	"context"

	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"perkeep.org/internal/httputil"
	"perkeep.org/pkg/types/camtypes"
)

func archiveUIHost(t *testing.T) (*Host, http.Handler) {
	t.Helper()
	h, _, _ := archiveTestHost(t)
	h.importerBase = "https://public.example/custom-imports/"
	h.discoveryRoot = "/metadata/"
	im := h.imp[archiveTestName]
	props := *im.props
	props.Title = "Test archive"
	props.ArchiveOnly = true
	im.props = &props
	return h, &httputil.PrefixHandler{Prefix: "/custom-imports/", Handler: h}
}

func archiveUIRequest(handler http.Handler, method, path, form string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(form))
	if method == "POST" {
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	return w
}

func TestArchiveUploadPage(t *testing.T) {
	h, handler := archiveUIHost(t)
	w := archiveUIRequest(handler, "GET", "/custom-imports/"+archiveTestName, "")
	if w.Code != http.StatusOK {
		t.Fatalf("GET = %d: %s", w.Code, w.Body)
	}
	body := w.Body.String()
	for _, want := range []string{
		`Upload a Test archive ZIP`, `type="file"`, `accept=".zip,application/zip"`,
		`data-discovery-url="/metadata/?camli.mode=config"`,
		`data-archive-url="/custom-imports/archive-test/archive"`,
		`data-account-base="/custom-imports/archive-test/"`,
		`data-account-ref=""`, `data-file-ref=""`,
		`src="/custom-imports/archive-test/archive.js"`,
		`id="archive-confirm" hidden`, `id="archive-error"`, `id="archive-warnings"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("upload page missing %q:\n%s", want, body)
		}
	}
	for _, bad := range []string{`value="newacct"`, `Add new account`, `data-archive-url="https://`} {
		if strings.Contains(body, bad) {
			t.Errorf("archive-only page contains %q", bad)
		}
	}
	if accts, err := h.imp[archiveTestName].Accounts(); err != nil || len(accts) != 0 {
		t.Fatalf("page created accounts: %v, %v", accts, err)
	}
}

func TestArchiveNewAccountDoesNotCreateEmptyAccount(t *testing.T) {
	h, handler := archiveUIHost(t)
	w := archiveUIRequest(handler, "POST", "/custom-imports/"+archiveTestName, "mode=newacct")
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != h.imp[archiveTestName].URL() {
		t.Fatalf("legacy newacct = %d, %v: %s", w.Code, w.Header(), w.Body)
	}
	if accts, err := h.imp[archiveTestName].Accounts(); err != nil || len(accts) != 0 {
		t.Fatalf("legacy newacct created accounts: %v, %v", accts, err)
	}
}

func TestArchiveAccountPageRetainsInput(t *testing.T) {
	h, handler := archiveUIHost(t)
	ref := archiveTestUpload(t, h.target, "export.zip", camtypes.ArchiveIdentity{UserName: "runner"})
	result, err := h.ImportArchive(context.Background(), archiveTestName, camtypes.ArchiveImportRequest{Action: "start", FileRef: ref})
	if err != nil {
		t.Fatal(err)
	}
	waitArchiveIdle(t, h, result.AccountRef)
	path := "/custom-imports/" + archiveTestName + "/" + result.AccountRef.String()
	w := archiveUIRequest(handler, "GET", path, "")
	if w.Code != http.StatusOK {
		t.Fatalf("account GET = %d: %s", w.Code, w.Body)
	}
	body := w.Body.String()
	for _, want := range []string{
		`data-account-ref="` + result.AccountRef.String() + `"`,
		`data-file-ref="` + ref.String() + `"`,
		`id="archive-upload-form" hidden`,
		`Upload another ZIP`, `id="archive-pause"`, `manual archive uploads`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("account page missing %q:\n%s", want, body)
		}
	}
	for _, bad := range []string{`Re-login`, `Toggle auto`, `Start Import`, `<li>Running:`, `Previous run:`} {
		if strings.Contains(body, bad) {
			t.Errorf("archive-only account contains %q", bad)
		}
	}
	w = archiveUIRequest(handler, "POST", path, "mode=login")
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != h.imp[archiveTestName].URL() {
		t.Fatalf("legacy login = %d, %v: %s", w.Code, w.Header(), w.Body)
	}
	ia, err := h.imp[archiveTestName].account(result.AccountRef)
	if err != nil {
		t.Fatal(err)
	}
	if err := ia.toggleAuto(); err == nil {
		t.Error("archive-only account allowed automatic importing")
	}
	if err := ia.acct.SetAttr(attrImportAuto, "1ns"); err != nil {
		t.Fatal(err)
	}
	ia.maybeStart()
	if calls := h.imp[archiveTestName].impl.(*archiveTestImporter).calls.Load(); calls != 1 {
		t.Errorf("periodic import ran an archive-only account; calls = %d", calls)
	}
}

func TestArchiveCapabilityDoesNotHideAPISetup(t *testing.T) {
	h, handler := archiveUIHost(t)
	h.imp[archiveTestName].props.ArchiveOnly = false
	w := archiveUIRequest(handler, "GET", "/custom-imports/"+archiveTestName, "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `value="newacct"`) ||
		!strings.Contains(w.Body.String(), `id="archive-import"`) {
		t.Fatalf("hybrid importer lost API or archive setup: %d\n%s", w.Code, w.Body)
	}
	ia, err := h.imp[archiveTestName].newAccount()
	if err != nil {
		t.Fatal(err)
	}
	w = archiveUIRequest(handler, "GET", "/custom-imports/"+archiveTestName+"/"+ia.acct.PermanodeRef().String(), "")
	for _, want := range []string{"Re-login", "Toggle auto", `id="archive-import"`} {
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), want) {
			t.Fatalf("hybrid account lost %q: %d\n%s", want, w.Code, w.Body)
		}
	}
}

func TestArchiveScript(t *testing.T) {
	h, handler := archiveUIHost(t)
	for _, method := range []string{"GET", "HEAD"} {
		w := archiveUIRequest(handler, method, "/custom-imports/"+archiveTestName+"/archive.js", "")
		if w.Code != http.StatusOK || w.Header().Get("Content-Type") != "text/javascript; charset=utf-8" ||
			w.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Fatalf("%s script = %d, %v", method, w.Code, w.Header())
		}
		if method == "GET" && w.Body.String() != string(archiveScript) {
			t.Fatal("script differs from embedded archive client")
		}
		if method == "HEAD" && w.Body.Len() != 0 {
			t.Fatal("HEAD returned a script body")
		}
	}
	w := archiveUIRequest(handler, "POST", "/custom-imports/"+archiveTestName+"/archive.js", "")
	if w.Code != http.StatusMethodNotAllowed || w.Header().Get("Allow") != "GET, HEAD" {
		t.Fatalf("POST script = %d, %v", w.Code, w.Header())
	}
	legacy := h.imp["dummy1"]
	legacy.allLoaded = true
	w = archiveUIRequest(handler, "GET", "/custom-imports/dummy1", "")
	if w.Code != http.StatusOK || strings.Contains(w.Body.String(), `id="archive-import"`) ||
		!strings.Contains(w.Body.String(), `value="newacct"`) {
		t.Fatalf("legacy importer page changed: %d\n%s", w.Code, w.Body)
	}
	w = archiveUIRequest(handler, "GET", "/custom-imports/dummy1/archive.js", "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("non-archive importer script = %d", w.Code)
	}
}

func TestArchiveUploadEscapesSourceText(t *testing.T) {
	data := &archiveUploadData{
		Title:       `<img src=x onerror=alert(1)>`,
		AccountBase: "/imports/archive-test/",
		FileRef:     `" onmouseover="alert(1)`,
	}
	page, err := tmpl.Clone()
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := page.ExecuteTemplate(&buf, "archiveUpload", data); err != nil {
		t.Fatal(err)
	}
	body := buf.String()
	if strings.Contains(body, "<img") || strings.Contains(body, `data-file-ref="" onmouseover=`) ||
		!strings.Contains(body, `&lt;img`) || !strings.Contains(body, `&#34; onmouseover=&#34;`) {
		t.Fatalf("archive source text was not HTML-escaped:\n%s", body)
	}
}

func TestArchiveHostsAfterRendering(t *testing.T) {
	// Rendering one host must not execute the shared, pristine template from
	// which subsequent hosts are cloned.
	for range 2 {
		_, handler := archiveUIHost(t)
		w := archiveUIRequest(handler, "GET", "/custom-imports/"+archiveTestName, "")
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `id="archive-import"`) {
			t.Fatalf("render archive host = %d:\n%s", w.Code, w.Body)
		}
	}
}
