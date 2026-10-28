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
	"context"
	"fmt"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"

	"perkeep.org/internal/httputil"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver/memory"
	"perkeep.org/pkg/importer"
	"perkeep.org/pkg/jsonsign"
	"perkeep.org/pkg/schema"
	"perkeep.org/pkg/search"
)

type navigationSearch struct{ search.QueryDescriber }

func (navigationSearch) Query(context.Context, *search.SearchQuery) (*search.SearchResult, error) {
	return &search.SearchResult{}, nil
}

type navigationNoNetwork struct{ t *testing.T }

func (n navigationNoNetwork) RoundTrip(r *http.Request) (*http.Response, error) {
	n.t.Errorf("unexpected network request: %s", r.URL)
	return nil, fmt.Errorf("network disabled")
}

func navigationHandler(t *testing.T) http.Handler {
	t.Helper()
	ent, err := jsonsign.EntityFromSecring("26F5ABDA", "../../jsonsign/testdata/test-secring.gpg")
	if err != nil {
		t.Fatal(err)
	}
	pub, err := jsonsign.ArmoredPublicKey(ent)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := schema.NewSigner(blob.RefFromString(pub), strings.NewReader(pub), ent)
	if err != nil {
		t.Fatal(err)
	}
	h, err := importer.NewHost(importer.HostConfig{
		BaseURL:      "https://canonical.example/base",
		Prefix:       "/custom-imports/",
		Target:       new(memory.Storage),
		Signer:       signer,
		Search:       navigationSearch{},
		ClientId:     map[string]string{"instapaper": "id", "plaid": "id"},
		ClientSecret: map[string]string{"instapaper": "secret", "plaid": "secret"},
		HTTPClient:   &http.Client{Transport: navigationNoNetwork{t}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := h.Close(); err != nil {
			t.Error(err)
		}
	})
	return &httputil.PrefixHandler{Prefix: "/base/custom-imports/", Handler: h}
}

func TestLocalSetupNavigation(t *testing.T) {
	handler := navigationHandler(t)
	action := regexp.MustCompile(`<form[^>]* action="([^"]+)"`)
	account := regexp.MustCompile(`name="acct" value="([^"]+)"`)
	for _, name := range []string{"bluesky", "feed", "pinboard", "plaid", "instapaper", "raindrop", "dummy"} {
		t.Run(name, func(t *testing.T) {
			if name == "dummy" && importer.All()[name] == nil {
				// Dummy registers only when dev mode is set before package init.
				t.Setenv("CAMLI_DEV_CAMLI_ROOT", "navigation-test")
				cmd := exec.Command(os.Args[0], "-test.run=^TestLocalSetupNavigation$/^dummy$")
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("dummy subprocess: %v\n%s", err, out)
				}
				return
			}
			r := httptest.NewRequest("POST", "https://vpn.example/base/custom-imports/"+name, strings.NewReader("mode=newacct"))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			target := w.Header().Get("Location")
			wantStatus := http.StatusFound
			if name != "dummy" {
				wantStatus = http.StatusOK
				m := action.FindStringSubmatch(w.Body.String())
				if len(m) != 2 {
					t.Fatalf("missing setup form: %s", w.Body)
				}
				target = html.UnescapeString(m[1])
			}
			if w.Code != wantStatus {
				t.Fatalf("status = %d; want %d: %s", w.Code, wantStatus, w.Body)
			}
			u, err := url.Parse(target)
			if err != nil {
				t.Fatal(err)
			}
			if u.Scheme != "" || u.Host != "" || !strings.HasPrefix(target, "/") || strings.HasPrefix(target, "//") ||
				u.Path != "/base/custom-imports/"+name+"/callback" {
				t.Errorf("callback target = %q; want authority-free absolute callback path", target)
			}
			acct := u.Query().Get("acct")
			if _, ok := blob.Parse(acct); !ok || len(u.Query()) != 1 {
				t.Errorf("callback query = %q; want account blobref", u.RawQuery)
			}
			if name != "dummy" {
				if m := account.FindStringSubmatch(w.Body.String()); len(m) != 2 || m[1] != acct {
					t.Errorf("hidden account does not match callback query: %s", w.Body)
				}
			}
		})
	}
}

func TestPicasaSetupNavigation(t *testing.T) {
	r := httptest.NewRequest("GET", "https://vpn.example/base/custom-imports/picasa", nil)
	w := httptest.NewRecorder()
	navigationHandler(t).ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body)
	}
	for _, want := range []string{
		`href='/base/custom-imports/gphotos'`,
		`Authorized JavaScript origins: <b>https://canonical.example</b>`,
		`Authorized Redirect URI: <b>https://canonical.example/base/custom-imports/picasa/callback</b>`,
	} {
		if !strings.Contains(w.Body.String(), want) {
			t.Errorf("setup page missing %q:\n%s", want, w.Body)
		}
	}
}
