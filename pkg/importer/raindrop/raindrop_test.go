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

package raindrop

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"perkeep.org/pkg/importer"
	imptest "perkeep.org/pkg/importer/test"
)

type roundTripper func(*http.Request) (*http.Response, error)

func (f roundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestRunProgress(t *testing.T) {
	when := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	bookmarks := []Bookmark{
		{ID: 1, Link: "https://example.com/1", Title: "One", Created: when, LastUpdate: when},
		{ID: 2, Link: "https://example.com/2", Title: "Two", Created: when, LastUpdate: when},
	}
	// The API serves the bookmarks a page at a time, each with the count of
	// all of them.
	transport := roundTripper(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "api.raindrop.io" || r.Header.Get("Authorization") != "Bearer token" {
			t.Errorf("unexpected request %s", r.URL)
		}
		resp := CollectionResponse{Result: true, Count: len(bookmarks)}
		if r.URL.Query().Get("page") == "0" {
			resp.Items = bookmarks
		}
		j, err := json.Marshal(resp)
		if err != nil {
			t.Fatal(err)
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(j)))}, nil
	})
	h := imptest.NewMemoryHost(t, importer.HostConfig{HTTPClient: &http.Client{Transport: transport}})
	t.Cleanup(func() { h.Close() })
	rc, err := importer.CreateAccount(h, "raindrop")
	if err != nil {
		t.Fatal(err)
	}
	if err := rc.AccountNode().SetAttrs(importer.KV(attrAuthToken, "token"), importer.KV(importer.AcctAttrUserName, "gina")); err != nil {
		t.Fatal(err)
	}

	for i := range 2 {
		if err := (imp{}).Run(rc); err != nil {
			t.Fatal(err)
		}
		// The run's progress bar, which the account's page shows, of the
		// API's count. The runs share rc's bars, so the second counts the
		// bookmarks, unchanged, as skipped, on the first's.
		bars := rc.Progress().Snapshot()
		if len(bars) != 1 || bars[0].Name != "bookmarks" || !bars[0].Finished || bars[0].Total != 2 || bars[0].Skipped != int64(2*i) {
			t.Errorf("run %d's progress bars: %+v", i, bars)
		}
	}
}
