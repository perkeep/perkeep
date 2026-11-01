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
	"html"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

func TestCredentialSetupFormsUsePOST(t *testing.T) {
	handler := navigationHandler(t)
	form := regexp.MustCompile(`<form method="post" action="([^"]+)"`)
	for _, name := range []string{"feed", "pinboard", "raindrop", "plaid", "instapaper", "livejournal"} {
		t.Run(name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "https://vpn.example/base/custom-imports/"+name, strings.NewReader("mode=newacct"))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			match := form.FindStringSubmatch(w.Body.String())
			if w.Code != http.StatusOK || len(match) != 2 {
				t.Fatalf("setup has no POST credential form (HTTP %d): %s", w.Code, w.Body)
			}
			callback := "https://vpn.example" + html.UnescapeString(match[1])

			// Local credential forms must not accept secrets through GET
			// query parameters, where browser history and logs retain them.
			w = httptest.NewRecorder()
			handler.ServeHTTP(w, httptest.NewRequest("GET", callback, nil))
			if w.Code != http.StatusMethodNotAllowed {
				t.Fatalf("GET callback returned %d; want 405", w.Code)
			}

			r = httptest.NewRequest("POST", callback, strings.NewReader("apiToken=not-a-real-secret"))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			r.Header.Set("Origin", "https://attacker.example")
			w = httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			if w.Code != http.StatusForbidden {
				t.Fatalf("cross-origin credential POST returned %d; want 403", w.Code)
			}
		})
	}
}
