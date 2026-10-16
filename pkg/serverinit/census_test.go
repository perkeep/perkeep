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

package serverinit_test

import (
	"crypto/sha1"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/synctest"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver"
	_ "perkeep.org/pkg/blobserver/cond"
	_ "perkeep.org/pkg/blobserver/memory"
	_ "perkeep.org/pkg/blobserver/replica"
	_ "perkeep.org/pkg/blobserver/shard"
	"perkeep.org/pkg/health"
	"perkeep.org/pkg/serverinit"
)

func TestCensus(t *testing.T) {
	t.Setenv("CAMLI_CONFIG_DIR", t.TempDir())
	synctest.Test(t, func(t *testing.T) {
		confData, err := json.Marshal(map[string]any{
			"handlerConfig": true,
			"auth":          "none",
			"prefixes": map[string]any{
				"/a/": map[string]any{"handler": "storage-memory"},
				"/b/": map[string]any{"handler": "storage-memory"},
				"/c/": map[string]any{"handler": "storage-memory"},
				"/cond/": map[string]any{
					"handler":     "storage-cond",
					"handlerArgs": map[string]any{"write": "/a/", "read": "/a/"},
				},
				"/rep/": map[string]any{
					"handler":     "storage-replica",
					"handlerArgs": map[string]any{"backends": []string{"/a/", "/b/"}},
				},
				"/shard/": map[string]any{
					"handler":     "storage-shard",
					"handlerArgs": map[string]any{"backends": []string{"/a/", "/c/"}},
				},
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		conf, err := serverinit.Load(confData)
		if err != nil {
			t.Fatal(err)
		}
		mon := health.New()
		defer mon.Close()
		conf.SetHealth(mon)
		mux := http.NewServeMux()
		shutdown, err := conf.InstallHandlers(mux, "http://example.invalid")
		if err != nil {
			t.Fatal(err)
		}
		defer shutdown.Close()

		put := func(pfx, contents string) {
			t.Helper()
			br := blob.RefFromString(contents)
			req := httptest.NewRequest("PUT", pfx+"camli/"+br.String(), strings.NewReader(contents))
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			if rec.Code >= 300 {
				t.Fatalf("PUT to %s: %d %s", pfx, rec.Code, rec.Body)
			}
		}
		for _, s := range []string{"one", "two", "three"} {
			put("/a/", s)
			put("/b/", s)
		}
		put("/c/", "four")
		put("/c/", "five")

		mon.EndStartup()
		synctest.Wait()

		blobs := func(pfx string) int64 {
			t.Helper()
			for _, c := range mon.Status().Checks {
				if c.ID != blobserver.CensusID(pfx) {
					continue
				}
				ests, ok := c.Details.([]blobserver.HashEstimate)
				if !ok {
					t.Fatalf("%s census: %v %q, details %#v; want estimates", pfx, c.State, c.Message, c.Details)
				}
				var n int64
				for _, e := range ests {
					if !e.Exact {
						t.Errorf("%s census estimate %+v isn't exact", pfx, e)
					}
					n += e.Blobs
				}
				return n
			}
			t.Fatalf("no census check for %s", pfx)
			return 0
		}
		for pfx, want := range map[string]int64{
			"/a/":     3,
			"/b/":     3,
			"/c/":     2,
			"/cond/":  3, // its read storage, /a/
			"/rep/":   3, // the largest of /a/ and /b/
			"/shard/": 5, // the sum of /a/ and /c/
		} {
			if got := blobs(pfx); got != want {
				t.Errorf("%s census has %d blobs; want %d", pfx, got, want)
			}
		}

		// A blob of a hash type that /a/ had none of, received after the
		// census ran (through /cond/, which writes to /a/), adds the
		// type to /a/'s census, and to that of the storage made of it.
		data := "a sha1 blob"
		br, ok := blob.Parse(fmt.Sprintf("sha1-%x", sha1.Sum([]byte(data))))
		if !ok {
			t.Fatal("can't make a sha1 ref")
		}
		req := httptest.NewRequest("PUT", "/cond/camli/"+br.String(), strings.NewReader(data))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code >= 300 {
			t.Fatalf("PUT: %d %s", rec.Code, rec.Body)
		}
		sha1Blobs := func(pfx string) int64 {
			t.Helper()
			for _, c := range mon.Status().Checks {
				if c.ID == blobserver.CensusID(pfx) {
					for _, e := range c.Details.([]blobserver.HashEstimate) {
						if e.Hash == "sha1" {
							return e.Blobs
						}
					}
				}
			}
			return 0
		}
		for pfx, want := range map[string]int64{"/a/": 1, "/cond/": 1, "/rep/": 1, "/shard/": 1, "/b/": 0, "/c/": 0} {
			if got := sha1Blobs(pfx); got != want {
				t.Errorf("after receiving a sha1 blob into /a/, %s census has %d sha1 blobs; want %d", pfx, got, want)
			}
		}
	})
}
