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
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSameOriginNavigation(t *testing.T) {
	h, handler := archiveUIHost(t)
	h.importers = []string{archiveTestName}
	im := h.imp[archiveTestName]
	ia, err := im.newAccount()
	if err != nil {
		t.Fatal(err)
	}
	const importerPath = "/custom-imports/archive-test"
	accountPath := importerPath + "/" + ia.acct.PermanodeRef().String()
	request := func(method, path, form string) *httptest.ResponseRecorder {
		// Deliberately differ from the configured https://public.example.
		return archiveUIRequest(handler, method, "https://vpn.example:8443"+path, form)
	}
	for _, tt := range []struct {
		path string
		want []string
	}{
		{"/custom-imports/", []string{`href="` + importerPath + `"`}},
		{importerPath, []string{`href="/custom-imports/"`, `href="` + accountPath + `"`}},
		{importerPath + "/", []string{`href="/custom-imports/"`, `href="` + accountPath + `"`}},
		{accountPath, []string{`href="` + importerPath + `"`}},
		{accountPath + "/", []string{`href="` + importerPath + `"`}},
	} {
		t.Run(tt.path, func(t *testing.T) {
			w := request("GET", tt.path, "")
			if w.Code != http.StatusOK {
				t.Fatalf("GET = %d: %s", w.Code, w.Body)
			}
			body := w.Body.String()
			for _, want := range tt.want {
				if !strings.Contains(body, want) {
					t.Errorf("page missing %s:\n%s", want, body)
				}
			}
			if strings.Contains(body, "https://public.example") || strings.Contains(body, `href="./"`) {
				t.Errorf("page contains a non-absolute-path navigation link:\n%s", body)
			}
		})
	}

	ia.lastRunErr = errors.New("test import failed")
	status, errs := h.AccountsStatus()
	accounts := status.([]accountStatus)
	if len(accounts) != 1 || accounts[0].Href != accountPath {
		t.Errorf("account status = %+v; want href %q", accounts, accountPath)
	}
	if len(errs) != 1 || errs[0].URL != accountPath {
		t.Errorf("status errors = %+v; want URL %q", errs, accountPath)
	}

	for _, tt := range []struct {
		path, form, want string
		code             int
	}{
		{importerPath, "mode=saveclientidsecret&clientID=id&clientSecret=secret", importerPath, http.StatusFound},
		{importerPath + "/", "mode=newacct", importerPath, http.StatusSeeOther},
		{accountPath, "mode=login", importerPath, http.StatusSeeOther},
		{accountPath, "mode=stop", accountPath, http.StatusFound},
		{accountPath + "/", "mode=stop", accountPath, http.StatusFound},
		{accountPath, "mode=delete", importerPath, http.StatusFound},
	} {
		t.Run(tt.form+tt.path, func(t *testing.T) {
			w := request("POST", tt.path, tt.form)
			if got := w.Header().Get("Location"); w.Code != tt.code || got != tt.want {
				t.Errorf("POST = %d, Location %q; want %d, %q:\n%s",
					w.Code, got, tt.code, tt.want, w.Body)
			}
		})
	}
}
